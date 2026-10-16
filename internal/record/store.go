package record

import (
	"context"
	"errors"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

// Store provides persistence operations for perception and alarm records.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

func normalizePagination(page, pageSize int) (int, int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize
	return page, pageSize, offset
}

// =========================================================================
// Recognition Operations
// =========================================================================

// CreateRecognition inserts a single recognition record.
func (s *Store) CreateRecognition(ctx context.Context, rec *Recognition) error {
	if rec == nil {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	if rec.CapturedAt.IsZero() {
		rec.CapturedAt = time.Now().UTC()
	} else {
		rec.CapturedAt = rec.CapturedAt.UTC()
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	} else {
		rec.CreatedAt = rec.CreatedAt.UTC()
	}
	return db.Create(rec).Error
}

// CreateRecognitionBatch inserts multiple recognition records in a batch.
func (s *Store) CreateRecognitionBatch(ctx context.Context, recs []*Recognition) error {
	if len(recs) == 0 {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, r := range recs {
		if r.CapturedAt.IsZero() {
			r.CapturedAt = now
		} else {
			r.CapturedAt = r.CapturedAt.UTC()
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		} else {
			r.CreatedAt = r.CreatedAt.UTC()
		}
	}
	return db.CreateInBatches(recs, 100).Error
}

// ListRecognitions returns filtered and paginated recognition records.
func (s *Store) ListRecognitions(ctx context.Context, filter RecognitionFilter) ([]Recognition, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := db.Model(&Recognition{})
	if filter.CameraID != "" {
		query = query.Where("camera_id = ?", filter.CameraID)
	}
	if filter.PersonID != "" {
		query = query.Where("person_id = ?", filter.PersonID)
	}
	if filter.Matched != nil {
		query = query.Where("matched = ?", *filter.Matched)
	}
	if filter.StartTime != nil {
		query = query.Where("captured_at >= ?", filter.StartTime.UTC())
	}
	if filter.EndTime != nil {
		query = query.Where("captured_at <= ?", filter.EndTime.UTC())
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	_, pageSize, offset := normalizePagination(filter.Page, filter.PageSize)
	var items []Recognition
	if err := query.Order("captured_at DESC, id DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = make([]Recognition, 0)
	}
	return items, total, nil
}

// GetRecognitionByID retrieves a recognition by its primary key.
func (s *Store) GetRecognitionByID(ctx context.Context, id string) (*Recognition, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var rec Recognition
	if err := db.Where("id = ?", id).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// =========================================================================
// Alarm Operations
// =========================================================================

// CreateAlarm inserts a single alarm record.
func (s *Store) CreateAlarm(ctx context.Context, alarm *Alarm) error {
	if alarm == nil {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	if alarm.CapturedAt.IsZero() {
		alarm.CapturedAt = time.Now().UTC()
	} else {
		alarm.CapturedAt = alarm.CapturedAt.UTC()
	}
	if alarm.CreatedAt.IsZero() {
		alarm.CreatedAt = time.Now().UTC()
	} else {
		alarm.CreatedAt = alarm.CreatedAt.UTC()
	}
	return db.Create(alarm).Error
}

// CreateAlarmBatch inserts multiple alarm records in a batch.
func (s *Store) CreateAlarmBatch(ctx context.Context, alarms []*Alarm) error {
	if len(alarms) == 0 {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, a := range alarms {
		if a.CapturedAt.IsZero() {
			a.CapturedAt = now
		} else {
			a.CapturedAt = a.CapturedAt.UTC()
		}
		if a.CreatedAt.IsZero() {
			a.CreatedAt = now
		} else {
			a.CreatedAt = a.CreatedAt.UTC()
		}
	}
	return db.CreateInBatches(alarms, 100).Error
}

// ListAlarms returns filtered and paginated alarm records.
func (s *Store) ListAlarms(ctx context.Context, filter AlarmFilter) ([]Alarm, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := db.Model(&Alarm{})
	if filter.CameraID != "" {
		query = query.Where("camera_id = ?", filter.CameraID)
	}
	if filter.AlarmType != "" {
		query = query.Where("alarm_type = ?", filter.AlarmType)
	}
	if filter.StartTime != nil {
		query = query.Where("captured_at >= ?", filter.StartTime.UTC())
	}
	if filter.EndTime != nil {
		query = query.Where("captured_at <= ?", filter.EndTime.UTC())
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	_, pageSize, offset := normalizePagination(filter.Page, filter.PageSize)
	var items []Alarm
	if err := query.Order("captured_at DESC, id DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = make([]Alarm, 0)
	}
	return items, total, nil
}

// GetAlarmByID retrieves an alarm by its primary key.
func (s *Store) GetAlarmByID(ctx context.Context, id string) (*Alarm, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var alarm Alarm
	if err := db.Where("id = ?", id).First(&alarm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &alarm, nil
}

// =========================================================================
// Capture Operations
// =========================================================================

// CreateCapture inserts a single capture record.
func (s *Store) CreateCapture(ctx context.Context, cap *Capture) error {
	if cap == nil {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	if cap.CapturedAt.IsZero() {
		cap.CapturedAt = time.Now().UTC()
	} else {
		cap.CapturedAt = cap.CapturedAt.UTC()
	}
	if cap.CreatedAt.IsZero() {
		cap.CreatedAt = time.Now().UTC()
	} else {
		cap.CreatedAt = cap.CreatedAt.UTC()
	}
	return db.Create(cap).Error
}

// CreateCaptureBatch inserts multiple capture records in a batch.
func (s *Store) CreateCaptureBatch(ctx context.Context, caps []*Capture) error {
	if len(caps) == 0 {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, c := range caps {
		if c.CapturedAt.IsZero() {
			c.CapturedAt = now
		} else {
			c.CapturedAt = c.CapturedAt.UTC()
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		} else {
			c.CreatedAt = c.CreatedAt.UTC()
		}
	}
	return db.CreateInBatches(caps, 100).Error
}

// ListCaptures returns filtered and paginated capture records.
func (s *Store) ListCaptures(ctx context.Context, filter CaptureFilter) ([]Capture, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := db.Model(&Capture{})
	if filter.CameraID != "" {
		query = query.Where("camera_id = ?", filter.CameraID)
	}
	if filter.TargetType != "" {
		query = query.Where("target_type = ?", filter.TargetType)
	}
	if filter.StartTime != nil {
		query = query.Where("captured_at >= ?", filter.StartTime.UTC())
	}
	if filter.EndTime != nil {
		query = query.Where("captured_at <= ?", filter.EndTime.UTC())
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	_, pageSize, offset := normalizePagination(filter.Page, filter.PageSize)
	var items []Capture
	if err := query.Order("captured_at DESC, id DESC").Offset(offset).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = make([]Capture, 0)
	}
	return items, total, nil
}

// GetCaptureByID retrieves a capture by its primary key.
func (s *Store) GetCaptureByID(ctx context.Context, id string) (*Capture, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var cap Capture
	if err := db.Where("id = ?", id).First(&cap).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cap, nil
}

// ListDistinctAlarmTypes returns all unique non-empty alarm types sorted alphabetically.
func (s *Store) ListDistinctAlarmTypes(ctx context.Context) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var types []string
	if err := db.Model(&Alarm{}).Where("alarm_type != ''").Distinct("alarm_type").Order("alarm_type ASC").Pluck("alarm_type", &types).Error; err != nil {
		return nil, err
	}
	if types == nil {
		types = make([]string, 0)
	}
	return types, nil
}

// ListDistinctTargetTypes returns all unique non-empty capture target types sorted alphabetically.
func (s *Store) ListDistinctTargetTypes(ctx context.Context) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var types []string
	if err := db.Model(&Capture{}).Where("target_type != ''").Distinct("target_type").Order("target_type ASC").Pluck("target_type", &types).Error; err != nil {
		return nil, err
	}
	if types == nil {
		types = make([]string, 0)
	}
	return types, nil
}
