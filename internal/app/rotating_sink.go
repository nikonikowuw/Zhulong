package app

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

const mebibyte int64 = 1 << 20

type streamLogSink struct {
	writer zapcore.WriteSyncer
}

func (s *streamLogSink) Prepare() error                 { return nil }
func (s *streamLogSink) Health() (error, uint64)        { return nil, 0 }
func (s *streamLogSink) Close() error                   { return nil }
func (s *streamLogSink) Write(data []byte) (int, error) { return s.writer.Write(data) }
func (s *streamLogSink) Sync() error                    { return s.writer.Sync() }

type rotatedArchive struct {
	path                 string
	size                 int64
	created              time.Time
	sequence             uint64
	compressed           bool
	compressionAttempted bool
}

type compressionJob struct {
	archive     rotatedArchive
	reservation int64
	cancel      chan struct{}
	cancelOnce  sync.Once
	done        chan struct{}
}

// boundedFileSink allows one process to write a path while enforcing a hard
// aggregate budget across the active file, archives and compression reservations.
type boundedFileSink struct {
	mu sync.Mutex

	path         string
	maxSize      int64
	maxTotalSize int64
	maxBackups   int
	maxAge       time.Duration
	compress     bool

	file       *os.File
	fileSize   int64
	archives   []rotatedArchive
	sequence   uint64
	prepared   bool
	closed     bool
	failed     error
	lostWrites uint64
	diagnostic io.Writer

	reservedBytes   int64
	compression     *compressionJob
	compressionJobs chan *compressionJob
	compressionStop chan struct{}
	compressionDone chan struct{}
	closeDone       chan struct{}
	closeErr        error
}

func openLogSink(output string, rotation RotationConfig) (loggerOutput, error) {
	switch output {
	case "stderr":
		return &streamLogSink{writer: zapcore.Lock(os.Stderr)}, nil
	case "stdout":
		return &streamLogSink{writer: zapcore.Lock(os.Stdout)}, nil
	}

	if !rotation.Enabled {
		return nil, errors.New("file logging requires logging.rotation.enabled=true")
	}
	if rotation.MaxSizeMB <= 0 {
		return nil, errors.New("file logging requires a positive logging.rotation.max_size_mb")
	}
	if rotation.MaxBackups <= 0 {
		return nil, errors.New("file logging requires a positive logging.rotation.max_backups")
	}
	if rotation.MaxAgeDays < 0 {
		return nil, errors.New("logging.rotation.max_age_days must not be negative")
	}
	if int64(rotation.MaxAgeDays) > math.MaxInt64/int64(24*time.Hour) {
		return nil, errors.New("logging.rotation.max_age_days exceeds the supported duration range")
	}
	if rotation.MaxTotalSizeMB <= 0 {
		return nil, errors.New("file logging requires a positive logging.rotation.max_total_size_mb")
	}
	if rotation.MaxSizeMB > math.MaxInt64/mebibyte || rotation.MaxTotalSizeMB > math.MaxInt64/mebibyte {
		return nil, errors.New("logging.rotation size exceeds the supported byte range")
	}
	maxSize := rotation.MaxSizeMB * mebibyte
	maxTotalSize := rotation.MaxTotalSizeMB * mebibyte
	if maxSize > maxTotalSize {
		return nil, errors.New("logging.rotation.max_total_size_mb must be at least max_size_mb")
	}
	if strings.TrimSpace(output) == "" {
		return nil, errors.New("logging.output must not be empty")
	}

	return &boundedFileSink{
		path:         filepath.Clean(output),
		maxSize:      maxSize,
		maxTotalSize: maxTotalSize,
		maxBackups:   rotation.MaxBackups,
		maxAge:       time.Duration(rotation.MaxAgeDays) * 24 * time.Hour,
		compress:     rotation.Compress,
		diagnostic:   os.Stderr,
	}, nil
}

func (s *boundedFileSink) Prepare() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareLocked()
}

func (s *boundedFileSink) prepareLocked() error {
	if s.failed != nil {
		return s.failed
	}
	if s.closed {
		return errors.New("file log sink is closed")
	}
	if s.prepared {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return s.failLocked(fmt.Errorf("create log directory: %w", err))
	}
	archives, err := s.readArchivesLocked()
	if err != nil {
		return s.failLocked(err)
	}
	s.archives = archives
	for _, archive := range archives {
		if archive.sequence > s.sequence {
			s.sequence = archive.sequence
		}
	}
	if info, err := os.Lstat(s.path); err == nil && !info.Mode().IsRegular() {
		return s.failLocked(fmt.Errorf("log output %q is not a regular file", s.path))
	} else if err != nil && !os.IsNotExist(err) {
		return s.failLocked(fmt.Errorf("inspect log output: %w", err))
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return s.failLocked(fmt.Errorf("open log output: %w", err))
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return s.failLocked(fmt.Errorf("stat log output: %w", err))
	}
	s.file = file
	s.fileSize = info.Size()
	if s.fileSize > s.maxTotalSize {
		_ = s.file.Close()
		s.file = nil
		return s.failLocked(fmt.Errorf("active log file exceeds total storage budget (%d > %d bytes)", s.fileSize, s.maxTotalSize))
	}
	if err := s.pruneLocked(0); err != nil {
		_ = s.file.Close()
		s.file = nil
		return s.failLocked(err)
	}
	if s.compress {
		s.compressionJobs = make(chan *compressionJob, 1)
		s.compressionStop = make(chan struct{})
		s.compressionDone = make(chan struct{})
		go s.compressionWorker()
	}
	if s.fileSize > s.maxSize {
		if err := s.rotateLocked(); err != nil {
			return s.failLocked(err)
		}
	}
	s.prepared = true
	return nil
}

func (s *boundedFileSink) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		s.lostWrites++
		return 0, s.failed
	}
	if s.closed {
		s.lostWrites++
		return 0, errors.New("file log sink is closed")
	}
	if !s.prepared {
		return 0, errors.New("file log sink has not been prepared by the application lifecycle")
	}
	if int64(len(data)) > s.maxSize {
		return 0, s.failWriteLocked(fmt.Errorf("log entry exceeds per-file size limit (%d > %d bytes)", len(data), s.maxSize))
	}
	if int64(len(data)) > s.maxSize-s.fileSize {
		if err := s.rotateLocked(); err != nil {
			if errors.Is(err, errCompressionReservation) {
				s.lostWrites++
				return 0, err
			}
			return 0, s.failWriteLocked(err)
		}
	}
	if err := s.pruneLocked(int64(len(data))); err != nil {
		if errors.Is(err, errCompressionReservation) {
			s.lostWrites++
			return 0, err
		}
		return 0, s.failWriteLocked(err)
	}
	written, err := s.file.Write(data)
	s.fileSize += int64(written)
	if err != nil {
		return written, s.failWriteLocked(fmt.Errorf("write log output: %w", err))
	}
	if written != len(data) {
		return written, s.failWriteLocked(io.ErrShortWrite)
	}
	return written, nil
}

func (s *boundedFileSink) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return s.failed
	}
	if err := s.file.Sync(); err != nil {
		return s.failLocked(fmt.Errorf("sync log output: %w", err))
	}
	return nil
}

// Health reports a persistent sink failure and the number of log writes rejected by the sink.
func (s *boundedFileSink) Health() (error, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed, s.lostWrites
}

func (s *boundedFileSink) rotateLocked() error {
	if s.fileSize == 0 {
		return nil
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync active log before rotation: %w", err)
	}
	if err := s.file.Close(); err != nil {
		s.file = nil
		return fmt.Errorf("close active log before rotation: %w", err)
	}
	s.file = nil
	archivePath, created, sequence, err := s.nextArchivePathLocked()
	if err != nil {
		return err
	}
	if err := os.Rename(s.path, archivePath); err != nil {
		return fmt.Errorf("rotate active log: %w", err)
	}
	archiveSize := s.fileSize
	s.archives = append(s.archives, rotatedArchive{
		path: archivePath, size: archiveSize, created: created, sequence: sequence,
	})
	sortArchivesLocked(s.archives)
	s.fileSize = 0
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("reopen active log after rotation: %w", err)
	}
	s.file = file
	if err := syncDirectory(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("sync log directory after rotation: %w", err)
	}
	if err := s.pruneLocked(0); err != nil {
		return err
	}
	if s.compress {
		for i := len(s.archives) - 1; i >= 0; i-- {
			if s.archives[i].path == archivePath {
				s.scheduleCompressionLocked(s.archives[i])
				break
			}
		}
	}
	return nil
}

func (s *boundedFileSink) nextArchivePathLocked() (string, time.Time, uint64, error) {
	created := time.Now().UTC().Truncate(time.Second)
	for attempts := 0; attempts < 1_000_000; attempts++ {
		s.sequence++
		name := fmt.Sprintf("%s.%s.%06d", s.path, created.Format("20060102T150405Z"), s.sequence)
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			return name, created, s.sequence, nil
		} else if err != nil {
			return "", time.Time{}, 0, fmt.Errorf("check archive path: %w", err)
		}
	}
	return "", time.Time{}, 0, errors.New("unable to allocate a unique log archive name")
}

func (s *boundedFileSink) readArchivesLocked() ([]rotatedArchive, error) {
	directory := filepath.Dir(s.path)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list log archives: %w", err)
	}
	prefix := filepath.Base(s.path) + "."
	archives := make([]rotatedArchive, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		candidate := filepath.Join(directory, name)
		archiveName := strings.TrimPrefix(name, prefix)
		temporary := strings.HasSuffix(archiveName, ".tmp")
		if temporary {
			archiveName = strings.TrimSuffix(archiveName, ".tmp")
		}
		compressed := strings.HasSuffix(archiveName, ".gz")
		if compressed {
			archiveName = strings.TrimSuffix(archiveName, ".gz")
		}
		parts := strings.Split(archiveName, ".")
		if len(parts) != 2 {
			continue
		}
		created, parseErr := time.Parse("20060102T150405Z", parts[0])
		if parseErr != nil {
			continue
		}
		sequence, parseErr := strconv.ParseUint(parts[1], 10, 64)
		if parseErr != nil {
			continue
		}
		if temporary {
			if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("remove abandoned log compression file: %w", err)
			}
			continue
		}
		info, statErr := os.Lstat(candidate)
		if statErr != nil {
			return nil, fmt.Errorf("inspect log archive: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("log archive %q is not a regular file", candidate)
		}
		archives = append(archives, rotatedArchive{
			path: candidate, size: info.Size(), created: created, sequence: sequence, compressed: compressed,
		})
	}
	sortArchivesLocked(archives)
	return archives, nil
}

func sortArchivesLocked(archives []rotatedArchive) {
	sort.Slice(archives, func(i, j int) bool {
		if archives[i].sequence != archives[j].sequence {
			return archives[i].sequence < archives[j].sequence
		}
		if !archives[i].created.Equal(archives[j].created) {
			return archives[i].created.Before(archives[j].created)
		}
		return archives[i].path < archives[j].path
	})
}

func (s *boundedFileSink) pruneLocked(extraBytes int64) error {
	now := time.Now().UTC()
	for i := 0; i < len(s.archives); {
		archive := s.archives[i]
		if s.maxAge <= 0 || now.Sub(archive.created) <= s.maxAge || s.archiveCompressingLocked(archive.path) {
			i++
			continue
		}
		if err := s.removeArchiveLocked(i); err != nil {
			return err
		}
	}
	for len(s.archives) > s.maxBackups {
		index := s.oldestRemovableArchiveLocked()
		if index < 0 {
			return s.compressionReservationErrorLocked()
		}
		if err := s.removeArchiveLocked(index); err != nil {
			return err
		}
	}
	for total := s.totalSizeLocked(); extraBytes > s.maxTotalSize-total; total = s.totalSizeLocked() {
		index := s.oldestRemovableArchiveLocked()
		if index < 0 {
			if s.compression != nil {
				return s.compressionReservationErrorLocked()
			}
			return fmt.Errorf("log storage budget exhausted (%d byte budget)", s.maxTotalSize)
		}
		if err := s.removeArchiveLocked(index); err != nil {
			return err
		}
	}
	return nil
}

func (s *boundedFileSink) oldestRemovableArchiveLocked() int {
	if len(s.archives) == 0 || s.archiveCompressingLocked(s.archives[0].path) {
		return -1
	}
	return 0
}

func (s *boundedFileSink) archiveCompressingLocked(path string) bool {
	return s.compression != nil && s.compression.archive.path == path
}

func (s *boundedFileSink) totalSizeLocked() int64 {
	total := s.fileSize
	for _, archive := range s.archives {
		if archive.size > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += archive.size
	}
	if s.reservedBytes > math.MaxInt64-total {
		return math.MaxInt64
	}
	return total + s.reservedBytes
}

func (s *boundedFileSink) removeArchiveLocked(index int) error {
	archive := s.archives[index]
	if s.archiveCompressingLocked(archive.path) {
		return s.compressionReservationErrorLocked()
	}
	if err := os.Remove(archive.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove old log archive: %w", err)
	}
	if err := syncDirectory(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("sync log directory after archive removal: %w", err)
	}
	s.archives = append(s.archives[:index], s.archives[index+1:]...)
	return nil
}

func (s *boundedFileSink) failWriteLocked(err error) error {
	s.lostWrites++
	return s.failLocked(err)
}

func (s *boundedFileSink) failLocked(err error) error {
	if s.failed == nil {
		s.failed = err
		if s.diagnostic == nil {
			s.diagnostic = os.Stderr
		}
		_, _ = fmt.Fprintf(s.diagnostic, "zhulong: file log sink disabled: %v\n", err)
	}
	return s.failed
}
