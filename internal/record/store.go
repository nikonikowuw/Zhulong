package record

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 感知记录的三张表名。保留清理会直接拼进 SQL，因此必须是常量而非外部输入。
const (
	tableRecognitions = "recognitions"
	tableAlarms       = "alarms"
	tableCaptures     = "captures"
)

// batchInsertSize 是单条 INSERT 多值的行数上限。SQLite 的变量上限是 32766，
// 100 行足够摊薄事务开销，又不会让单条语句过长。
const batchInsertSize = 100

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

// normalizeTimestamps 把可空的写入时间补成 UTC：零值表示「现在就写」，
// 非零值必须归一成 UTC，否则 SQLite 里会混进本地时区的时间戳。
// 抽成一个函数是因为三张表 × 单条/批量共六处都走同一规则。
func normalizeTimestamps(capturedAt, createdAt, now time.Time) (time.Time, time.Time) {
	if capturedAt.IsZero() {
		capturedAt = now
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	return capturedAt.UTC(), createdAt.UTC()
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
	rec.CapturedAt, rec.CreatedAt = normalizeTimestamps(rec.CapturedAt, rec.CreatedAt, time.Now().UTC())
	return gorm.G[Recognition](db).Create(ctx, rec)
}

// CreateRecognitionBatch inserts multiple recognition records in a batch.
func (s *Store) CreateRecognitionBatch(ctx context.Context, recs []*Recognition) error {
	valid := make([]*Recognition, 0, len(recs))
	now := time.Now().UTC()
	for _, r := range recs {
		if r == nil {
			continue
		}
		r.CapturedAt, r.CreatedAt = normalizeTimestamps(r.CapturedAt, r.CreatedAt, now)
		valid = append(valid, r)
	}
	return s.createInBatches(ctx, valid)
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
	alarm.CapturedAt, alarm.CreatedAt = normalizeTimestamps(alarm.CapturedAt, alarm.CreatedAt, time.Now().UTC())
	return gorm.G[Alarm](db).Create(ctx, alarm)
}

// CreateAlarmBatch inserts multiple alarm records in a batch.
func (s *Store) CreateAlarmBatch(ctx context.Context, alarms []*Alarm) error {
	valid := make([]*Alarm, 0, len(alarms))
	now := time.Now().UTC()
	for _, a := range alarms {
		if a == nil {
			continue
		}
		a.CapturedAt, a.CreatedAt = normalizeTimestamps(a.CapturedAt, a.CreatedAt, now)
		valid = append(valid, a)
	}
	return s.createInBatches(ctx, valid)
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
	cap.CapturedAt, cap.CreatedAt = normalizeTimestamps(cap.CapturedAt, cap.CreatedAt, time.Now().UTC())
	return gorm.G[Capture](db).Create(ctx, cap)
}

// CreateCaptureBatch inserts multiple capture records in a batch.
func (s *Store) CreateCaptureBatch(ctx context.Context, caps []*Capture) error {
	valid := make([]*Capture, 0, len(caps))
	now := time.Now().UTC()
	for _, c := range caps {
		if c == nil {
			continue
		}
		c.CapturedAt, c.CreatedAt = normalizeTimestamps(c.CapturedAt, c.CreatedAt, now)
		valid = append(valid, c)
	}
	return s.createInBatches(ctx, valid)
}

// createInBatches 是批量写入的唯一入口：空集合直接返回，避免为一个空事务去取连接。
func (s *Store) createInBatches[T any](ctx context.Context, rows []*T) error {
	if len(rows) == 0 {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return gorm.G[*T](db).CreateInBatches(ctx, &rows, batchInsertSize)
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
	types := []string{}
	err = db.Model(&Alarm{}).
		Where("alarm_type != ''").
		Distinct("alarm_type").
		Order("alarm_type ASC").
		Pluck("alarm_type", &types).
		Error
	if err != nil {
		return nil, err
	}
	return types, nil
}

// ListDistinctTargetTypes returns all unique non-empty capture target types sorted alphabetically.
func (s *Store) ListDistinctTargetTypes(ctx context.Context) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	types := []string{}
	err = db.Model(&Capture{}).
		Where("target_type != ''").
		Distinct("target_type").
		Order("target_type ASC").
		Pluck("target_type", &types).
		Error
	if err != nil {
		return nil, err
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

// pendingSnapshotDelete is the durable outbox entry for a snapshot whose media
// files were deleted by the storage cleaner. The row is removed only after the
// corresponding captures/recognitions rows are gone.
type pendingSnapshotDelete struct {
	ID       string    `gorm:"primaryKey;column:id"`
	QueuedAt time.Time `gorm:"column:queued_at;not null;default:CURRENT_TIMESTAMP"`
}

func (pendingSnapshotDelete) TableName() string {
	return "pending_snapshot_deletes"
}

// enqueuePendingSnapshotDeletes records deleted snapshot IDs before the DB
// cascade is attempted. Duplicate IDs are ignored, so retries are idempotent.
func (s *Store) enqueuePendingSnapshotDeletes(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}

	rows := make([]pendingSnapshotDelete, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		rows = append(rows, pendingSnapshotDelete{ID: id})
	}
	if len(rows) == 0 {
		return 0, nil
	}

	res := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, pendingSnapshotDeleteBatchSize)
	if res.Error != nil {
		return 0, res.Error
	}

	return res.RowsAffected, nil
}

func (s *Store) pendingSnapshotDeleteIDs(ctx context.Context, limit int) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var ids []string
	query := db.Model(&pendingSnapshotDelete{}).Order("queued_at ASC, id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Pluck("id", &ids).Error; err != nil {
		return nil, err
	}

	return ids, nil
}

func (s *Store) clearPendingSnapshotDeletes(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}

	res := db.Where("id IN ?", ids).Delete(&pendingSnapshotDelete{})
	if res.Error != nil {
		return 0, res.Error
	}

	return res.RowsAffected, nil
}

// deleteRecordsByIDs removes captures and recognitions rows by primary key.
// The two deletes share one transaction so a failure cannot leave only one
// table cleaned.
func (s *Store) deleteRecordsByIDs(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}

	var total int64
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, table := range []string{tableCaptures, tableRecognitions} {
			stmt := fmt.Sprintf("DELETE FROM %s WHERE id IN ?", table)
			res := tx.Exec(stmt, ids)
			if res.Error != nil {
				return res.Error
			}
			total += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return total, nil
}

// ExistingAlarmIDs reports which IDs belong to alarm records. The storage
// evidence guard uses this to protect historical alarm images that predate the
// .lock sidecar convention.
func (s *Store) ExistingAlarmIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	found := make(map[string]bool)
	if len(ids) == 0 {
		return found, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
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
		return found, nil
	}

	var rows []string
	if err := db.Model(&Alarm{}).Where("id IN ?", unique).Pluck("id", &rows).Error; err != nil {
		return nil, err
	}
	for _, id := range rows {
		found[id] = true
	}

	return found, nil
}

// AlarmEvidencePaths returns every non-empty alarm image/crop path, for the
// one-time lock backfill on upgrade.
func (s *Store) AlarmEvidencePaths(ctx context.Context) ([]string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var rows []Alarm
	if err := db.Model(&Alarm{}).Select("image_path, crop_path").Find(&rows).Error; err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(rows)*2)
	paths := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		for _, path := range []string{row.ImagePath, row.CropPath} {
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}

	return paths, nil
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

// ClearRecordSnapshotPaths clears image_path, crop_path, and face_image_path for a
// record when snapshot generation fails.
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
		// 未知表名曾经静默返回 nil，等于「路径清空成功了」；调用方据此认为
		// 已没有悬空引用。这里必须报错，让误传的表名在日志里暴露。
		return fmt.Errorf("clear snapshot paths: unsupported table %q", table)
	}
}
