package recording

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

const (
	// DefaultPollInterval is how often native completion events are drained.
	DefaultPollInterval = 500 * time.Millisecond
	// DefaultCleanupInterval is how often clip rows are reconciled with files.
	DefaultCleanupInterval = 10 * time.Minute
	// DefaultCleanupInitialDelay delays the first cleanup round after Start.
	DefaultCleanupInitialDelay = time.Minute

	pollBatch           = 64
	defaultCleanupBatch = 500
	recordingsDir       = "recordings"
	clipFilePrefix      = "evt-"
	clipFileExt         = ".mp4"
	partSuffix          = ".part"
	lockSuffix          = ".lock"
)

// Options tunes the Service. Zero values use the defaults.
type Options struct {
	PollInterval        time.Duration
	CleanupInterval     time.Duration
	CleanupInitialDelay time.Duration
	// MaxClipMs caps one clip's length; <=0 uses DefaultMaxClipMs.
	MaxClipMs int64
	// Now overrides the clock (tests).
	Now func() time.Time
}

// activeClip tracks a clip that native is still writing, keyed by native seq.
type activeClip struct {
	clipID   string
	cameraID string
	absPath  string
}

// Service triggers event clips and keeps event_clips consistent with native state.
//
// Pure constructor; goroutines start in Start and stop in Stop.
type Service struct {
	store  *Store
	hub    ClipHub
	media  MediaRootProvider
	gate   WriteGate
	logger *zap.Logger

	pollInterval    time.Duration
	cleanupInterval time.Duration
	cleanupDelay    time.Duration
	maxClipMs       int64
	now             func() time.Time

	depsMu      sync.RWMutex
	retention   RetentionDaysProvider
	fileChecker ClipFileChecker

	// triggerMu serializes trigger → insert → map registration so that an extension
	// racing a creation on the same camera always finds the clip row and map entry.
	triggerMu sync.Mutex
	// activeMu guards active; held only briefly (never across cgo or DB calls).
	activeMu sync.Mutex
	active   map[uint64]activeClip
	// pollMu serializes poll rounds (loop vs. Stop's final poll).
	pollMu sync.Mutex

	lifeMu  sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
}

// NewService constructs the recording service. hub / media / gate may be nil:
// a nil hub or media root makes Trigger return ErrClipUnavailable; a nil gate means always writable.
func NewService(
	store *Store,
	hub ClipHub,
	media MediaRootProvider,
	gate WriteGate,
	logger *zap.Logger,
	opts Options,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	s := &Service{
		store:           store,
		hub:             hub,
		media:           media,
		gate:            gate,
		logger:          logger.Named("recording"),
		pollInterval:    DefaultPollInterval,
		cleanupInterval: DefaultCleanupInterval,
		cleanupDelay:    DefaultCleanupInitialDelay,
		maxClipMs:       DefaultMaxClipMs,
		now:             func() time.Time { return time.Now().UTC() },
		active:          map[uint64]activeClip{},
	}
	if opts.PollInterval > 0 {
		s.pollInterval = opts.PollInterval
	}
	if opts.CleanupInterval > 0 {
		s.cleanupInterval = opts.CleanupInterval
	}
	if opts.CleanupInitialDelay > 0 {
		s.cleanupDelay = opts.CleanupInitialDelay
	}
	if opts.MaxClipMs > 0 {
		s.maxClipMs = opts.MaxClipMs
	}
	if opts.Now != nil {
		s.now = func() time.Time { return opts.Now().UTC() }
	}
	return s
}

// SetRetentionDaysProvider injects RecordingsRetentionDays (used to age out failed rows).
// Unset ⇒ failed rows are never deleted automatically.
func (s *Service) SetRetentionDaysProvider(p RetentionDaysProvider) {
	s.depsMu.Lock()
	s.retention = p
	s.depsMu.Unlock()
}

func (s *Service) retentionProvider() RetentionDaysProvider {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.retention
}

// SetFileChecker injects a fallthrough-safe media file checker (backed by mediafile.Service).
func (s *Service) SetFileChecker(c ClipFileChecker) {
	s.depsMu.Lock()
	s.fileChecker = c
	s.depsMu.Unlock()
}

func (s *Service) clipFileChecker() ClipFileChecker {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.fileChecker
}

// ActiveClips returns how many clips are tracked as still recording in this process.
func (s *Service) ActiveClips() int {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	return len(s.active)
}

// =========================================================================
// Trigger
// =========================================================================

// Trigger starts a new clip or extends the camera's active clip, then attaches it to the
// event row. Alarm clips get a ".lock" sidecar so storage never evicts them.
//
// Errors never imply the event row is affected beyond clip_id staying NULL.
func (s *Service) Trigger(ctx context.Context, req TriggerRequest) (clipID string, offsetMs int64, err error) {
	if err := validateTrigger(req); err != nil {
		return "", 0, err
	}
	if s.hub == nil || s.media == nil || s.store == nil {
		return "", 0, ErrClipUnavailable
	}
	if s.gate != nil && !s.gate.CanWrite() {
		return "", 0, ErrClipSkipped
	}

	root, err := s.media.MediaRoot(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("resolve media root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return "", 0, ErrClipUnavailable
	}

	capturedAt := req.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = s.now()
	}
	newID := idutil.New()
	relPath := ClipRelPath(req.CameraID, capturedAt, newID)
	// Native 的 clip 触发器只接受绝对路径；storage 的 mediaDirectory 允许相对
	// 路径（默认 data/media），这里必须相对进程工作目录解析后再下发。
	absPath, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return "", 0, fmt.Errorf("resolve clip path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o750); err != nil {
		return "", 0, fmt.Errorf("create clip directory: %w", err)
	}

	var ptsUS int64
	if req.PTSUS != nil {
		ptsUS = *req.PTSUS
	}

	clip, offsetMs, err := s.triggerAndRegister(ctx, req, newID, relPath, absPath, ptsUS)
	if err != nil {
		return "", 0, err
	}

	if err := s.store.AttachClip(ctx, req.EventKind, req.EventID, clip.clipID, offsetMs); err != nil {
		return "", 0, err
	}
	if req.EventKind == EventKindAlarm {
		if err := ensureLock(clip.absPath); err != nil {
			// 关联已经写入：锁失败只降级为告警日志，返回 clipID 让调用方知道关联存在。
			s.logger.Warn("write event clip lock failed",
				zap.String("clip_id", clip.clipID), zap.String("path", clip.absPath), zap.Error(err))
		}
	}
	return clip.clipID, offsetMs, nil
}

func (s *Service) triggerAndRegister(
	ctx context.Context,
	req TriggerRequest,
	newID, relPath, absPath string,
	ptsUS int64,
) (activeClip, int64, error) {
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()

	res, err := s.hub.TriggerClip(ctx, req.CameraID, ClipTriggerRequest{
		EventPTSUS: ptsUS,
		PostMs:     int64(req.Policy.PostSeconds) * 1000,
		MaxMs:      s.maxClipMs,
		Path:       absPath,
	})
	if err != nil {
		return activeClip{}, 0, fmt.Errorf("trigger clip: %w", err)
	}
	offsetMs := max(res.OffsetMs, 0)

	if !res.Created {
		s.activeMu.Lock()
		clip, ok := s.active[res.Seq]
		s.activeMu.Unlock()
		if !ok {
			return activeClip{}, 0, fmt.Errorf("%w (seq=%d camera=%s)", ErrClipStateLost, res.Seq, req.CameraID)
		}
		return clip, offsetMs, nil
	}

	row := &EventClip{
		ID:        newID,
		CameraID:  req.CameraID,
		Path:      relPath,
		Codec:     res.Codec,
		Status:    ClipStatusRecording,
		StartedAt: s.now(),
	}
	if err := s.store.CreateClip(ctx, row); err != nil {
		// native 仍在写这个文件：无行引用的孤儿文件交给 storage 的 recordings TTL 清理。
		return activeClip{}, 0, fmt.Errorf("insert event clip: %w", err)
	}
	clip := activeClip{clipID: newID, cameraID: req.CameraID, absPath: absPath}
	s.activeMu.Lock()
	s.active[res.Seq] = clip
	s.activeMu.Unlock()
	return clip, offsetMs, nil
}

func validateTrigger(req TriggerRequest) error {
	if !validPathSegment(req.CameraID) {
		return fmt.Errorf("%w: invalid camera id %q", ErrInvalidTrigger, req.CameraID)
	}
	if req.EventID == "" {
		return fmt.Errorf("%w: empty event id", ErrInvalidTrigger)
	}
	if _, err := eventTable(req.EventKind); err != nil {
		return err
	}
	if req.Policy.PostSeconds <= 0 {
		return fmt.Errorf("%w: post seconds must be positive", ErrInvalidTrigger)
	}
	return nil
}

// validPathSegment rejects camera IDs that would escape recordings/{camera}/.
func validPathSegment(seg string) bool {
	if seg == "" || seg == "." || seg == ".." {
		return false
	}
	return !strings.ContainsAny(seg, "/\\\x00")
}

// ClipRelPath returns recordings/{camera}/{YYYY-MM-DD UTC}/evt-{clipID}.mp4.
//
// ⚠️ The file name must not contain '_': storage groups media by the prefix before
// the first '_', so "evt_{id}" would merge every clip of a day into one cleanup unit.
func ClipRelPath(cameraID string, capturedAt time.Time, clipID string) string {
	return path.Join(
		recordingsDir,
		cameraID,
		capturedAt.UTC().Format("2006-01-02"),
		clipFilePrefix+clipID+clipFileExt,
	)
}

// ensureLock writes "<path>.lock" if missing (idempotent).
func ensureLock(absPath string) error {
	marker := absPath + lockSuffix
	if _, err := os.Lstat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	return f.Close()
}

// =========================================================================
// Lifecycle
// =========================================================================

// Start repairs leftover recording rows and launches the poll and cleanup loops.
//
// The loops run on a context detached from ctx's cancellation (ctx may be a bounded
// start-up context); Stop ends them. Repair failures are logged, not fatal.
func (s *Service) Start(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.started {
		return nil
	}

	if err := s.RepairLeftovers(ctx); err != nil {
		s.logger.Warn("repair leftover event clips failed", zap.Error(err))
	}

	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	s.started = true
	if s.hub != nil {
		s.wg.Add(1)
		go s.pollLoop(loopCtx)
	}
	s.wg.Add(1)
	go s.cleanupLoop(loopCtx)
	return nil
}

// Stop ends the loops, then drains completion events one final time so clips flushed
// by native during camera shutdown get their terminal state. Leftovers are repaired on
// the next Start.
func (s *Service) Stop(ctx context.Context) error {
	s.lifeMu.Lock()
	cancel := s.cancel
	wasStarted := s.started
	s.cancel = nil
	s.started = false
	s.lifeMu.Unlock()
	if !wasStarted {
		return nil
	}
	if cancel != nil {
		cancel()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("stop recording loops: %w", ctx.Err())
	}

	if s.hub != nil {
		if err := s.PollOnce(ctx); err != nil {
			s.logger.Warn("final event clip poll failed", zap.Error(err))
		}
	}
	return nil
}

func (s *Service) pollLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.PollOnce(ctx); err != nil && ctx.Err() == nil {
				s.logger.Debug("poll event clips failed", zap.Error(err))
			}
		}
	}
}

func (s *Service) cleanupLoop(ctx context.Context) {
	defer s.wg.Done()
	timer := time.NewTimer(s.cleanupDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("event clip cleanup failed", zap.Error(err))
			}
			timer.Reset(s.cleanupInterval)
		}
	}
}

// =========================================================================
// Poll
// =========================================================================

// PollOnce drains native completion events and writes terminal clip states.
func (s *Service) PollOnce(ctx context.Context) error {
	if s.hub == nil || s.store == nil {
		return nil
	}
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	for {
		events, err := s.hub.PollClipEvents(ctx, pollBatch)
		if err != nil {
			return fmt.Errorf("poll clip events: %w", err)
		}
		for _, ev := range events {
			s.applyEvent(ctx, ev)
		}
		if len(events) < pollBatch {
			return nil
		}
	}
}

func (s *Service) applyEvent(ctx context.Context, ev ClipEvent) {
	// 等在途的 Trigger 完成登记：失败可能在 native 侧立即发生，事件先于 map 登记出队。
	s.triggerMu.Lock()
	s.activeMu.Lock()
	clip, ok := s.active[ev.Seq]
	delete(s.active, ev.Seq)
	s.activeMu.Unlock()
	s.triggerMu.Unlock()
	if !ok {
		s.logger.Debug("drop completion event for untracked clip", zap.Uint64("seq", ev.Seq))
		return
	}

	status := ev.Status
	switch status {
	case ClipStatusCompleted, ClipStatusTruncated, ClipStatusFailed:
	default:
		s.logger.Warn("unknown clip status from native; treating as failed",
			zap.String("clip_id", clip.clipID), zap.String("status", string(status)))
		status = ClipStatusFailed
	}

	size := ev.SizeBytes
	if status != ClipStatusFailed && size <= 0 {
		if info, err := os.Stat(clip.absPath); err == nil {
			size = info.Size()
		}
	}
	if status == ClipStatusFailed {
		s.removeOrphanLock(clip.absPath)
	}
	// Stop() 可能在 pollLoop 刚出队后取消 loopCtx；用独立有界上下文完成落库，避免丢失已完成事件。
	dbCtx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer dbCancel()
	updated, err := s.store.FinishClip(dbCtx, clip.clipID, ClipResult{
		Status:     status,
		Codec:      ev.Codec,
		DurationMS: max(ev.DurationMs, 0),
		SizeBytes:  max(size, 0),
		EndedAt:    s.now(),
	})
	if err != nil {
		s.logger.Warn("persist clip completion failed",
			zap.String("clip_id", clip.clipID), zap.Error(err))
		return
	}
	if !updated {
		s.logger.Debug("clip completion matched no recording row", zap.String("clip_id", clip.clipID))
	}
}

// =========================================================================
// Start-up repair
// =========================================================================

// RepairLeftovers fixes rows left in "recording" by a crash or unclean stop:
// file exists (non-empty) ⇒ completed+partial; only ".part" exists ⇒ rename, then
// completed+partial; neither ⇒ failed.
func (s *Service) RepairLeftovers(ctx context.Context) error {
	rows, err := s.store.RecordingClips(ctx)
	if err != nil {
		return fmt.Errorf("list recording clips: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	if s.media == nil {
		return fmt.Errorf("repair %d clips: %w", len(rows), ErrClipUnavailable)
	}
	root, err := s.media.MediaRoot(ctx)
	if err != nil {
		return fmt.Errorf("resolve media root: %w", err)
	}
	if checker := s.clipFileChecker(); checker != nil {
		// 挂载点未就绪时（fallthrough）不得把残留 recording 行全部误判为失败。
		if _, checkErr := checker.ClipFileExists(ctx, rows[0].Path); checkErr != nil {
			return fmt.Errorf("media root unavailable for repair: %w", checkErr)
		}
	}

	var errs []error
	for i := range rows {
		if err := s.repairClip(ctx, root, &rows[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) repairClip(ctx context.Context, root string, row *EventClip) error {
	abs := filepath.Join(root, filepath.FromSlash(row.Path))
	result := ClipResult{Codec: row.Codec, EndedAt: s.now()}

	size, err := nonEmptyFileSize(abs)
	if err != nil {
		return fmt.Errorf("stat clip %q: %w", row.ID, err)
	}
	if size <= 0 {
		partSize, err := nonEmptyFileSize(abs + partSuffix)
		if err != nil {
			return fmt.Errorf("stat clip part %q: %w", row.ID, err)
		}
		if partSize > 0 {
			if err := os.Rename(abs+partSuffix, abs); err != nil {
				return fmt.Errorf("rename clip part %q: %w", row.ID, err)
			}
			size = partSize
		}
	}

	if size > 0 {
		result.Status = ClipStatusCompleted
		result.Partial = true
		result.SizeBytes = size
	} else {
		result.Status = ClipStatusFailed
		// 空的 .part / 空文件没有任何可播放内容，顺手删掉避免留下 0 字节垃圾。
		s.removeIfEmpty(abs)
		s.removeIfEmpty(abs + partSuffix)
		s.removeOrphanLock(abs)
	}
	if _, err := s.store.FinishClip(ctx, row.ID, result); err != nil {
		return err
	}
	s.logger.Info("repaired leftover event clip",
		zap.String("clip_id", row.ID), zap.String("status", string(result.Status)), zap.Int64("size", size))
	return nil
}

// removeOrphanLock removes "<absPath>.lock" if the clip mp4 itself does not exist,
// preventing failed alarm clips from leaking unreferenced lock markers forever.
func (s *Service) removeOrphanLock(absPath string) {
	if _, err := os.Stat(absPath); err == nil || !os.IsNotExist(err) {
		return
	}
	if err := os.Remove(absPath + lockSuffix); err != nil && !os.IsNotExist(err) {
		s.logger.Debug("remove orphan clip lock failed", zap.String("path", absPath), zap.Error(err))
	}
}

// nonEmptyFileSize returns the size of a regular file, 0 when it does not exist.
func nonEmptyFileSize(p string) (int64, error) {
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, nil
	}
	return info.Size(), nil
}

// removeIfEmpty best-effort removes a zero-byte leftover file.
func (s *Service) removeIfEmpty(p string) {
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		return
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		s.logger.Debug("remove empty clip leftover failed", zap.String("path", p), zap.Error(err))
	}
}

// =========================================================================
// Cleanup
// =========================================================================

// CleanupOnce deletes completed/truncated rows whose file storage has removed, and
// failed rows older than RecordingsRetentionDays. A stat error other than "not exist"
// aborts the round (never treat uncertainty as deletion).
func (s *Service) CleanupOnce(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	var errs []error
	if err := s.pruneMissingFiles(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.pruneFailed(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) pruneMissingFiles(ctx context.Context) error {
	if s.media == nil {
		return nil
	}
	root, err := s.media.MediaRoot(ctx)
	if err != nil {
		return fmt.Errorf("resolve media root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if _, err := os.Stat(root); err != nil {
		// 媒体盘未挂载时所有文件都「不存在」：绝不能据此清空整张表。
		return fmt.Errorf("media root unavailable: %w", err)
	}

	cursor := ""
	var deleted int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := s.store.FinishedClipsAfter(ctx, cursor, defaultCleanupBatch)
		if err != nil {
			return fmt.Errorf("list finished clips: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		var missing []string
		checker := s.clipFileChecker()
		for i := range rows {
			if checker != nil {
				exists, checkErr := checker.ClipFileExists(ctx, rows[i].Path)
				if checkErr != nil {
					return fmt.Errorf("check clip %q: %w", rows[i].ID, checkErr)
				}
				if !exists {
					missing = append(missing, rows[i].ID)
				}
				continue
			}
			abs := filepath.Join(root, filepath.FromSlash(rows[i].Path))
			_, statErr := os.Stat(abs)
			switch {
			case statErr == nil:
			case os.IsNotExist(statErr):
				missing = append(missing, rows[i].ID)
			default:
				return fmt.Errorf("stat clip %q: %w", rows[i].ID, statErr)
			}
		}
		n, err := s.store.DeleteClips(ctx, missing)
		if err != nil {
			return fmt.Errorf("delete missing clips: %w", err)
		}
		deleted += n
		cursor = rows[len(rows)-1].ID
		if len(rows) < defaultCleanupBatch {
			break
		}
	}
	if deleted > 0 {
		s.logger.Info("deleted event clip rows whose files were removed", zap.Int64("rows", deleted))
	}
	return nil
}

func (s *Service) pruneFailed(ctx context.Context) error {
	provider := s.retentionProvider()
	if provider == nil {
		return nil
	}
	days, err := provider.RecordingRetentionDays(ctx)
	if err != nil {
		return fmt.Errorf("read recordings retention: %w", err)
	}
	if days <= 0 {
		return nil
	}
	cutoff := s.now().Add(-time.Duration(days) * 24 * time.Hour)
	n, err := s.store.DeleteFailedClipsBefore(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("delete expired failed clips: %w", err)
	}
	if n > 0 {
		s.logger.Info("deleted expired failed event clip rows", zap.Int64("rows", n))
	}
	return nil
}
