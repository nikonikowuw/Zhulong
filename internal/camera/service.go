package camera

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
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
	createMu  sync.Mutex
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

// Create validates stream connections through the 3-5s probe gate, encrypts credentials, and persists the camera.
func (s *CameraService) Create(ctx context.Context, req CreateCameraRequest) (*CameraResponse, error) {
	// 1. Capacity limit check
	_, total, err := s.store.List(ctx, 1, 0)
	if err != nil {
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to query camera capacity", err)
	}
	if total >= MaxCameraCapacity {
		return nil, apperr.New(apperr.KindInvalid, "CAMERA_LIMIT_EXCEEDED", "Maximum camera capacity reached", nil)
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
		return nil, apperr.New(apperr.KindInvalid, "CAMERA_INVALID_PARAM", "Invalid main stream URL format", err)
	}

	var subInfo *StreamConnectionInfo
	if req.SubStream != nil && strings.TrimSpace(req.SubStream.RTSPURL) != "" {
		subInfo, err = ParseAndNormalizeRTSP(req.SubStream.RTSPURL, req.SubStream.Transport)
		if err != nil {
			return nil, apperr.New(apperr.KindInvalid, "CAMERA_INVALID_PARAM", "Invalid sub stream URL format", err)
		}
	}

	// 4. Atomic Probe Validation Gate (3-5s budget)
	mainProbe, subProbe, err := s.prober.ValidateStreams(ctx, mainInfo, subInfo, 5*time.Second)
	if err != nil {
		return nil, err // Returns mapped semantic apperr directly
	}

	// 5. Generate Camera ID if absent
	camID := strings.TrimSpace(req.ID)
	if camID == "" {
		s.createMu.Lock()
		defer s.createMu.Unlock()

		nextID, err := s.store.GetNextNumericID(ctx)
		if err != nil {
			return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to allocate camera ID", err)
		}
		camID = nextID
	}

	// 6. Encrypt Stream Credentials with AES-GCM and AAD
	streams, err := s.buildEncryptedStreams(camID, mainInfo, subInfo, mainProbe, subProbe, mainProto)
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

	// 7. Persist to DB in short transaction
	if err := s.store.Create(ctx, camera, streams); err != nil {
		if errors.Is(err, ErrDuplicateCamera) {
			return nil, apperr.New(apperr.KindConflict, "CAMERA_ALREADY_EXISTS", "Camera already exists", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to create camera record", err)
	}

	// 8. Register in Runtime State Registry
	state := s.registry.InitCameraState(camera)
	if camera.Enabled {
		// Probe passed, mark main stream as checked
		state = s.registry.RecordStreamSuccess(camID, camera.Revision, StreamRoleMain, EvidenceRTSPDescribe)
		if subInfo != nil {
			state = s.registry.RecordStreamSuccess(camID, camera.Revision, StreamRoleSub, EvidenceRTSPDescribe)
		}
	}
	s.hub.BroadcastChange(state)

	return s.buildCameraResponse(camera, state), nil
}

// Get returns public status and sanitized stream information for a camera.
func (s *CameraService) Get(ctx context.Context, id string) (*CameraResponse, error) {
	cam, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return nil, apperr.New(apperr.KindNotFound, "CAMERA_NOT_FOUND", "Camera not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get camera", err)
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
			return nil, apperr.New(apperr.KindNotFound, "CAMERA_NOT_FOUND", "Camera not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get camera credentials", err)
	}

	creds := make(map[string]string, len(cam.Streams))
	for _, stream := range cam.Streams {
		decrypted, err := s.decryptStreamURI(id, &stream)
		if err != nil {
			s.logger.Error("failed to decrypt credentials", zap.String("camera_id", id), zap.String("role", stream.Role), zap.Error(err))
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
		return nil, 0, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list cameras", err)
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
			return nil, apperr.New(apperr.KindNotFound, "CAMERA_NOT_FOUND", "Camera not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get camera for update", err)
	}

	if current.Revision != req.Revision {
		return nil, apperr.New(apperr.KindConflict, "CAMERA_REVISION_CONFLICT", "Camera configuration revision conflict", nil)
	}

	var updatedStreams []CameraStream
	streamsChanged := req.MainStream != nil || req.SubStream != nil

	if streamsChanged {
		mainReq := req.MainStream
		if mainReq == nil {
			// Find existing main stream to preserve
			for _, st := range current.Streams {
				if st.Role == StreamRoleMain {
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
		}
		if mainReq == nil {
			return nil, apperr.New(apperr.KindInvalid, "CAMERA_STREAM_REQUIRED", "Main stream is required", nil)
		}

		mainInfo, err := ParseAndNormalizeRTSP(mainReq.RTSPURL, mainReq.Transport)
		if err != nil {
			return nil, apperr.New(apperr.KindInvalid, "CAMERA_INVALID_PARAM", "Invalid main stream URL format", err)
		}

		var subInfo *StreamConnectionInfo
		if req.SubStream != nil && strings.TrimSpace(req.SubStream.RTSPURL) != "" {
			subInfo, err = ParseAndNormalizeRTSP(req.SubStream.RTSPURL, req.SubStream.Transport)
			if err != nil {
				return nil, apperr.New(apperr.KindInvalid, "CAMERA_INVALID_PARAM", "Invalid sub stream URL format", err)
			}
		} else if req.SubStream == nil {
			// Symmetrically preserve existing sub stream if present
			for _, st := range current.Streams {
				if st.Role == StreamRoleSub {
					if rawURI, decErr := s.decryptStreamURI(id, &st); decErr == nil {
						subInfo, _ = ParseAndNormalizeRTSP(rawURI, st.Transport)
					}
					break
				}
			}
		}

		mainProbe, subProbe, err := s.prober.ValidateStreams(ctx, mainInfo, subInfo, 5*time.Second)
		if err != nil {
			return nil, err
		}

		updatedStreams, err = s.buildEncryptedStreams(id, mainInfo, subInfo, mainProbe, subProbe, ProtocolRTSP)
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
	})
	if err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			return nil, apperr.New(apperr.KindConflict, "CAMERA_REVISION_CONFLICT", "Camera configuration revision conflict", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to update camera", err)
	}

	state := s.registry.InitCameraState(updatedCamera)
	if updatedCamera.Enabled && streamsChanged {
		state = s.registry.RecordStreamSuccess(id, updatedCamera.Revision, StreamRoleMain, EvidenceRTSPDescribe)
		if len(updatedCamera.Streams) > 1 {
			state = s.registry.RecordStreamSuccess(id, updatedCamera.Revision, StreamRoleSub, EvidenceRTSPDescribe)
		}
	}
	s.hub.BroadcastChange(state)

	return s.buildCameraResponse(updatedCamera, state), nil
}

// Delete deletes a camera and cleans up runtime state.
func (s *CameraService) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		if errors.Is(err, ErrCameraNotFound) {
			return apperr.New(apperr.KindNotFound, "CAMERA_NOT_FOUND", "Camera not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete camera", err)
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
			return nil, apperr.New(apperr.KindNotFound, "CAMERA_NOT_FOUND", "Camera not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DIAGNOSE_ERROR", "Failed to execute manual probe", err)
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

func (s *CameraService) buildEncryptedStreams(
	camID string,
	mainInfo *StreamConnectionInfo,
	subInfo *StreamConnectionInfo,
	mainProbe *StreamProbeResult,
	subProbe *StreamProbeResult,
	mainProto string,
) ([]CameraStream, error) {
	mainAAD := MakeAAD(camID, StreamRoleMain)
	mainCiphertext, err := s.cipher.Encrypt([]byte(mainInfo.NormalizedURI), mainAAD)
	if err != nil {
		return nil, apperr.New(apperr.KindInternal, "ENCRYPTION_ERROR", "Failed to encrypt main stream credentials", err)
	}

	streams := []CameraStream{
		{
			CameraID:       camID,
			Role:           StreamRoleMain,
			Protocol:       mainProto,
			EncryptedURI:   mainCiphertext,
			Transport:      mainInfo.Transport,
			Codec:          mainProbe.Codec,
			Width:          mainProbe.Width,
			Height:         mainProbe.Height,
			FPSNumerator:   mainProbe.FPSNumerator,
			FPSDenominator: mainProbe.FPSDenominator,
		},
	}

	if subInfo != nil && subProbe != nil {
		subAAD := MakeAAD(camID, StreamRoleSub)
		subCiphertext, err := s.cipher.Encrypt([]byte(subInfo.NormalizedURI), subAAD)
		if err != nil {
			return nil, apperr.New(apperr.KindInternal, "ENCRYPTION_ERROR", "Failed to encrypt sub stream credentials", err)
		}
		streams = append(streams, CameraStream{
			CameraID:       camID,
			Role:           StreamRoleSub,
			Protocol:       ProtocolRTSP,
			EncryptedURI:   subCiphertext,
			Transport:      subInfo.Transport,
			Codec:          subProbe.Codec,
			Width:          subProbe.Width,
			Height:         subProbe.Height,
			FPSNumerator:   subProbe.FPSNumerator,
			FPSDenominator: subProbe.FPSDenominator,
		})
	}

	return streams, nil
}

func (s *CameraService) buildCameraResponse(cam *Camera, state *CameraStateInfo) *CameraResponse {
	resp := &CameraResponse{
		ID:        cam.ID,
		Name:      cam.Name,
		Enabled:   cam.Enabled,
		Revision:  cam.Revision,
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
	} else {
		resp.Health = HealthStateUnknown
		resp.Session = SessionStateIdle
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
		return "", apperr.New(apperr.KindInvalid, "CAMERA_PROTOCOL_UNSUPPORTED", "Only RTSP protocol is supported", nil)
	}
	return p, nil
}
