package spatial

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

// ErrMapNotFound reports an absent canvas map as a domain condition, so the
// service does not have to import the ORM just to classify an error.
var ErrMapNotFound = errors.New("spatial: canvas map not found")

// RawDetectionPoint represents one perception event retrieved from captures or recognitions.
type RawDetectionPoint struct {
	CapturedAt time.Time `gorm:"column:captured_at"`
	CameraID   string    `gorm:"column:camera_id"`
	CropPath   string    `gorm:"column:crop_path"`
	ImagePath  string    `gorm:"column:image_path"`
	Details    string    `gorm:"column:details"`
	Score      float64   `gorm:"column:score"`
}

// Store encapsulates SQLite database operations for spatial maps and positions.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) *gorm.DB {
	return s.dbProvider.DB().WithContext(ctx)
}

// ListMaps returns all canvas maps ordered by creation time ascending.
func (s *Store) ListMaps(ctx context.Context) ([]CanvasMap, error) {
	maps, err := gorm.G[CanvasMap](s.db(ctx)).Order("created_at ASC, id ASC").Find(ctx)
	if err != nil {
		return nil, err
	}
	if maps == nil {
		maps = []CanvasMap{}
	}
	return maps, nil
}

// GetMap returns a canvas map by ID. A missing row is reported as ErrMapNotFound.
func (s *Store) GetMap(ctx context.Context, id string) (*CanvasMap, error) {
	m, err := gorm.G[CanvasMap](s.db(ctx)).Where("id = ?", id).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMapNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// FindMissingCameraIDs returns the subset of cameraIDs that no camera row matches.
func (s *Store) FindMissingCameraIDs(ctx context.Context, cameraIDs []string) ([]string, error) {
	if len(cameraIDs) == 0 {
		return []string{}, nil
	}
	found := []string{}
	if err := s.db(ctx).Table("cameras").Where("id IN (?)", cameraIDs).Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	existing := make(map[string]struct{}, len(found))
	for _, id := range found {
		existing[id] = struct{}{}
	}
	missing := make([]string, 0, len(cameraIDs))
	for _, id := range cameraIDs {
		if _, ok := existing[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// CreateMap inserts a new canvas map record.
func (s *Store) CreateMap(ctx context.Context, m *CanvasMap) error {
	return gorm.G[CanvasMap](s.db(ctx)).Create(ctx, m)
}

// UpdateMapName updates the name of an existing canvas map.
func (s *Store) UpdateMapName(ctx context.Context, id, name string) error {
	_, err := gorm.G[CanvasMap](s.db(ctx)).Where("id = ?", id).Update(ctx, "name", name)
	return err
}

// DeleteMap deletes a canvas map. Database cascades will clean up camera_canvas_positions.
func (s *Store) DeleteMap(ctx context.Context, id string) error {
	_, err := gorm.G[CanvasMap](s.db(ctx)).Where("id = ?", id).Delete(ctx)
	return err
}

// GetPositionsByMap returns all calibrated camera positions for a map with joined camera names.
//
// Order is by camera name, not created_at: BatchSavePositions replaces the whole
// set on every save, so created_at is identical for all rows of a map and cannot
// express a stable order.
func (s *Store) GetPositionsByMap(ctx context.Context, mapID string) ([]CameraPositionInfo, error) {
	positions := []CameraPositionInfo{}
	err := s.db(ctx).Table("camera_canvas_positions AS p").
		Select("p.camera_id, COALESCE(c.name, '') AS camera_name, p.x, p.y, p.angle").
		Joins("LEFT JOIN cameras AS c ON c.id = p.camera_id").
		Where("p.map_id = ?", mapID).
		Order("COALESCE(c.name, '') ASC, p.camera_id ASC").
		Scan(&positions).Error
	return positions, err
}

// BatchSavePositions replaces all camera positions for a given map in a single transaction.
func (s *Store) BatchSavePositions(ctx context.Context, mapID string, positions []CameraCanvasPosition) error {
	return s.db(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := gorm.G[CameraCanvasPosition](tx).Where("map_id = ?", mapID).Delete(ctx); err != nil {
			return err
		}
		if len(positions) == 0 {
			return nil
		}
		return gorm.G[CameraCanvasPosition](tx).CreateInBatches(ctx, &positions, len(positions))
	})
}

// TrajectoryFilter groups the constraints both trajectory queries share, so no
// caller can forget the empty-camera guard or the inclusive time window.
type TrajectoryFilter struct {
	// CameraIDs scopes the query to the cameras calibrated on the map.
	CameraIDs []string
	// MatchKey is the plate substring for vehicles and the person ID for persons.
	MatchKey string
	Start    time.Time
	End      time.Time
}

// escapeLike neutralises the LIKE metacharacters of a user-supplied substring.
//
// Without it a plate of "%" matches every vehicle capture, and "_" silently turns
// into a single-character wildcard. The query must pair this with ESCAPE '\'.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// QueryVehicleCaptures retrieves vehicle captures for the filtered cameras and
// time range whose plate contains the filter's match key.
func (s *Store) QueryVehicleCaptures(ctx context.Context, f TrajectoryFilter) ([]RawDetectionPoint, error) {
	if len(f.CameraIDs) == 0 {
		return []RawDetectionPoint{}, nil
	}
	pattern := "%" + escapeLike(f.MatchKey) + "%"
	points := []RawDetectionPoint{}
	err := s.db(ctx).Table("captures").
		Select(
			"camera_id, score, crop_path, image_path, "+
				"COALESCE(json_extract(attributes_json, '$.plate_number'), '') AS details, captured_at",
		).
		Where("target_type = 'vehicle'").
		Where("camera_id IN (?)", f.CameraIDs).
		Where("captured_at >= ? AND captured_at <= ?", f.Start, f.End).
		Where(`json_extract(attributes_json, '$.plate_number') LIKE ? ESCAPE '\'`, pattern).
		Order("captured_at ASC").
		Scan(&points).Error
	return points, err
}

// QueryPersonRecognitions retrieves recognition events for the filtered cameras
// and time range matching the filter's match key (internal ID or external business
// key).
//
// matched = 1 is part of the contract: a recognition row carries person_id only
// after a gallery hit, but an unmatched row must never contribute a trajectory
// node even if it somehow inherited an ID.
func (s *Store) QueryPersonRecognitions(ctx context.Context, f TrajectoryFilter) ([]RawDetectionPoint, error) {
	if len(f.CameraIDs) == 0 {
		return []RawDetectionPoint{}, nil
	}
	points := []RawDetectionPoint{}
	err := s.db(ctx).Table("recognitions").
		Select(
			"camera_id, similarity AS score, face_image_path AS crop_path, "+
				"image_path, person_name AS details, captured_at",
		).
		Where("camera_id IN (?)", f.CameraIDs).
		Where("captured_at >= ? AND captured_at <= ?", f.Start, f.End).
		Where("(person_id = ? OR person_external_id = ?)", f.MatchKey, f.MatchKey).
		Where("matched = ?", true).
		Order("captured_at ASC").
		Scan(&points).Error
	return points, err
}
