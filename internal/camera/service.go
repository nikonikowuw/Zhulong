package camera

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

const (
	MaxCameraCapacity = 128
)

// CameraService manages camera domain lifecycle, validation gates, credential encryption, and state orchestration.
type CameraService struct {
	store     CameraStore
	cipher    Cipher
	prober    *ProbeService
	registry  *StateRegistry
	hub       *EventHub
	scheduler *HealthScheduler
	logger    *zap.Logger
}

type buildStreamsParams struct {
	cameraID  string
	mainProto string
	mainInfo  *StreamConnectionInfo
	subInfo   *StreamConnectionInfo
	mainProbe *StreamProbeResult
	subProbe  *StreamProbeResult
}

type encryptStreamParams struct {
	camID string
	role  string
	proto string
	info  *StreamConnectionInfo
	probe *StreamProbeResult
}

// NewCameraService creates a new CameraService.
func NewCameraService(
	store CameraStore,
	cipher Cipher,
	prober *ProbeService,
	registry *StateRegistry,
	hub *EventHub,
	scheduler *HealthScheduler,
	logger *zap.Logger,
) *CameraService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &CameraService{
		store:     store,
		cipher:    cipher,
		prober:    prober,
		registry:  registry,
		hub:       hub,
		scheduler: scheduler,
		logger:    logger.Named("camera.service"),
	}
}

// ensureCapacity rejects creation once the camera table is full. It is shared by
// every create path so a new protocol cannot quietly skip the budget check.
func (s *CameraService) ensureCapacity(ctx context.Context) error {
	_, total, err := s.store.List(ctx, 1, 0)
	if err != nil {
		return apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to query camera capacity",
			err,
		)
	}
	if total >= MaxCameraCapacity {
		return apperr.New(
			apperr.KindInvalid,
			"CAMERA_LIMIT_EXCEEDED",
			"Maximum camera capacity reached",
			nil,
		)
	}
	return nil
}

// persistCamera writes a new camera row, initialises its runtime state and
// broadcasts the change.
//
// probedRoles lists the stream roles whose credentials were proven by an RTSP
// DESCRIBE; an empty list leaves the state unknown, which is the honest answer for a
// source that has not been probed yet (a GB28181 channel has no RTSP endpoint).
func (s *CameraService) persistCamera(
	ctx context.Context,
	camera *Camera,
	streams []CameraStream,
	probedRoles []string,
) (*CameraResponse, error) {
	if err := s.store.Create(ctx, camera, streams); err != nil {
		if errors.Is(err, ErrDuplicateCamera) {
			return nil, apperr.New(
				apperr.KindConflict,
				"CAMERA_ALREADY_EXISTS",
				"Camera already exists",
				err,
			)
		}
		return nil, apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to create camera record",
			err,
		)
	}

	state := s.registry.InitCameraState(camera)
	if camera.Enabled {
		for _, role := range probedRoles {
			state = s.registry.RecordStreamSuccess(
				camera.ID,
				camera.Revision,
				role,
				EvidenceRTSPDescribe,
			)
		}
	}
	s.hub.BroadcastChange(state)
	return s.buildCameraResponse(camera, state), nil
}

// Create validates stream connections through the 3-5s probe gate, encrypts credentials, and persists the camera.
func (s *CameraService) Create(ctx context.Context, req CreateCameraRequest) (*CameraResponse, error) {
	// 1. Capacity limit check
	if err := s.ensureCapacity(ctx); err != nil {
		return nil, err
	}

	// 2. Validate protocols
	mainProto, err := validateStreamProtocol(req.MainStream.Protocol)
	if err != nil {
		return nil, err
	}
	if req.SubStream != nil {
		if _, err := validateStreamProtocol(req.SubStream.Protocol); err != nil {
			return nil, err
		}
	}

	// 3. Parse and normalize RTSP URLs
	mainInfo, err := ParseAndNormalizeRTSP(req.MainStream.RTSPURL, req.MainStream.Transport)
	if err != nil {
		return nil, apperr.New(
			apperr.KindInvalid,
			"CAMERA_INVALID_PARAM",
			"Invalid main stream URL format",
			err,
		)
	}

	var subInfo *StreamConnectionInfo
	if req.SubStream != nil && strings.TrimSpace(req.SubStream.RTSPURL) != "" {
		subInfo, err = ParseAndNormalizeRTSP(req.SubStream.RTSPURL, req.SubStream.Transport)
		if err != nil {
			return nil, apperr.New(
				apperr.KindInvalid,
				"CAMERA_INVALID_PARAM",
				"Invalid sub stream URL format",
				err,
			)
		}
	}

	// 4. Atomic Probe Validation Gate (3-5s budget)
	mainProbe, subProbe, err := s.prober.ValidateStreams(
		ctx,
		mainInfo,
		subInfo,
		5*time.Second,
	)
	if err != nil {
		return nil, err // Returns mapped semantic apperr directly
	}

	// 5. Generate Camera ID if absent
	camID := strings.TrimSpace(req.ID)
	if camID == "" {
		camID = idutil.New()
	}

	// 6. Encrypt Stream Credentials with AES-GCM and AAD
	streams, err := s.buildEncryptedStreams(buildStreamsParams{
		cameraID:  camID,
		mainProto: mainProto,
		mainInfo:  mainInfo,
		subInfo:   subInfo,
		mainProbe: mainProbe,
		subProbe:  subProbe,
	})
	if err != nil {
		return nil, err
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	camera := &Camera{
		ID:       camID,
		Name:     strings.TrimSpace(req.Name),
		Enabled:  enabled,
		Revision: 1,
	}

	// 7. Persist and publish state; the probe gate above is the evidence for both roles
	probedRoles := []string{StreamRoleMain}
	if subInfo != nil {
		probedRoles = append(probedRoles, StreamRoleSub)
	}
	return s.persistCamera(
		ctx,
		camera,
		streams,
		probedRoles,
	)
}

// CreateGB28181CameraParams defines parameters for creating a camera from a GB28181 channel.
type CreateGB28181CameraParams struct {
	DeviceID         string
	ChannelID        string
	Name             string
	ResolutionWidth  int
	ResolutionHeight int
}

// CreateGB28181Camera creates a camera bound to protocol gb28181 without executing
// the RTSP atomic probe gate.
func (s *CameraService) CreateGB28181Camera(
	ctx context.Context,
	params CreateGB28181CameraParams,
) (*CameraResponse, error) {
	deviceID := strings.TrimSpace(params.DeviceID)
	channelID := strings.TrimSpace(params.ChannelID)
	if !IsGB28181ID(deviceID) || !IsGB28181ID(channelID) {
		return nil, apperr.New(
			apperr.KindInvalid,
			"CAMERA_INVALID_PARAM",
			"device ID and channel ID must be 20 digits",
			nil,
		)
	}

	// 1. Capacity limit check
	if err := s.ensureCapacity(ctx); err != nil {
		return nil, err
	}

	// 2. Generate Camera ID
	camID := idutil.New()

	// 3. Encrypt URI
	uri := ProtocolGB28181 + "://" + deviceID + "/" + channelID
	mainAAD := MakeAAD(camID, StreamRoleMain)
	mainCiphertext, err := s.cipher.Encrypt([]byte(uri), mainAAD)
	if err != nil {
		return nil, apperr.New(
			apperr.KindInternal,
			"ENCRYPTION_ERROR",
			"Failed to encrypt stream credentials",
			err,
		)
	}

	stream := CameraStream{
		CameraID:       camID,
		Role:           StreamRoleMain,
		Protocol:       ProtocolGB28181,
		EncryptedURI:   mainCiphertext,
		Transport:      TransportUDP,
		Codec:          "",
		Width:          params.ResolutionWidth,
		Height:         params.ResolutionHeight,
		FPSNumerator:   0,
		FPSDenominator: 1,
	}

	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = channelID
	}

	camera := &Camera{
		ID:       camID,
		Name:     name,
		Enabled:  true,
		Revision: 1,
	}

	// 4. Persist and publish state. No RTSP probe ran, so no role is marked checked.
	return s.persistCamera(
		ctx,
		camera,
		[]CameraStream{stream},
		nil,
	)
}

// Get returns public status and sanitized stream information for a camera.
func (s *CameraService) Get(ctx context.Context, id string) (*CameraResponse, error) {
	cam, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return nil, apperr.New(
				apperr.KindNotFound,
				"CAMERA_NOT_FOUND",
				"Camera not found",
				err,
			)
		}
		return nil, apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to get camera",
			err,
		)
	}

	state, ok := s.registry.GetState(id)
	if !ok {
		state = s.registry.InitCameraState(cam)
	}

	return s.buildCameraResponse(cam, state), nil
}

// GetCredentials returns decrypted stream URLs for authorized administrator inspection/copying.
func (s *CameraService) GetCredentials(ctx context.Context, id string) (*CameraCredentialsResponse, error) {
	cam, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return nil, apperr.New(
				apperr.KindNotFound,
				"CAMERA_NOT_FOUND",
				"Camera not found",
				err,
			)
		}
		return nil, apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to get camera credentials",
			err,
		)
	}

	creds := make(map[string]string, len(cam.Streams))
	for _, stream := range cam.Streams {
		decrypted, err := s.decryptStreamURI(id, &stream)
		if err != nil {
			s.logger.Error("failed to decrypt credentials",
				zap.String("camera_id", id),
				zap.String("role", stream.Role),
				zap.Error(err),
			)
			continue
		}
		creds[stream.Role] = decrypted
	}

	return &CameraCredentialsResponse{
		CameraID:    id,
		Credentials: creds,
	}, nil
}

// List returns a paginated list of cameras merged with runtime health states.
func (s *CameraService) List(ctx context.Context, limit, offset int) ([]CameraResponse, int64, error) {
	cameras, total, err := s.store.List(ctx, limit, offset)
	if err != nil {
		return nil, 0, apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to list cameras",
			err,
		)
	}

	states := s.registry.GetAllStates()
	responses := make([]CameraResponse, len(cameras))
	for i := range cameras {
		cam := &cameras[i]
		st, ok := states[cam.ID]
		if !ok {
			st = s.registry.InitCameraState(cam)
		}
		responses[i] = *s.buildCameraResponse(cam, st)
	}

	return responses, total, nil
}

// Update updates an existing camera's name, enabled switch, or stream connections with revision CAS check.
func (s *CameraService) Update(ctx context.Context, id string, req UpdateCameraRequest) (*CameraResponse, error) {
	current, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return nil, apperr.New(
				apperr.KindNotFound,
				"CAMERA_NOT_FOUND",
				"Camera not found",
				err,
			)
		}
		return nil, apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to get camera for update",
			err,
		)
	}

	if current.Revision != req.Revision {
		return nil, apperr.New(
			apperr.KindConflict,
			"CAMERA_REVISION_CONFLICT",
			"Camera configuration revision conflict",
			nil,
		)
	}

	var updatedStreams []CameraStream
	streamsChanged := req.MainStream != nil || req.SubStream != nil

	if streamsChanged {
		var err error
		updatedStreams, err = s.prepareUpdatedStreams(
			ctx,
			id,
			current,
			req,
		)
		if err != nil {
			return nil, err
		}
	}

	updatedCamera, err := s.store.Update(ctx, UpdateCameraParams{
		ID:               id,
		ExpectedRevision: req.Revision,
		Name:             req.Name,
		Enabled:          req.Enabled,
		Streams:          updatedStreams,
		UpdateStreams:    streamsChanged,
	})
	if err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			return nil, apperr.New(
				apperr.KindConflict,
				"CAMERA_REVISION_CONFLICT",
				"Camera configuration revision conflict",
				err,
			)
		}
		return nil, apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to update camera",
			err,
		)
	}

	state := s.registry.InitCameraState(updatedCamera)
	if updatedCamera.Enabled && streamsChanged {
		state = s.registry.RecordStreamSuccess(
			id,
			updatedCamera.Revision,
			StreamRoleMain,
			EvidenceRTSPDescribe,
		)
		if len(updatedCamera.Streams) > 1 {
			state = s.registry.RecordStreamSuccess(
				id,
				updatedCamera.Revision,
				StreamRoleSub,
				EvidenceRTSPDescribe,
			)
		}
	}
	s.hub.BroadcastChange(state)

	return s.buildCameraResponse(updatedCamera, state), nil
}

func (s *CameraService) prepareUpdatedStreams(
	ctx context.Context,
	id string,
	current *Camera,
	req UpdateCameraRequest,
) ([]CameraStream, error) {
	mainReq := req.MainStream
	if mainReq == nil {
		for _, st := range current.Streams {
			if st.Role != StreamRoleMain {
				continue
			}
			if rawURI, decErr := s.decryptStreamURI(id, &st); decErr == nil {
				mainReq = &CreateStreamRequest{
					Role:      StreamRoleMain,
					Protocol:  st.Protocol,
					RTSPURL:   rawURI,
					Transport: st.Transport,
				}
			}
			break
		}
	}
	if mainReq == nil {
		return nil, apperr.New(
			apperr.KindInvalid,
			"CAMERA_STREAM_REQUIRED",
			"Main stream is required",
			nil,
		)
	}

	mainInfo, err := ParseAndNormalizeRTSP(mainReq.RTSPURL, mainReq.Transport)
	if err != nil {
		return nil, apperr.New(
			apperr.KindInvalid,
			"CAMERA_INVALID_PARAM",
			"Invalid main stream URL format",
			err,
		)
	}

	var subInfo *StreamConnectionInfo
	switch {
	case req.SubStream != nil && strings.TrimSpace(req.SubStream.RTSPURL) != "":
		subInfo, err = ParseAndNormalizeRTSP(req.SubStream.RTSPURL, req.SubStream.Transport)
		if err != nil {
			return nil, apperr.New(
				apperr.KindInvalid,
				"CAMERA_INVALID_PARAM",
				"Invalid sub stream URL format",
				err,
			)
		}
	case req.SubStream == nil:
		for _, st := range current.Streams {
			if st.Role != StreamRoleSub {
				continue
			}
			rawURI, decErr := s.decryptStreamURI(id, &st)
			if decErr != nil {
				break
			}
			parsedSub, parseErr := ParseAndNormalizeRTSP(rawURI, st.Transport)
			if parseErr != nil {
				s.logger.Warn("failed to parse existing sub stream URI during update",
					zap.String("camera_id", id),
					zap.Error(parseErr),
				)
				break
			}
			subInfo = parsedSub
			break
		}
	}

	mainProbe, subProbe, err := s.prober.ValidateStreams(
		ctx,
		mainInfo,
		subInfo,
		5*time.Second,
	)
	if err != nil {
		return nil, err
	}

	return s.buildEncryptedStreams(buildStreamsParams{
		cameraID:  id,
		mainProto: ProtocolRTSP,
		mainInfo:  mainInfo,
		subInfo:   subInfo,
		mainProbe: mainProbe,
		subProbe:  subProbe,
	})
}

// Delete deletes a camera and cleans up runtime state.
func (s *CameraService) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return apperr.New(
				apperr.KindNotFound,
				"CAMERA_NOT_FOUND",
				"Camera not found",
				err,
			)
		}
		return apperr.New(
			apperr.KindInternal,
			"DATABASE_ERROR",
			"Failed to delete camera",
			err,
		)
	}

	s.registry.DeleteState(id)
	s.hub.BroadcastChange(&CameraStateInfo{
		CameraID: id,
		Health:   HealthStateUnknown,
		Reason:   "deleted",
	})
	return nil
}

// Diagnose triggers an immediate manual health probe and returns diagnostic results.
func (s *CameraService) Diagnose(ctx context.Context, id string) (*DiagnoseResponse, error) {
	state, err := s.scheduler.TriggerCheck(ctx, id)
	if err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return nil, apperr.New(
				apperr.KindNotFound,
				"CAMERA_NOT_FOUND",
				"Camera not found",
				err,
			)
		}
		return nil, apperr.New(
			apperr.KindInternal,
			"DIAGNOSE_ERROR",
			"Failed to execute manual probe",
			err,
		)
	}

	msg := "Diagnostic check completed successfully"
	if state != nil && state.Health != HealthStateOnline {
		msg = fmt.Sprintf("Diagnostic check finished: health is %s (%s)", state.Health, state.Reason)
	}

	return &DiagnoseResponse{
		CameraID: id,
		State:    state,
		Message:  msg,
	}, nil
}

func (s *CameraService) encryptStream(params encryptStreamParams) (CameraStream, error) {
	aad := MakeAAD(params.camID, params.role)
	ciphertext, err := s.cipher.Encrypt([]byte(params.info.NormalizedURI), aad)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to encrypt %q stream credentials", params.role)
		return CameraStream{}, apperr.New(
			apperr.KindInternal,
			"ENCRYPTION_ERROR",
			errMsg,
			err,
		)
	}

	return CameraStream{
		CameraID:       params.camID,
		Role:           params.role,
		Protocol:       params.proto,
		EncryptedURI:   ciphertext,
		Transport:      params.info.Transport,
		Codec:          params.probe.Codec,
		Width:          params.probe.Width,
		Height:         params.probe.Height,
		FPSNumerator:   params.probe.FPSNumerator,
		FPSDenominator: params.probe.FPSDenominator,
	}, nil
}

func (s *CameraService) buildEncryptedStreams(params buildStreamsParams) ([]CameraStream, error) {
	mainStream, err := s.encryptStream(encryptStreamParams{
		camID: params.cameraID,
		role:  StreamRoleMain,
		proto: params.mainProto,
		info:  params.mainInfo,
		probe: params.mainProbe,
	})
	if err != nil {
		return nil, err
	}

	streams := []CameraStream{mainStream}
	if params.subInfo != nil && params.subProbe != nil {
		subStream, err := s.encryptStream(encryptStreamParams{
			camID: params.cameraID,
			role:  StreamRoleSub,
			proto: ProtocolRTSP,
			info:  params.subInfo,
			probe: params.subProbe,
		})
		if err != nil {
			return nil, err
		}
		streams = append(streams, subStream)
	}

	return streams, nil
}

func (s *CameraService) buildCameraResponse(cam *Camera, state *CameraStateInfo) *CameraResponse {
	resp := &CameraResponse{
		ID:        cam.ID,
		Name:      cam.Name,
		Enabled:   cam.Enabled,
		Revision:  cam.Revision,
		Health:    HealthStateUnknown,
		Session:   SessionStateIdle,
		CreatedAt: cam.CreatedAt,
		UpdatedAt: cam.UpdatedAt,
	}

	if state != nil {
		resp.Health = state.Health
		resp.Session = state.Session
		resp.Degraded = state.Degraded
		resp.Stale = state.Stale
		resp.Reason = state.Reason
		resp.LastCheckedAt = state.LastCheckedAt
		resp.LastSuccessAt = state.LastSuccessAt
	}

	streamResponses := make([]StreamResponse, len(cam.Streams))
	for i, stream := range cam.Streams {
		fullURL := "[ENCRYPTED]"
		if decrypted, err := s.decryptStreamURI(cam.ID, &stream); err == nil {
			fullURL = decrypted
		}

		var stInfo *StreamStateInfo
		if state != nil && state.Streams != nil {
			stInfo = state.Streams[stream.Role]
		}

		streamResponses[i] = StreamResponse{
			ID:           stream.ID,
			Role:         stream.Role,
			Protocol:     stream.Protocol,
			RTSPURL:      fullURL,
			Host:         streamHost(fullURL),
			Transport:    stream.Transport,
			Codec:        stream.Codec,
			Width:        stream.Width,
			Height:       stream.Height,
			FPS:          stream.FPSValue(),
			FPSString:    stream.FormatFPS(),
			CreatedAt:    stream.CreatedAt,
			UpdatedAt:    stream.UpdatedAt,
			RuntimeState: stInfo,
		}
	}
	resp.Streams = streamResponses

	return resp
}

// streamHost extracts the target host from a decrypted stream URI for display and search.
// It deliberately returns an empty string instead of a placeholder when the URI is missing,
// still encrypted, or unparsable — callers must not fabricate a host.
func streamHost(rawURI string) string {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func (s *CameraService) decryptStreamURI(cameraID string, stream *CameraStream) (string, error) {
	aad := MakeAAD(cameraID, stream.Role)
	decrypted, err := s.cipher.Decrypt(stream.EncryptedURI, aad)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func validateStreamProtocol(proto string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(proto))
	if p == "" {
		p = ProtocolRTSP
	}
	if p != ProtocolRTSP {
		return "", apperr.New(
			apperr.KindInvalid,
			"CAMERA_PROTOCOL_UNSUPPORTED",
			"Only RTSP protocol is supported",
			nil,
		)
	}
	return p, nil
}
