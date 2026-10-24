package app

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

var (
	errCompressionReservation = errors.New("log write dropped while a compression reservation is protected")
	errCompressionCancelled   = errors.New("log compression was skipped to preserve write capacity")
)

const compressionOverheadReserve int64 = 64 << 10

func compressionReservation(sourceSize int64) int64 {
	if sourceSize < 0 {
		return math.MaxInt64
	}
	overhead := sourceSize/8 + compressionOverheadReserve
	if sourceSize > math.MaxInt64-overhead {
		return math.MaxInt64
	}
	return sourceSize + overhead
}

func (s *boundedFileSink) scheduleCompressionLocked(archive rotatedArchive) {
	if !s.compress || s.closed || s.compression != nil || archive.compressed || archive.compressionAttempted {
		return
	}
	reservation := compressionReservation(archive.size)
	used := s.totalSizeLocked()
	if reservation > s.maxTotalSize-used {
		s.reportDiagnosticLocked("compression skipped for %s: temporary reservation of %d bytes does not fit the %d byte budget", archive.path, reservation, s.maxTotalSize)
		return
	}
	job := &compressionJob{
		archive:     archive,
		reservation: reservation,
		cancel:      make(chan struct{}),
		done:        make(chan struct{}),
	}
	s.reservedBytes = reservation
	s.compression = job
	select {
	case s.compressionJobs <- job:
		for i := range s.archives {
			if s.archives[i].path == archive.path {
				s.archives[i].compressionAttempted = true
				break
			}
		}
	default:
		s.reservedBytes = 0
		s.compression = nil
		s.reportDiagnosticLocked("compression skipped for %s: bounded worker is occupied", archive.path)
	}
}

func (s *boundedFileSink) scheduleNextCompressionLocked() {
	if !s.compress || s.closed || s.failed != nil || s.compression != nil {
		return
	}
	for _, archive := range s.archives {
		if !archive.compressed && !archive.compressionAttempted {
			s.scheduleCompressionLocked(archive)
			return
		}
	}
}

func (s *boundedFileSink) compressionReservationErrorLocked() error {
	if s.compression != nil {
		s.compression.cancelOnce.Do(func() { close(s.compression.cancel) })
	}
	return errCompressionReservation
}

func (s *boundedFileSink) compressionWorker() {
	defer close(s.compressionDone)
	for {
		select {
		case <-s.compressionStop:
			return
		case job := <-s.compressionJobs:
			temporary, size, err := compressArchive(job)
			s.finishCompression(job, temporary, size, err)
		}
	}
}

// releaseLocked returns job's reservation to the budget and frees the single
// compression slot. Every exit path of finishCompression must reach it exactly
// once: skipping one either leaks budget until the next restart or leaves the
// scheduler believing a compression is still running.
func (s *boundedFileSink) releaseLocked(job *compressionJob) {
	s.reservedBytes -= job.reservation
	s.compression = nil
}

// compressArchive writes the gzip copy of one archive and returns its size. The
// temporary file belongs to this function: on error it is already removed, on
// success the caller renames it into place.
func compressArchive(job *compressionJob) (string, int64, error) {
	source, err := os.Open(job.archive.path)
	if err != nil {
		return "", 0, fmt.Errorf("open archive for compression: %w", err)
	}
	// Read-only handle: a close failure here cannot lose data, and the copy below
	// is what reports real I/O errors.
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat archive for compression: %w", err)
	}
	if info.Size() != job.archive.size {
		return "", 0, fmt.Errorf("archive size changed before compression (%d != %d)", info.Size(), job.archive.size)
	}

	temporary := job.archive.path + ".tmp"
	size, err := writeCompressedCopy(job, source, temporary)
	if err != nil {
		if removeErr := os.Remove(temporary); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove compression temporary %s: %w", temporary, removeErr))
		}
		return "", 0, err
	}
	return temporary, size, nil
}

// writeCompressedCopy owns the output handle: it closes it exactly once and, when
// the stream itself succeeded, reports a Sync or Close failure. That is the last
// place where the final gzip bytes can still surface an I/O error.
func writeCompressedCopy(job *compressionJob, source io.Reader, path string) (int64, error) {
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create bounded compression temporary file: %w", err)
	}

	limited := &limitedWriter{writer: output, limit: job.reservation}
	writeErr := writeGzipStream(job, source, limited)
	if writeErr == nil && limited.written > job.reservation {
		writeErr = fmt.Errorf("compressed archive exceeded its reservation (%d > %d bytes)",
			limited.written, job.reservation)
	}
	if writeErr == nil {
		if syncErr := output.Sync(); syncErr != nil {
			writeErr = fmt.Errorf("sync compressed archive: %w", syncErr)
		}
	}
	if closeErr := output.Close(); closeErr != nil && writeErr == nil {
		writeErr = fmt.Errorf("close compressed archive: %w", closeErr)
	}
	if writeErr != nil {
		return 0, writeErr
	}
	return limited.written, nil
}

// writeGzipStream copies source through a gzip writer in chunks, checking for
// cancellation between chunks so a shutdown never waits for a whole archive.
func writeGzipStream(job *compressionJob, source io.Reader, destination io.Writer) error {
	compressed := gzip.NewWriter(destination)
	buffer := make([]byte, 32*1024)
	for {
		if compressionCancelled(job) {
			return errCompressionCancelled
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			if compressionCancelled(job) {
				return errCompressionCancelled
			}
			if _, err := compressed.Write(buffer[:count]); err != nil {
				return fmt.Errorf("write compressed archive: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read archive for compression: %w", readErr)
		}
	}
	if err := compressed.Close(); err != nil {
		return fmt.Errorf("finish compressed archive: %w", err)
	}
	return nil
}

func compressionCancelled(job *compressionJob) bool {
	select {
	case <-job.cancel:
		return true
	default:
		return false
	}
}

func (s *boundedFileSink) finishCompression(job *compressionJob, temporary string, compressedSize int64, compressionErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	completed := false
	defer func() {
		if completed {
			s.scheduleNextCompressionLocked()
		}
		close(job.done)
	}()
	if s.compression != job {
		if temporary != "" {
			_ = os.Remove(temporary)
		}
		return
	}
	if compressionErr != nil {
		if temporary != "" {
			_ = os.Remove(temporary)
		}
		s.releaseLocked(job)
		if !errors.Is(compressionErr, errCompressionCancelled) {
			s.reportDiagnosticLocked("compression failed for %s; retaining source archive: %v", job.archive.path, compressionErr)
		}
		return
	}
	select {
	case <-job.cancel:
		_ = os.Remove(temporary)
		s.releaseLocked(job)
		return
	default:
	}

	destination := job.archive.path + ".gz"
	if _, err := os.Lstat(destination); err == nil {
		_ = os.Remove(temporary)
		s.releaseLocked(job)
		s.reportDiagnosticLocked("compression skipped for %s: destination already exists", job.archive.path)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporary)
		s.releaseLocked(job)
		s.reportDiagnosticLocked("compression failed for %s: inspect destination: %v", job.archive.path, err)
		return
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		s.releaseLocked(job)
		s.reportDiagnosticLocked("compression failed for %s: rename output: %v", job.archive.path, err)
		return
	}
	if err := os.Remove(job.archive.path); err != nil {
		if index := s.archiveIndexLocked(job.archive.path); index >= 0 {
			s.archives = append(s.archives, rotatedArchive{
				path: destination, size: compressedSize, created: job.archive.created,
				sequence: job.archive.sequence, compressed: true,
			})
		}
		s.releaseLocked(job)
		s.failLocked(fmt.Errorf("remove uncompressed archive after compression: %w", err))
		return
	}
	if err := syncDirectory(filepath.Dir(job.archive.path)); err != nil {
		s.releaseLocked(job)
		s.failLocked(fmt.Errorf("sync log directory after compression: %w", err))
		return
	}
	if index := s.archiveIndexLocked(job.archive.path); index >= 0 {
		s.archives[index] = rotatedArchive{
			path: destination, size: compressedSize, created: job.archive.created,
			sequence: job.archive.sequence, compressed: true,
		}
	}
	s.releaseLocked(job)
	if err := s.pruneLocked(0); err != nil {
		s.failLocked(fmt.Errorf("prune compressed log archives: %w", err))
		return
	}
	completed = true
}

func (s *boundedFileSink) archiveIndexLocked(path string) int {
	for i, archive := range s.archives {
		if archive.path == path {
			return i
		}
	}
	return -1
}

func (s *boundedFileSink) reportDiagnosticLocked(format string, values ...any) {
	writer := s.diagnostic
	if writer == nil {
		writer = os.Stderr
	}
	_, _ = fmt.Fprintf(writer, "log sink: "+format+"\n", values...)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return err
	}
	return nil
}

type limitedWriter struct {
	writer  io.Writer
	limit   int64
	written int64
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.limit-w.written {
		return 0, fmt.Errorf("compression output exceeds reserved %d bytes", w.limit)
	}
	written, err := w.writer.Write(data)
	w.written += int64(written)
	if err != nil {
		return written, err
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	return written, nil
}

func (s *boundedFileSink) Close() error {
	s.mu.Lock()
	if s.closeDone != nil {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	s.closeDone = make(chan struct{})
	closeDone := s.closeDone
	job := s.compression
	workerStop := s.compressionStop
	workerDone := s.compressionDone
	file := s.file
	s.file = nil
	var closeErr error
	if file != nil {
		if err := file.Close(); err != nil {
			closeErr = s.failLocked(fmt.Errorf("close log output: %w", err))
		}
	}
	s.mu.Unlock()

	if job != nil {
		<-job.done
	}
	if workerStop != nil {
		close(workerStop)
		<-workerDone
	}

	s.mu.Lock()
	s.closeErr = closeErr
	close(closeDone)
	s.mu.Unlock()
	return closeErr
}
