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

func compressArchive(job *compressionJob) (string, int64, error) {
	source, err := os.Open(job.archive.path)
	if err != nil {
		return "", 0, fmt.Errorf("open archive for compression: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat archive for compression: %w", err)
	}
	if info.Size() != job.archive.size {
		return "", 0, fmt.Errorf("archive size changed before compression (%d != %d)", info.Size(), job.archive.size)
	}
	temporary := job.archive.path + ".tmp"
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create bounded compression temporary file: %w", err)
	}
	keepTemporary := false
	defer func() {
		_ = output.Close()
		if !keepTemporary {
			_ = os.Remove(temporary)
		}
	}()

	limited := &limitedWriter{writer: output, limit: job.reservation}
	compressed := gzip.NewWriter(limited)
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-job.cancel:
			return "", 0, errCompressionCancelled
		default:
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			select {
			case <-job.cancel:
				return "", 0, errCompressionCancelled
			default:
			}
			if _, err := compressed.Write(buffer[:count]); err != nil {
				return "", 0, fmt.Errorf("write compressed archive: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", 0, fmt.Errorf("read archive for compression: %w", readErr)
		}
	}
	if err := compressed.Close(); err != nil {
		return "", 0, fmt.Errorf("finish compressed archive: %w", err)
	}
	if err := output.Sync(); err != nil {
		return "", 0, fmt.Errorf("sync compressed archive: %w", err)
	}
	if err := output.Close(); err != nil {
		return "", 0, fmt.Errorf("close compressed archive: %w", err)
	}
	if limited.written > job.reservation {
		return "", 0, fmt.Errorf("compressed archive exceeded its reservation (%d > %d bytes)", limited.written, job.reservation)
	}
	keepTemporary = true
	return temporary, limited.written, nil
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
		s.reservedBytes -= job.reservation
		s.compression = nil
		if !errors.Is(compressionErr, errCompressionCancelled) {
			s.reportDiagnosticLocked("compression failed for %s; retaining source archive: %v", job.archive.path, compressionErr)
		}
		return
	}
	select {
	case <-job.cancel:
		_ = os.Remove(temporary)
		s.reservedBytes -= job.reservation
		s.compression = nil
		return
	default:
	}

	destination := job.archive.path + ".gz"
	if _, err := os.Lstat(destination); err == nil {
		_ = os.Remove(temporary)
		s.reservedBytes -= job.reservation
		s.compression = nil
		s.reportDiagnosticLocked("compression skipped for %s: destination already exists", job.archive.path)
		return
	} else if !os.IsNotExist(err) {
		_ = os.Remove(temporary)
		s.reservedBytes -= job.reservation
		s.compression = nil
		s.reportDiagnosticLocked("compression failed for %s: inspect destination: %v", job.archive.path, err)
		return
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		s.reservedBytes -= job.reservation
		s.compression = nil
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
		s.reservedBytes -= job.reservation
		s.compression = nil
		s.failLocked(fmt.Errorf("remove uncompressed archive after compression: %w", err))
		return
	}
	if err := syncDirectory(filepath.Dir(job.archive.path)); err != nil {
		s.reservedBytes -= job.reservation
		s.compression = nil
		s.failLocked(fmt.Errorf("sync log directory after compression: %w", err))
		return
	}
	if index := s.archiveIndexLocked(job.archive.path); index >= 0 {
		s.archives[index] = rotatedArchive{
			path: destination, size: compressedSize, created: job.archive.created,
			sequence: job.archive.sequence, compressed: true,
		}
	}
	s.reservedBytes -= job.reservation
	s.compression = nil
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
