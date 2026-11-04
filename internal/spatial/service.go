package spatial

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "golang.org/x/image/webp"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

const (
	// defaultDwellWindow merges consecutive detections of the same target on the
	// same camera into one node while the gap stays *strictly* below it (PRD §2.3).
	defaultDwellWindow = 30 * time.Second

	// maxMapNameRunes mirrors the HTTP binding limit so the service cannot persist
	// a name the API would have rejected.
	maxMapNameRunes = 64
)

// allowedMapExtensions lists the accepted base map formats. Animated formats are
// excluded on purpose: their intrinsic size is per-frame and a base map needs one
// stable layout canvas.
var allowedMapExtensions = map[string]struct{}{
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".webp": {},
	".svg":  {},
}

// Service coordinates spatial maps, camera layout calibration, and trajectory generation.
type Service struct {
	store   *Store
	mapsDir string
	logger  *zap.Logger
}

// NewService instantiates a spatial Service.
//
// It only binds dependencies: the maps directory is created by the domain's
// start hook (see SpatialLifecycle), never here.
func NewService(store *Store, mapsDir string, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:   store,
		mapsDir: mapsDir,
		logger:  logger,
	}
}

// ListMaps returns all canvas maps formatted as responses.
func (s *Service) ListMaps(ctx context.Context) ([]CanvasMapResponse, error) {
	maps, err := s.store.ListMaps(ctx)
	if err != nil {
		return nil, databaseError("failed to list canvas maps", err)
	}
	resp := make([]CanvasMapResponse, 0, len(maps))
	for _, m := range maps {
		resp = append(resp, s.formatMapResponse(m))
	}
	return resp, nil
}

// GetMap returns a single canvas map by ID.
func (s *Service) GetMap(ctx context.Context, id string) (*CanvasMapResponse, error) {
	m, err := s.store.GetMap(ctx, id)
	if err != nil {
		return nil, mapLookupError(err)
	}
	res := s.formatMapResponse(*m)
	return &res, nil
}

// GetMapFile resolves the absolute filesystem path and MIME type for serving the map image.
func (s *Service) GetMapFile(ctx context.Context, id string) (string, string, error) {
	m, err := s.store.GetMap(ctx, id)
	if err != nil {
		return "", "", mapLookupError(err)
	}
	fullPath := filepath.Join(s.mapsDir, m.ImagePath)
	if _, err := os.Stat(fullPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", apperr.New(
				apperr.KindNotFound,
				"MAP_FILE_NOT_FOUND",
				"canvas map image file not found",
				err,
			)
		}
		return "", "", apperr.New(
			apperr.KindInternal,
			"MAP_STORAGE_ERROR",
			"failed to read canvas map image",
			err,
		)
	}
	mimeType := mime.TypeByExtension(filepath.Ext(m.ImagePath))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return fullPath, mimeType, nil
}

// CreateMap saves the uploaded map image to disk and creates the map entity.
func (s *Service) CreateMap(
	ctx context.Context,
	name string,
	file io.Reader,
	filename string,
) (*CanvasMapResponse, error) {
	name, err := normalizeMapName(name)
	if err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if _, ok := allowedMapExtensions[ext]; !ok {
		return nil, apperr.New(
			apperr.KindInvalid,
			"UNSUPPORTED_MAP_FORMAT",
			"supported formats: png, jpg, webp, svg",
			nil,
		)
	}

	mapID := idutil.New()
	relPath := mapID + ext
	fullPath := filepath.Join(s.mapsDir, relPath)

	if err := s.writeMapFile(fullPath, file); err != nil {
		return nil, err
	}

	width, height, ok := readImageSize(fullPath, ext)
	if !ok {
		s.removeFile(fullPath, zap.String("map_id", mapID))
		return nil, apperr.New(
			apperr.KindInvalid,
			"IMAGE_SIZE_UNREADABLE",
			"cannot determine the intrinsic size of the uploaded map image",
			nil,
		)
	}

	entity := &CanvasMap{
		ID:        mapID,
		Name:      name,
		ImagePath: relPath,
		Width:     width,
		Height:    height,
	}
	if err := s.store.CreateMap(ctx, entity); err != nil {
		s.removeFile(fullPath, zap.String("map_id", mapID))
		return nil, databaseError("failed to save canvas map", err)
	}

	resp := s.formatMapResponse(*entity)
	return &resp, nil
}

// UpdateMapName updates the display name of a map and returns the refreshed record.
func (s *Service) UpdateMapName(ctx context.Context, id, name string) (*CanvasMapResponse, error) {
	name, err := normalizeMapName(name)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.GetMap(ctx, id); err != nil {
		return nil, mapLookupError(err)
	}
	if err := s.store.UpdateMapName(ctx, id, name); err != nil {
		return nil, databaseError("failed to update map name", err)
	}
	return s.GetMap(ctx, id)
}

// DeleteMap deletes a map and its associated image file on disk.
func (s *Service) DeleteMap(ctx context.Context, id string) error {
	m, err := s.store.GetMap(ctx, id)
	if err != nil {
		return mapLookupError(err)
	}
	if err := s.store.DeleteMap(ctx, id); err != nil {
		return databaseError("failed to delete canvas map", err)
	}
	s.removeFile(filepath.Join(s.mapsDir, m.ImagePath), zap.String("map_id", id))
	return nil
}

// GetPositions retrieves all calibrated camera positions on a map.
func (s *Service) GetPositions(ctx context.Context, mapID string) ([]CameraPositionInfo, error) {
	if _, err := s.store.GetMap(ctx, mapID); err != nil {
		return nil, mapLookupError(err)
	}
	positions, err := s.store.GetPositionsByMap(ctx, mapID)
	if err != nil {
		return nil, databaseError("failed to fetch camera positions", err)
	}
	return positions, nil
}

// SavePositions replaces all camera positions of a map and reports how many were stored.
func (s *Service) SavePositions(ctx context.Context, mapID string, req BatchSavePositionsRequest) (int, error) {
	if _, err := s.store.GetMap(ctx, mapID); err != nil {
		return 0, mapLookupError(err)
	}
	// An absent list is a malformed request; an explicitly empty list is a
	// legitimate "unbind everything on this map" and must not be conflated with it.
	if req.Positions == nil {
		return 0, apperr.New(
			apperr.KindInvalid,
			"POSITIONS_REQUIRED",
			"positions must be provided (send an empty array to unbind every camera)",
			nil,
		)
	}

	entities := make([]CameraCanvasPosition, 0, len(req.Positions))
	seenCameras := make(map[string]struct{}, len(req.Positions))
	cameraIDs := make([]string, 0, len(req.Positions))
	for _, p := range req.Positions {
		if _, seen := seenCameras[p.CameraID]; seen {
			return 0, apperr.New(
				apperr.KindInvalid,
				"DUPLICATE_CAMERA_POSITION",
				fmt.Sprintf("camera %q appears more than once in the batch", p.CameraID),
				nil,
			)
		}
		seenCameras[p.CameraID] = struct{}{}
		cameraIDs = append(cameraIDs, p.CameraID)
		entities = append(entities, CameraCanvasPosition{
			CameraID: p.CameraID,
			MapID:    mapID,
			X:        p.X,
			Y:        p.Y,
			Angle:    p.Angle,
		})
	}

	// A camera deleted between the frontend reading the list and this save would
	// otherwise surface as a raw FK failure (HTTP 500). Name the offender instead.
	missing, err := s.store.FindMissingCameraIDs(ctx, cameraIDs)
	if err != nil {
		return 0, databaseError("failed to validate camera positions", err)
	}
	if len(missing) > 0 {
		return 0, apperr.New(
			apperr.KindInvalid,
			"CAMERA_NOT_FOUND",
			fmt.Sprintf("unknown camera: %q", strings.Join(missing, ", ")),
			nil,
		)
	}

	if err := s.store.BatchSavePositions(ctx, mapID, entities); err != nil {
		return 0, databaseError("failed to save camera positions", err)
	}
	return len(entities), nil
}

// QueryTrajectory retrieves and reconstructs the cross-camera spatio-temporal trajectory.
func (s *Service) QueryTrajectory(ctx context.Context, q TrajectoryQuery) (*TrajectoryResponse, error) {
	window, err := parseTrajectoryWindow(q)
	if err != nil {
		return nil, err
	}

	cameraIDs, posMap, err := s.mapPositions(ctx, q.MapID)
	if err != nil {
		return nil, err
	}

	targetKey, rawPoints, err := s.queryDetections(ctx, q, cameraIDs, window)
	if err != nil {
		return nil, err
	}

	return &TrajectoryResponse{
		MapID:      q.MapID,
		TargetType: q.TargetType,
		TargetKey:  targetKey,
		Nodes:      s.aggregateDetections(rawPoints, posMap),
	}, nil
}

// trajectoryWindow is the validated, UTC-normalised time range of a trajectory query.
type trajectoryWindow struct {
	start time.Time
	end   time.Time
}

// parseTrajectoryWindow normalises the query range to UTC and rejects an inverted
// window before any storage work happens.
func parseTrajectoryWindow(q TrajectoryQuery) (trajectoryWindow, error) {
	window := trajectoryWindow{start: q.StartTime.UTC(), end: q.EndTime.UTC()}
	if !window.end.After(window.start) {
		return trajectoryWindow{}, apperr.New(
			apperr.KindInvalid,
			"INVALID_TIME_RANGE",
			"endTime must be later than startTime",
			nil,
		)
	}
	return window, nil
}

// mapPositions loads a map's calibrated positions, returning them both as the
// coordinate lookup used for node placement and as the camera whitelist every
// detection query is scoped to.
func (s *Service) mapPositions(
	ctx context.Context,
	mapID string,
) ([]string, map[string]CameraPositionInfo, error) {
	if _, err := s.store.GetMap(ctx, mapID); err != nil {
		return nil, nil, mapLookupError(err)
	}

	positions, err := s.store.GetPositionsByMap(ctx, mapID)
	if err != nil {
		return nil, nil, databaseError("failed to get map positions", err)
	}

	posMap := make(map[string]CameraPositionInfo, len(positions))
	cameraIDs := make([]string, 0, len(positions))
	for _, p := range positions {
		posMap[p.CameraID] = p
		cameraIDs = append(cameraIDs, p.CameraID)
	}
	return cameraIDs, posMap, nil
}

// queryDetections dispatches on the target type and returns the raw detections
// together with the normalised target key echoed back to the caller.
func (s *Service) queryDetections(
	ctx context.Context,
	q TrajectoryQuery,
	cameraIDs []string,
	window trajectoryWindow,
) (string, []RawDetectionPoint, error) {
	filter := TrajectoryFilter{
		CameraIDs: cameraIDs,
		Start:     window.start,
		End:       window.end,
	}

	switch q.TargetType {
	case "vehicle":
		filter.MatchKey = strings.TrimSpace(q.Plate)
		if filter.MatchKey == "" {
			return "", nil, apperr.New(
				apperr.KindInvalid,
				"PLATE_REQUIRED",
				"vehicle plate is required",
				nil,
			)
		}
		points, err := s.store.QueryVehicleCaptures(ctx, filter)
		if err != nil {
			return "", nil, databaseError("failed to query vehicle captures", err)
		}
		return filter.MatchKey, points, nil

	case "person":
		filter.MatchKey = strings.TrimSpace(q.PersonID)
		if filter.MatchKey == "" {
			return "", nil, apperr.New(
				apperr.KindInvalid,
				"SPATIAL_PERSON_ID_REQUIRED",
				"person id is required",
				nil,
			)
		}
		points, err := s.store.QueryPersonRecognitions(ctx, filter)
		if err != nil {
			return "", nil, databaseError("failed to query person recognitions", err)
		}
		return filter.MatchKey, points, nil

	default:
		return "", nil, apperr.New(
			apperr.KindInvalid,
			"INVALID_TARGET_TYPE",
			"targetType must be vehicle or person",
			nil,
		)
	}
}

// aggregateDetections performs spatio-temporal dwell window grouping and node creation.
func (s *Service) aggregateDetections(
	rawPoints []RawDetectionPoint,
	posMap map[string]CameraPositionInfo,
) []TrajectoryNodeInfo {
	nodes := make([]TrajectoryNodeInfo, 0, len(rawPoints))

	var current *TrajectoryNodeInfo

	for _, pt := range rawPoints {
		pos, ok := posMap[pt.CameraID]
		if !ok {
			continue // skip cameras not calibrated on this canvas
		}

		capturedAt := utc(pt.CapturedAt)

		// Same camera and within the dwell window: fold the detection into the open node.
		isSameCamera := current != nil && current.CameraID == pt.CameraID
		isWithinDwell := isSameCamera && capturedAt.Sub(current.LastSeenAt) < defaultDwellWindow
		if isWithinDwell {
			current.LastSeenAt = capturedAt
			current.DwellSec = int(current.LastSeenAt.Sub(current.FirstSeenAt).Seconds())
			current.absorb(pt)
			continue
		}

		// Seal the open node and start a new one.
		if current != nil {
			nodes = append(nodes, *current)
		}
		current = newNodeInfo(
			len(nodes)+1,
			pt,
			pos,
			capturedAt,
		)
	}

	if current != nil {
		nodes = append(nodes, *current)
	}
	return nodes
}

// newNodeInfo builds the node that opens a new trajectory segment.
func newNodeInfo(
	seq int,
	pt RawDetectionPoint,
	pos CameraPositionInfo,
	capturedAt time.Time,
) *TrajectoryNodeInfo {
	return &TrajectoryNodeInfo{
		Seq:          seq,
		CameraID:     pt.CameraID,
		CameraName:   pos.CameraName,
		X:            pos.X,
		Y:            pos.Y,
		FirstSeenAt:  capturedAt,
		LastSeenAt:   capturedAt,
		DwellSec:     0,
		SnapshotPath: snapshotOf(pt),
		Score:        pt.Score,
		Details:      pt.Details,
	}
}

// snapshotOf prefers the cropped thumbnail and falls back to the full frame.
func snapshotOf(pt RawDetectionPoint) string {
	if pt.CropPath != "" {
		return pt.CropPath
	}
	return pt.ImagePath
}

// normalizeMapName trims and validates a display name.
//
// The rune limit mirrors the HTTP binding so the service cannot persist a name the
// API would have rejected.
func normalizeMapName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxMapNameRunes {
		return "", apperr.New(
			apperr.KindInvalid,
			"INVALID_MAP_NAME",
			"map name is required and must not exceed 64 characters",
			nil,
		)
	}
	return name, nil
}

func (s *Service) formatMapResponse(m CanvasMap) CanvasMapResponse {
	return CanvasMapResponse{
		ID:        m.ID,
		Name:      m.Name,
		ImageURL:  fmt.Sprintf("/api/v1/spatial/maps/%s/image", m.ID),
		Width:     m.Width,
		Height:    m.Height,
		CreatedAt: utc(m.CreatedAt),
		UpdatedAt: utc(m.UpdatedAt),
	}
}

// mapLookupError separates "this map does not exist" from "the database failed",
// so a storage fault is not reported to the caller as a 404.
func mapLookupError(err error) error {
	if errors.Is(err, ErrMapNotFound) {
		return apperr.New(
			apperr.KindNotFound,
			"MAP_NOT_FOUND",
			"canvas map not found",
			err,
		)
	}
	return databaseError("failed to load canvas map", err)
}

// databaseError wraps a storage fault with the domain's stable code, so every store
// failure reaches the caller as a 500 while the cause stays server-side.
func databaseError(msg string, err error) error {
	return apperr.New(
		apperr.KindInternal,
		"DATABASE_ERROR",
		msg,
		err,
	)
}

// writeMapFile streams the upload to its final path, removing a partial file so a
// failed write never leaves an orphan behind.
func (s *Service) writeMapFile(fullPath string, src io.Reader) error {
	out, err := os.Create(fullPath)
	if err != nil {
		return apperr.New(
			apperr.KindInternal,
			"MAP_STORAGE_ERROR",
			"failed to create canvas map file",
			err,
		)
	}
	if _, err := io.Copy(out, src); err != nil {
		s.closeFile(out, fullPath)
		s.removeFile(fullPath)
		return apperr.New(
			apperr.KindInternal,
			"MAP_STORAGE_ERROR",
			"failed to write canvas map file",
			err,
		)
	}
	if err := out.Close(); err != nil {
		s.removeFile(fullPath)
		return apperr.New(
			apperr.KindInternal,
			"MAP_STORAGE_ERROR",
			"failed to flush canvas map file",
			err,
		)
	}
	return nil
}

func (s *Service) closeFile(f *os.File, path string) {
	if err := f.Close(); err != nil {
		s.logger.Warn("failed to close canvas map file", zap.String("path", path), zap.Error(err))
	}
}

// removeFile deletes a best-effort artefact. Cleanup happens on error paths where
// the caller already reports a more specific failure, so a failed removal is only logged.
func (s *Service) removeFile(path string, fields ...zap.Field) {
	if path == "" {
		return
	}
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return
	}
	s.logger.Warn(
		"failed to remove canvas map image",
		append(fields, zap.String("path", path), zap.Error(err))...,
	)
}

// readImageSize reports the intrinsic pixel size of a base map.
//
// SVG needs its own reader: image.DecodeConfig understands raster formats only,
// which is why SVG maps used to be stored with an invented 1920×1080 size.
func readImageSize(path, ext string) (int, int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}

	width, height, ok := decodeIntrinsicSize(f, ext)
	// 关闭失败按读取失败处理：一个读不全的文件尺寸不可信，不能拿它建底图。
	if closeErr := f.Close(); closeErr != nil {
		return 0, 0, false
	}
	return width, height, ok
}

// decodeIntrinsicSize reads the size of an already-open base map.
func decodeIntrinsicSize(r io.Reader, ext string) (int, int, bool) {
	if ext == ".svg" {
		return svgIntrinsicSize(r)
	}
	cfg, _, err := image.DecodeConfig(r)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// svgIntrinsicSize extracts the root <svg> dimensions, falling back to the
// viewBox aspect box when width/height are absent or expressed as percentages.
//
// Only the root element is considered: an inner <svg> belongs to the artwork, and
// sizing the canvas from it would scale the map to whatever the author nested in it.
func svgIntrinsicSize(r io.Reader) (width, height int, ok bool) {
	decoder := xml.NewDecoder(r)
	for {
		token, err := decoder.Token()
		if err != nil {
			return 0, 0, false
		}
		// Comments, processing instructions and whitespace precede the root element.
		start, isStart := token.(xml.StartElement)
		if !isStart {
			continue
		}
		if start.Name.Local != "svg" {
			return 0, 0, false
		}

		var widthAttr, heightAttr, viewBox string
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "width":
				widthAttr = attr.Value
			case "height":
				heightAttr = attr.Value
			case "viewBox":
				viewBox = attr.Value
			}
		}

		w, widthOK := svgLength(widthAttr)
		h, heightOK := svgLength(heightAttr)
		if widthOK && heightOK {
			return w, h, true
		}
		if vw, vh, viewBoxOK := svgViewBoxSize(viewBox); viewBoxOK {
			return vw, vh, true
		}
		return 0, 0, false
	}
}

// svgLength parses an absolute CSS length. Percentages carry no pixel information,
// so they are reported as unusable and the caller falls back to the viewBox.
func svgLength(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasSuffix(value, "%") {
		return 0, false
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "px"), "PX")
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return int(math.Round(parsed)), true
}

// svgViewBoxSize reads "min-x min-y width height" and returns the box size.
func svgViewBoxSize(value string) (int, int, bool) {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if len(fields) != 4 {
		return 0, 0, false
	}
	w, errW := strconv.ParseFloat(fields[2], 64)
	h, errH := strconv.ParseFloat(fields[3], 64)
	isValidBox := errW == nil && errH == nil && w > 0 && h > 0
	if !isValidBox {
		return 0, 0, false
	}
	return int(math.Round(w)), int(math.Round(h)), true
}
