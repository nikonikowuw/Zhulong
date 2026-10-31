package record

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"gorm.io/gorm"
)

// 感知记录的三张表名。保留清理会直接拼进 SQL，因此必须是常量而非外部输入。
const (
	tableRecognitions = "recognitions"
	tableAlarms       = "alarms"
	tableCaptures     = "captures"
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

// listInReadSnapshot 在同一个只读事务里执行 count 与分页查询。
//
// WAL 模式下读事务在第一条语句处建立快照，后续语句读同一版本：否则 Count 与
// Find 之间的并发写入会让 total 与当前页不自洽 —— 翻页时表现为重复行或漏行。
// 读事务不阻塞写者，代价只是一次 BEGIN/COMMIT。
//
// window 由调用方（HTTP 层）解析，这里只做零值/越界防御：GORM 的 Limit(0)
// 表示**不加 LIMIT**，一个漏填的 window 会把整表读进内存，在边缘设备上直接 OOM。
func listInReadSnapshot[T any](
	ctx context.Context,
	db *gorm.DB,
	build func(g gorm.ChainInterface[T]) gorm.ChainInterface[T],
	window httputil.PageWindow,
) ([]T, int64, error) {
	if window.PageSize <= 0 || window.PageSize > httputil.MaxPageSize {
		window.PageSize = httputil.DefaultPageSize
	}
	if window.Offset < 0 {
		window.Offset = 0
	}

	var total int64
	var items []T
	err := db.Transaction(func(tx *gorm.DB) error {
		g := build(gorm.G[T](tx).Scopes())
		var err error
		total, err = g.Count(ctx, "*")
		if err != nil {
			return err
		}
		items, err = g.
			Order("captured_at DESC, id DESC").
			Offset(window.Offset).
			Limit(window.PageSize).
			Find(ctx)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []T{}
	}
	return items, total, nil
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
	return gorm.G[Recognition](db).Create(ctx, rec)
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
	valid := make([]*Recognition, 0, len(recs))
	now := time.Now().UTC()
	for _, r := range recs {
		if r == nil {
			continue
		}
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
		valid = append(valid, r)
	}
	if len(valid) == 0 {
		return nil
	}
	return gorm.G[*Recognition](db).CreateInBatches(ctx, &valid, 100)
}

// ListRecognitions returns filtered and paginated recognition records.
func (s *Store) ListRecognitions(ctx context.Context, filter RecognitionFilter) ([]Recognition, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	return listInReadSnapshot(ctx, db, func(g gorm.ChainInterface[Recognition]) gorm.ChainInterface[Recognition] {
		if filter.CameraID != "" {
			g = g.Where("camera_id = ?", filter.CameraID)
		}
		if filter.PersonID != "" {
			g = g.Where("person_id = ?", filter.PersonID)
		}
		if filter.PersonExternalID != "" {
			g = g.Where("person_external_id = ?", filter.PersonExternalID)
		}
		if filter.Matched != nil {
			g = g.Where("matched = ?", *filter.Matched)
		}
		if filter.StartTime != nil {
			g = g.Where("captured_at >= ?", filter.StartTime.UTC())
		}
		if filter.EndTime != nil {
			g = g.Where("captured_at <= ?", filter.EndTime.UTC())
		}
		return g
	}, filter.Window)
}

// GetRecognitionByID retrieves a recognition by its primary key.
func (s *Store) GetRecognitionByID(ctx context.Context, id string) (*Recognition, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := gorm.G[Recognition](db).Where("id = ?", id).First(ctx)
	if err != nil {
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
	return gorm.G[Alarm](db).Create(ctx, alarm)
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
	valid := make([]*Alarm, 0, len(alarms))
	now := time.Now().UTC()
	for _, a := range alarms {
		if a == nil {
			continue
		}
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
		valid = append(valid, a)
	}
	if len(valid) == 0 {
		return nil
	}
	return gorm.G[*Alarm](db).CreateInBatches(ctx, &valid, 100)
}

// ListAlarms returns filtered and paginated alarm records.
func (s *Store) ListAlarms(ctx context.Context, filter AlarmFilter) ([]Alarm, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	return listInReadSnapshot(ctx, db, func(g gorm.ChainInterface[Alarm]) gorm.ChainInterface[Alarm] {
		if filter.CameraID != "" {
			g = g.Where("camera_id = ?", filter.CameraID)
		}
		if filter.AlarmType != "" {
			g = g.Where("alarm_type = ?", filter.AlarmType)
		}
		if filter.StartTime != nil {
			g = g.Where("captured_at >= ?", filter.StartTime.UTC())
		}
		if filter.EndTime != nil {
			g = g.Where("captured_at <= ?", filter.EndTime.UTC())
		}
		return g
	}, filter.Window)
}

// GetAlarmByID retrieves an alarm by its primary key.
func (s *Store) GetAlarmByID(ctx context.Context, id string) (*Alarm, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	alarm, err := gorm.G[Alarm](db).Where("id = ?", id).First(ctx)
	if err != nil {
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
	return gorm.G[Capture](db).Create(ctx, cap)
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
	valid := make([]*Capture, 0, len(caps))
	now := time.Now().UTC()
	for _, c := range caps {
		if c == nil {
			continue
		}
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
		valid = append(valid, c)
	}
	if len(valid) == 0 {
		return nil
	}
	return gorm.G[*Capture](db).CreateInBatches(ctx, &valid, 100)
}

// ListCaptures returns filtered and paginated capture records.
func (s *Store) ListCaptures(ctx context.Context, filter CaptureFilter) ([]Capture, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	return listInReadSnapshot(ctx, db, func(g gorm.ChainInterface[Capture]) gorm.ChainInterface[Capture] {
		if filter.CameraID != "" {
			g = g.Where("camera_id = ?", filter.CameraID)
		}
		if filter.TargetType != "" {
			g = g.Where("target_type = ?", filter.TargetType)
		}
		if filter.StartTime != nil {
			g = g.Where("captured_at >= ?", filter.StartTime.UTC())
		}
		if filter.EndTime != nil {
			g = g.Where("captured_at <= ?", filter.EndTime.UTC())
		}
		return g
	}, filter.Window)
}

// GetCaptureByID retrieves a capture by its primary key.
func (s *Store) GetCaptureByID(ctx context.Context, id string) (*Capture, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	cap, err := gorm.G[Capture](db).Where("id = ?", id).First(ctx)
	if err != nil {
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

type retentionCursor struct {
	CapturedAt time.Time
	ID         string
}

type retentionCandidate struct {
	ID            string
	CapturedAt    time.Time
	ImagePath     string
	SecondaryPath string
}

// retentionCandidatesBatch returns expired non-alarm rows in stable cursor order.
func (s *Store) retentionCandidatesBatch(
	ctx context.Context,
	table string,
	cutoff time.Time,
	after *retentionCursor,
	limit int,
) ([]retentionCandidate, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	query := db.Table(table)
	switch table {
	case tableRecognitions:
		query = query.Select("id, captured_at, image_path, face_image_path AS secondary_path")
	case tableCaptures:
		query = query.Select("id, captured_at, image_path, crop_path AS secondary_path")
	default:
		return nil, fmt.Errorf("unsupported retention table %q", table)
	}

	query = query.Where("captured_at < ?", cutoff.UTC())
	if after != nil {
		query = query.Where(
			"((captured_at > ?) OR (captured_at = ? AND id > ?))",
			after.CapturedAt.UTC(), after.CapturedAt.UTC(), after.ID,
		)
	}

	var rows []retentionCandidate
	if err := query.Order("captured_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) deleteRetentionRows(ctx context.Context, table string, cutoff time.Time, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	if table != tableRecognitions && table != tableCaptures {
		return 0, fmt.Errorf("unsupported retention table %q", table)
	}

	stmt := fmt.Sprintf("DELETE FROM %s WHERE captured_at < ? AND id IN ?", table)
	res := db.Exec(stmt, cutoff.UTC(), ids)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// GalleryFaceRef is the local identity behind one enrolled face, resolved from the
// person tables. A zero value means the face is not (or no longer) in the local gallery.
type GalleryFaceRef struct {
	ImagePath        string
	PersonRowID      string
	PersonExternalID string
	PersonName       string
}

// ResolveGalleryFaces maps runner-reported face ids onto their owning person.
//
// The lookup is keyed by matched_face_id rather than by the person_id the runner
// reports. That is deliberate: the IPC payload carries the *external business key*
// while every recognitions column carries the *internal row id*, so mapping through
// the payload would either write the wrong value into the foreign key or silently
// lose records whenever a runner holds a stale gallery. matched_face_id is a primary
// key of this database, so one JOIN answers both questions at once.
//
// Face ids with no local row (deleted between inference and ingest) are absent from
// the returned map instead of being an error: the caller degrades to an unattributed
// record, which is still evidence worth keeping.
func (s *Store) ResolveGalleryFaces(ctx context.Context, faceIDs []string) (map[string]GalleryFaceRef, error) {
	refs := make(map[string]GalleryFaceRef, len(faceIDs))
	if len(faceIDs) == 0 {
		return refs, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	unique := make([]string, 0, len(faceIDs))
	seen := make(map[string]struct{}, len(faceIDs))
	for _, id := range faceIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return refs, nil
	}

	type row struct {
		FaceID           string
		ImagePath        string
		PersonRowID      string
		PersonExternalID *string
		PersonName       string
	}
	var rows []row
	if err := db.Table("person_faces AS f").
		Select("f.id AS face_id, f.image_path, f.person_id AS person_row_id, "+
			"p.person_id AS person_external_id, p.name AS person_name").
		Joins("JOIN persons AS p ON p.id = f.person_id").
		Where("f.id IN ?", unique).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("resolve gallery faces: %w", err)
	}

	for _, r := range rows {
		ref := GalleryFaceRef{
			ImagePath:   r.ImagePath,
			PersonRowID: r.PersonRowID,
			PersonName:  r.PersonName,
		}
		if r.PersonExternalID != nil {
			ref.PersonExternalID = *r.PersonExternalID
		}
		refs[r.FaceID] = ref
	}
	return refs, nil
}

// BackfillPersonExternalID fills in the business-key snapshot of recognitions that
// were captured before their person had one.
//
// Only empty snapshots are written: a non-empty value records the identity as it was
// at capture time, and the person domain makes the business key immutable once set,
// so rewriting it is never legitimate. Returns the number of rows touched.
func (s *Store) BackfillPersonExternalID(ctx context.Context, personRowID, externalID string) (int64, error) {
	if personRowID == "" || externalID == "" {
		return 0, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := gorm.G[Recognition](db).
		Where("person_id = ? AND person_external_id = ?", personRowID, "").
		Update(ctx, "person_external_id", externalID)
	if err != nil {
		return 0, fmt.Errorf("backfill recognition person_external_id: %w", err)
	}
	return int64(rows), nil
}

// ClearRecordSnapshotPaths clears image_path, crop_path, and face_image_path for a record when snapshot generation fails.
func (s *Store) ClearRecordSnapshotPaths(ctx context.Context, table, id string) error {
	if id == "" {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	switch table {
	case tableCaptures:
		return db.Model(&Capture{}).Where("id = ?", id).Updates(map[string]any{
			"image_path": "",
			"crop_path":  "",
		}).Error
	case tableAlarms:
		return db.Model(&Alarm{}).Where("id = ?", id).Updates(map[string]any{
			"image_path": "",
			"crop_path":  "",
		}).Error
	case tableRecognitions:
		return db.Model(&Recognition{}).Where("id = ?", id).Updates(map[string]any{
			"image_path":      "",
			"face_image_path": "",
		}).Error
	default:
		return nil
	}
}
