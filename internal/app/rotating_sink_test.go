package app

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenLogSinkRejectsUnboundedFileConfigurations(t *testing.T) {
	valid := RotationConfig{Enabled: true, MaxSizeMB: 1, MaxBackups: 1, MaxTotalSizeMB: 2}
	for name, rotation := range map[string]RotationConfig{
		"disabled":         {},
		"missing size":     {Enabled: true, MaxBackups: 1, MaxTotalSizeMB: 1},
		"missing count":    {Enabled: true, MaxSizeMB: 1, MaxTotalSizeMB: 2},
		"missing budget":   {Enabled: true, MaxSizeMB: 1, MaxBackups: 1},
		"budget too small": {Enabled: true, MaxSizeMB: 2, MaxBackups: 1, MaxTotalSizeMB: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := openLogSink(filepath.Join(t.TempDir(), "service.log"), rotation); err == nil {
				t.Fatal("expected unsafe file logging config to fail")
			}
		})
	}
	for _, output := range []string{"stderr", "stdout"} {
		if _, err := openLogSink(output, valid); err != nil {
			t.Errorf("stream output %q rejected: %v", output, err)
		}
	}
	compressed, err := openLogSink(filepath.Join(t.TempDir(), "compressed.log"), RotationConfig{
		Enabled: true, MaxSizeMB: 1, MaxBackups: 1, MaxTotalSizeMB: 2, Compress: true,
	})
	if err != nil {
		t.Fatalf("bounded compression configuration rejected: %v", err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("Close unprepared compression sink: %v", err)
	}
}

func TestBoundedFileSinkKeepsActiveAndArchivesWithinBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	sink := &boundedFileSink{
		path:         path,
		maxSize:      32,
		maxTotalSize: 96,
		maxBackups:   5,
		diagnostic:   &bytes.Buffer{},
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	for i := 0; i < 20; i++ {
		if _, err := sink.Write(bytes.Repeat([]byte{'x'}, 16)); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		activeInfo, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat active file: %v", err)
		}
		archives, err := sink.readArchivesLocked()
		if err != nil {
			t.Fatalf("read archives: %v", err)
		}
		total := activeInfo.Size()
		for _, archive := range archives {
			total += archive.size
		}
		if total > sink.maxTotalSize {
			t.Fatalf("stored %d bytes, exceeds budget %d", total, sink.maxTotalSize)
		}
		if activeInfo.Size() > sink.maxSize {
			t.Fatalf("active file has %d bytes, exceeds per-file limit %d", activeInfo.Size(), sink.maxSize)
		}
		if len(archives) > sink.maxBackups {
			t.Fatalf("retained %d archives, exceeds max_backups %d", len(archives), sink.maxBackups)
		}
	}
	if err := sink.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if gotErr, lost := sink.Health(); gotErr != nil || lost != 0 {
		t.Fatalf("sink health = (%v, %d), want healthy", gotErr, lost)
	}
}

func TestBoundedFileSinkStopsAfterEntryExceedsFileLimit(t *testing.T) {
	var diagnostic bytes.Buffer
	sink := &boundedFileSink{
		path:         filepath.Join(t.TempDir(), "service.log"),
		maxSize:      8,
		maxTotalSize: 16,
		maxBackups:   1,
		diagnostic:   &diagnostic,
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	if _, err := sink.Write([]byte("123456789")); err == nil {
		t.Fatal("expected oversized log entry to disable the sink")
	}
	if _, err := sink.Write([]byte("x")); err == nil {
		t.Fatal("failed sink accepted a later write")
	}
	failure, lost := sink.Health()
	if failure == nil || lost != 2 {
		t.Fatalf("sink health = (%v, %d), want a failure and two refused writes", failure, lost)
	}
	if !strings.Contains(diagnostic.String(), "file log sink disabled") {
		t.Fatalf("sink failure was not reported through the independent diagnostic writer: %q", diagnostic.String())
	}
	info, err := os.Stat(sink.path)
	if err != nil {
		t.Fatalf("stat active file: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("failed sink appended %d bytes", info.Size())
	}
}

func TestBoundedFileSinkExpiresArchivesByTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	created := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	archivePath := path + "." + created.Format("20060102T150405Z") + ".000001"
	if err := os.WriteFile(archivePath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old archive: %v", err)
	}
	sink := &boundedFileSink{
		path:         path,
		maxSize:      32,
		maxTotalSize: 64,
		maxBackups:   3,
		maxAge:       24 * time.Hour,
		diagnostic:   &bytes.Buffer{},
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	if _, err := os.Stat(archivePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired archive still exists, stat err=%v", err)
	}
}

func TestBoundedFileSinkCompressesWithinReservedBudget(t *testing.T) {
	var diagnostic bytes.Buffer
	path := filepath.Join(t.TempDir(), "service.log")
	sink := &boundedFileSink{
		path:         path,
		maxSize:      1024,
		maxTotalSize: 256 << 10,
		maxBackups:   2,
		compress:     true,
		diagnostic:   &diagnostic,
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	original := bytes.Repeat([]byte("log-event-"), 102)
	if _, err := sink.Write(original); err != nil {
		t.Fatalf("write active file: %v", err)
	}
	if _, err := sink.Write([]byte("next!")); err != nil {
		t.Fatalf("rotate and write: %v", err)
	}

	deadline := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	var compressedArchive rotatedArchive
	for {
		sink.mu.Lock()
		for _, archive := range sink.archives {
			if archive.compressed {
				compressedArchive = archive
				break
			}
		}
		pending := sink.compression != nil
		sink.mu.Unlock()
		if compressedArchive.path != "" {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("archive was not compressed: pending=%t diagnostic=%q", pending, diagnostic.String())
		}
	}
	sink.mu.Lock()
	total := sink.totalSizeLocked()
	sink.mu.Unlock()
	if total > sink.maxTotalSize {
		t.Fatalf("active, archives and reservations exceed budget: %d > %d", total, sink.maxTotalSize)
	}

	file, err := os.Open(compressedArchive.path)
	if err != nil {
		t.Fatalf("open compressed archive: %v", err)
	}
	reader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		t.Fatalf("open gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(reader)
	_ = reader.Close()
	_ = file.Close()
	if err != nil {
		t.Fatalf("read compressed archive: %v", err)
	}
	if !bytes.Equal(decoded, original) {
		t.Fatalf("decoded archive has %d bytes, want %d", len(decoded), len(original))
	}
	if gotErr, lost := sink.Health(); gotErr != nil || lost != 0 {
		t.Fatalf("sink health = (%v, %d), want healthy", gotErr, lost)
	}
}

func TestBoundedFileSinkSkipsCompressionWhenReservationDoesNotFit(t *testing.T) {
	var diagnostic bytes.Buffer
	path := filepath.Join(t.TempDir(), "service.log")
	sink := &boundedFileSink{
		path:         path,
		maxSize:      1024,
		maxTotalSize: 64 << 10,
		maxBackups:   2,
		compress:     true,
		diagnostic:   &diagnostic,
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	if _, err := sink.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
		t.Fatalf("write active file: %v", err)
	}
	if _, err := sink.Write([]byte("next")); err != nil {
		t.Fatalf("rotation should proceed without compression: %v", err)
	}
	sink.mu.Lock()
	archives := append([]rotatedArchive(nil), sink.archives...)
	sink.mu.Unlock()
	if len(archives) != 1 || archives[0].compressed {
		t.Fatalf("expected original uncompressed archive to be retained: %+v", archives)
	}
	if strings.Contains(diagnostic.String(), "reservation") == false {
		t.Fatalf("skipped compression was not reported: %q", diagnostic.String())
	}
	if _, err := os.Stat(archives[0].path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compression created a temporary output without a reservation: stat err=%v", err)
	}
}

func TestCompressionReservationIncludesActiveArchiveAndTemporaryOutput(t *testing.T) {
	const activeSize int64 = 4096
	const archiveSize int64 = 8192
	reservation := compressionReservation(archiveSize)
	budget := activeSize + archiveSize + reservation
	archive := rotatedArchive{path: filepath.Join(t.TempDir(), "service.log.20261020T120000Z.000001"), size: archiveSize}
	sink := &boundedFileSink{
		maxTotalSize:    budget,
		compress:        true,
		fileSize:        activeSize,
		archives:        []rotatedArchive{archive},
		compressionJobs: make(chan *compressionJob, 1),
		diagnostic:      &bytes.Buffer{},
	}

	sink.scheduleCompressionLocked(archive)
	if sink.compression == nil || sink.reservedBytes != reservation {
		t.Fatalf("compression reservation = %d, job=%v; want %d and one queued job", sink.reservedBytes, sink.compression, reservation)
	}
	if got := sink.totalSizeLocked(); got != budget {
		t.Fatalf("active + archive + temporary reservation = %d, want hard budget %d", got, budget)
	}
	if got := <-sink.compressionJobs; got != sink.compression {
		t.Fatal("queued compression job does not match reserved job")
	}

	var output bytes.Buffer
	limited := &limitedWriter{writer: &output, limit: reservation}
	if _, err := limited.Write(bytes.Repeat([]byte{'x'}, int(reservation))); err != nil {
		t.Fatalf("write within compression reservation: %v", err)
	}
	if _, err := limited.Write([]byte{'x'}); err == nil || int64(output.Len()) > reservation {
		t.Fatalf("temporary compression writer exceeded its reservation: size=%d err=%v", output.Len(), err)
	}
}

func TestBoundedFileSinkCloseWaitsForCompressionAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	sink := &boundedFileSink{
		path:         path,
		maxSize:      1 << 20,
		maxTotalSize: 3 << 20,
		maxBackups:   2,
		compress:     true,
		diagnostic:   &bytes.Buffer{},
	}
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := sink.Write(bytes.Repeat([]byte("log-event-"), (1<<20)/len("log-event-"))); err != nil {
		t.Fatalf("write active archive: %v", err)
	}
	if _, err := sink.Write([]byte("next")); err != nil {
		t.Fatalf("rotate active archive: %v", err)
	}

	results := make(chan error, 2)
	go func() { results <- sink.Close() }()
	go func() { results <- sink.Close() }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("Close %d: %v", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent Close did not finish after compression shutdown")
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read log directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("compression temporary file remained after Close: %s", entry.Name())
		}
	}
}

func TestBoundedFileSinkSchedulesAtMostOneCompression(t *testing.T) {
	active := &compressionJob{cancel: make(chan struct{}), done: make(chan struct{})}
	sink := &boundedFileSink{
		compress:        true,
		maxTotalSize:    1 << 20,
		compression:     active,
		compressionJobs: make(chan *compressionJob, 1),
	}
	sink.scheduleCompressionLocked(rotatedArchive{path: "older.log", size: 1024})
	if sink.compression != active || len(sink.compressionJobs) != 0 {
		t.Fatal("queued a second compression while one operation was active")
	}
}
