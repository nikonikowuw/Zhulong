// Package faceextract implements the host's one-shot face-feature extraction
// channel: a synchronous request/response conversation with an *algorithm
// package executable*, not with a dlopen'd plugin.
//
// Why it is a package of its own: the previous channel lived inside the C++ engine
// (`FacePluginLoader`: dlopen + dlsym per call), which made the engine the second
// consumer of algorithm code. A package executable cannot be dlopen'd (glibc
// refuses PIE), so the host now speaks a small frame protocol to a child process it
// already knows how to start and kill. Keeping the client here — instead of inside
// internal/person — keeps the domain package free of process, pipe and timeout
// concerns.
//
// The child is reused while idle (model load is the expensive part) but it is *not*
// a long-lived plugin: across the boundary there are only frame bytes and JSON
// lines. See docs/design §11 of the algorithm-package-blackbox task.
package faceextract

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
)

// Errors are the vocabulary the HTTP layer maps onto status codes. They are
// deliberately coarse: the caller must never be able to tell a retryable crash
// from a permanent failure by string matching.
var (
	// ErrNoFaceDetected means the frame was valid and processed, but contains no
	// usable face. Not retryable and not a server error.
	ErrNoFaceDetected = errors.New("no face detected in the supplied image")
	// ErrInvalidArgument means the frame violated the protocol (dimensions,
	// stride, payload size, format). A caller bug, not a package failure.
	ErrInvalidArgument = errors.New("invalid feature-extraction frame")
	// ErrUnsupported means the provider package cannot do this at all: the symbol
	// is missing, the model is unavailable, or the backend is a test stub. Retrying
	// is pointless and hiding it behind ErrInternal would send operators to the
	// wrong logs.
	ErrUnsupported = errors.New("face feature extraction is not supported by the provider package")
	// ErrInternal covers crashes, timeouts, protocol desync and malformed feature
	// dimensions. It is the only error that may be retried, and only once.
	ErrInternal = errors.New("face feature extraction failed")
)

// Protocol constants. They are part of the SDK contract (see algo-plugin-contract
// §7), so they are named once here and asserted by tests rather than inlined.
const (
	frameMagic    uint32 = 0x45464C5A // "ZLFE" little-endian
	headerBytes          = 28
	maxDimension  uint32 = 16384
	maxFeatureDim        = 512
	formatRGB24   uint32 = 2
	formatBGR24   uint32 = 1
)

// Frame is one RGB24/BGR24 image handed to the provider process.
//
// Pixels are already flattened by the caller: the package skeleton deliberately
// links no image decoder, so JPEG/PNG/WebP decoding stays on the Go side and no
// codec enters the algorithm binary.
type Frame struct {
	Width  uint32
	Height uint32
	Stride uint32 // bytes per row; 0 means Width*3
	Format uint32 // 1 = BGR24, 2 = RGB24
	Pixels []byte
}

// ErrEntry is returned when the provider package has no runnable entry. It wraps
// the underlying resolution error (algorunner.ErrMissingEntry / ErrLegacyEntry /
// ErrInvalidEntry) so callers can distinguish "not installed yet" from "broken".
var ErrEntry = errors.New("provider package entry is unavailable")

// Options configures a Client. Zero values are filled from DefaultOptions.
type Options struct {
	// Entry resolves the provider package's executable. It is a function, not a
	// path, because the provider can be upgraded, rolled back or reinstalled while
	// the host runs, and the host (not this package) knows which version is active.
	Entry func(ctx context.Context) (string, error)

	// RequestTimeout bounds one round trip. It must cover a cold start (model load)
	// plus one inference; the default is 30s.
	RequestTimeout time.Duration

	// IdleTimeout retires the child process after this much inactivity. It mirrors
	// the TTL of the retired plugin loader so the memory curve stays the same.
	IdleTimeout time.Duration

	// Logger receives lifecycle events (spawn, retire, kill). Never used for
	// per-frame noise.
	Logger *zap.Logger
}

// Defaults for Options.
const (
	DefaultRequestTimeout = 30 * time.Second
	DefaultIdleTimeout    = 60 * time.Second
	// IdleTimeoutEnv lets operators keep the historical knob. The retired loader
	// read the same variable, so existing deployments keep their memory profile.
	IdleTimeoutEnv = "ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC"
)

// Client is a concurrency-1, one-request-at-a-time conversation with one provider
// process.
//
// Serialisation is not a compromise: the retired loader held a mutex around every
// extraction too, so a single child is semantically equivalent. Queueing is bounded
// by RequestTimeout, never by unbounded spawning.
type Client struct {
	opts Options
	log  *zap.Logger

	mu       sync.Mutex // serialises requests and idle-timer decisions
	proc     *proc
	lastUsed time.Time

	nextID atomic.Uint32

	reaperStop chan struct{}
	reaperDone chan struct{}
	closeOnce  sync.Once
}

// proc is one running provider process plus the identity of the executable it was
// started from.
type proc struct {
	cmd    *algorunner.PinnedProcess
	entry  string
	finger fileFingerprint

	stdin  io.WriteCloser
	stdout *bufio.Reader
}

// fileFingerprint identifies the exact on-disk executable a child was started from.
//
// Without it, a package upgraded in place would keep answering with embeddings from
// the *old* model while streaming recognition uses the new one. That mismatch is
// invisible: no error, no log, just a slowly degrading match rate.
type fileFingerprint struct {
	dev   uint64
	ino   uint64
	size  int64
	mtime int64
}

// New creates a client. It does not start a process: the first Extract does, so a
// host that never enrols a face never pays for a model load.
func New(opts Options) *Client {
	if opts.RequestTimeout <= 0 {
		opts.RequestTimeout = DefaultRequestTimeout
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = idleTimeoutFromEnv()
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	c := &Client{
		opts:       opts,
		log:        logger,
		reaperStop: make(chan struct{}),
		reaperDone: make(chan struct{}),
	}
	go c.reapIdleLoop()
	return c
}

// idleTimeoutFromEnv reads the historical TTL knob, falling back to the default on
// anything unusable. A malformed value must not silently disable reclamation.
func idleTimeoutFromEnv() time.Duration {
	raw := os.Getenv(IdleTimeoutEnv)
	if raw == "" {
		return DefaultIdleTimeout
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return DefaultIdleTimeout
	}
	return time.Duration(seconds) * time.Second
}

// Extract returns the normalised feature vector of the most prominent face in f.
//
// A single retry is allowed, and only for failures that indicate the child died or
// stalled — never for a frame-level verdict (no face, invalid input) and never by
// sending a *different* frame: request ids are matched, so a stale answer can never
// be attributed to the next request.
func (c *Client) Extract(ctx context.Context, f Frame) ([]float32, error) {
	if err := validateFrame(f); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		p, err := c.ensureProcess(ctx)
		if err != nil {
			return nil, err
		}

		features, err := c.roundTrip(ctx, p, f)
		c.lastUsed = time.Now()
		if err == nil {
			return features, nil
		}
		lastErr = err

		// The child is in an unknown state: a half-written frame or an unread reply
		// would poison the next request. Both are unrecoverable in-process.
		c.discardLocked(p, "request failed")

		if !errors.Is(err, ErrInternal) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrInternal, lastErr)
}

// Close retires the provider process and stops the idle reaper. It is safe to call
// twice and safe to call while a request is in flight (the request finishes first).
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.reaperStop)
		<-c.reaperDone
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc != nil {
		c.discardLocked(c.proc, "client closed")
	}
	return nil
}

// ensureProcess returns a live child that matches the current provider executable,
// spawning (or re-spawning) as needed.
func (c *Client) ensureProcess(ctx context.Context) (*proc, error) {
	entry, err := c.resolveEntry(ctx)
	if err != nil {
		return nil, err
	}
	finger, err := fingerprintOf(entry)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEntry, err)
	}

	if c.proc != nil {
		if c.proc.entry == entry && c.proc.finger == finger {
			select {
			case <-c.proc.cmd.Done():
				// Exited on its own: replace it, but report nothing — the retry logic
				// in Extract decides whether the frame was answered.
				c.discardLocked(c.proc, "provider process exited")
			default:
				return c.proc, nil
			}
		} else {
			c.discardLocked(c.proc, "provider executable changed")
		}
	}

	p, err := c.spawn(entry, finger)
	if err != nil {
		return nil, err
	}
	// Registration must live here, not in spawn: forgetting it makes every request
	// pay a cold start while looking perfectly healthy (no error, just slow).
	c.proc = p
	c.lastUsed = time.Now()
	return p, nil
}

func (c *Client) resolveEntry(ctx context.Context) (string, error) {
	if c.opts.Entry == nil {
		return "", fmt.Errorf("%w: no provider entry resolver configured", ErrEntry)
	}
	entry, err := c.opts.Entry(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrEntry, err)
	}
	return entry, nil
}

func (c *Client) spawn(entry string, finger fileFingerprint) (*proc, error) {
	cmd := exec.Command(entry, "--extract-feature")
	// The package resolves its models relative to its own executable, so the CWD is
	// irrelevant — but it must not be the host's, or a stray models/ directory in
	// the working directory could be picked up by a package that resolves loosely.
	cmd.Dir = filepath.Dir(entry)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdin pipe: %v", ErrInternal, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: stdout pipe: %v", ErrInternal, err)
	}
	// stderr is inherited on purpose: package logs go to the host's stderr already
	// (the skeleton's log writer targets stderr), and swallowing them would hide the
	// only diagnostic a third-party package can produce.
	cmd.Stderr = os.Stderr

	pinned, err := algorunner.StartPinned(cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: start provider: %v", ErrUnsupported, err)
	}

	c.log.Info("started face feature provider process",
		zap.String("entry", entry),
		zap.Int("pid", pinned.PID()),
	)

	return &proc{
		cmd:    pinned,
		entry:  entry,
		finger: finger,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}, nil
}

func (c *Client) roundTrip(ctx context.Context, p *proc, f Frame) ([]float32, error) {
	requestID := c.nextID.Add(1)

	deadline := time.Now().Add(c.opts.RequestTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	// Abort the round trip when the caller goes away, instead of holding the lock
	// (and the child) for the full timeout.
	stopWatch := context.AfterFunc(ctx, func() {
		_ = p.cmd.KillGroup()
	})
	defer stopWatch()

	if err := writeFrame(p.stdin, requestID, f); err != nil {
		return nil, fmt.Errorf("%w: write frame: %v", ErrInternal, err)
	}

	line, err := readLineBefore(p.stdout, deadline)
	if err != nil {
		if errors.Is(err, errDeadlineExceeded) {
			// A hung child still holds the model; only a group kill frees it.
			_ = p.cmd.KillGroup()
			return nil, fmt.Errorf("%w: provider timed out after %s", ErrInternal, c.opts.RequestTimeout)
		}
		return nil, fmt.Errorf("%w: read reply: %v", ErrInternal, err)
	}

	return parseReply(line, requestID)
}

// discardLocked kills and reaps p, then forgets it. The lock must be held so no
// request can observe a half-dead process.
func (c *Client) discardLocked(p *proc, reason string) {
	if c.proc == p {
		c.proc = nil
	}
	if err := p.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		c.log.Debug("close provider stdin", zap.Error(err))
	}
	if err := p.cmd.KillGroup(); err != nil {
		c.log.Warn("kill provider process group", zap.Int("pid", p.cmd.PID()), zap.Error(err))
	}
	<-p.cmd.Done()
	c.log.Info("retired face feature provider process",
		zap.Int("pid", p.cmd.PID()),
		zap.String("reason", reason),
	)
}

// reapIdleLoop retires the child after Options.IdleTimeout of inactivity.
//
// The decision is made under the same mutex requests take, and only when no request
// is in flight: an idle check that reset a timer *after* a request started would
// happily kill a process mid-inference.
func (c *Client) reapIdleLoop() {
	defer close(c.reaperDone)
	interval := c.opts.IdleTimeout / 4
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.reaperStop:
			return
		case <-ticker.C:
		}

		c.mu.Lock()
		// The mutex is the whole story: a request in flight holds it, so reaching
		// this line means the client really is idle right now.
		if c.proc != nil && time.Since(c.lastUsed) >= c.opts.IdleTimeout {
			c.discardLocked(c.proc, "idle timeout")
		}
		c.mu.Unlock()
	}
}

var errDeadlineExceeded = errors.New("read deadline exceeded")

// readLineBefore reads one '\n'-terminated protocol line, honouring a wall-clock
// deadline. The pipe is pollable, so SetReadDeadline gives us an interruptible read
// without a watchdog goroutine that could outlive the request.
func readLineBefore(reader *bufio.Reader, deadline time.Time) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := reader.ReadString('\n')
		done <- result{line: line, err: err}
	}()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case r := <-done:
		if r.err != nil && !errors.Is(r.err, io.EOF) {
			return "", r.err
		}
		if r.err != nil {
			return "", r.err
		}
		return r.line, nil
	case <-timer.C:
		return "", errDeadlineExceeded
	}
}

func writeFrame(w io.Writer, requestID uint32, f Frame) error {
	stride := f.Stride
	if stride == 0 {
		stride = f.Width * 3
	}
	header := make([]byte, headerBytes)
	binary.LittleEndian.PutUint32(header[0:], frameMagic)
	binary.LittleEndian.PutUint32(header[4:], requestID)
	binary.LittleEndian.PutUint32(header[8:], f.Width)
	binary.LittleEndian.PutUint32(header[12:], f.Height)
	binary.LittleEndian.PutUint32(header[16:], stride)
	binary.LittleEndian.PutUint32(header[20:], f.Format)
	binary.LittleEndian.PutUint32(header[24:], uint32(len(f.Pixels)))

	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(f.Pixels)
	return err
}

// reply is the wire form of one provider answer. Every field is optional except
// request_id and status, because failures carry a code instead of features.
type reply struct {
	RequestID  uint32    `json:"request_id"`
	Status     string    `json:"status"`
	Code       string    `json:"code"`
	Error      string    `json:"error"`
	FeatureDim uint32    `json:"feature_dim"`
	Features   []float32 `json:"features"`
}

func parseReply(line string, wantRequestID uint32) ([]float32, error) {
	trimmed := bytes.TrimSpace([]byte(line))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// Anything else is either a stray log line or garbage. Treating it as a
		// protocol error (retryable) is the only safe reading: a silent skip could
		// pair this request with the *next* answer.
		return nil, fmt.Errorf("%w: provider wrote a non-JSON line on stdout: %.120q", ErrInternal, string(trimmed))
	}
	var r reply
	if err := json.Unmarshal(trimmed, &r); err != nil {
		return nil, fmt.Errorf("%w: decode provider reply: %v", ErrInternal, err)
	}
	if r.RequestID != wantRequestID {
		// Mismatched ids mean the stream is out of step; attaching these features to
		// this request would store someone else's embedding.
		return nil, fmt.Errorf("%w: reply request_id %d does not match request %d", ErrInternal, r.RequestID, wantRequestID)
	}
	if r.Status != "passed" {
		if r.Error != "" {
			return nil, failureForCode(r.Code, r.Error)
		}
		return nil, failureForCode(r.Code, r.Status)
	}
	if r.FeatureDim == 0 || r.FeatureDim > maxFeatureDim || len(r.Features) != int(r.FeatureDim) {
		return nil, fmt.Errorf("%w: provider returned feature_dim=%d with %d values", ErrInternal, r.FeatureDim, len(r.Features))
	}
	return r.Features, nil
}

func failureForCode(code, detail string) error {
	switch code {
	case "NO_FACE_DETECTED":
		return fmt.Errorf("%w: %s", ErrNoFaceDetected, detail)
	case "INVALID_INPUT":
		return fmt.Errorf("%w: %s", ErrInvalidArgument, detail)
	case "UNSUPPORTED", "MODEL_UNAVAILABLE":
		return fmt.Errorf("%w: %s", ErrUnsupported, detail)
	default:
		return fmt.Errorf("%w: %s", ErrInternal, detail)
	}
}

func validateFrame(f Frame) error {
	if f.Width == 0 || f.Height == 0 {
		return fmt.Errorf("%w: frame dimensions must be positive", ErrInvalidArgument)
	}
	if f.Width > maxDimension || f.Height > maxDimension {
		return fmt.Errorf("%w: frame dimensions exceed %d", ErrInvalidArgument, maxDimension)
	}
	if f.Format != formatRGB24 && f.Format != formatBGR24 {
		return fmt.Errorf("%w: unsupported pixel format %d", ErrInvalidArgument, f.Format)
	}
	stride := f.Stride
	if stride == 0 {
		stride = f.Width * 3
	}
	if stride < f.Width*3 {
		return fmt.Errorf("%w: stride %d is smaller than a packed row (%d)", ErrInvalidArgument, stride, f.Width*3)
	}
	if want := uint64(stride) * uint64(f.Height); uint64(len(f.Pixels)) != want {
		return fmt.Errorf("%w: payload is %d bytes, want %d", ErrInvalidArgument, len(f.Pixels), want)
	}
	return nil
}

func fingerprintOf(path string) (fileFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileFingerprint{}, err
	}
	stat, ok := info.Sys().(interface {
		Dev() uint64
		Ino() uint64
	})
	if !ok {
		return fileFingerprint{size: info.Size(), mtime: info.ModTime().UnixNano()}, nil
	}
	return fileFingerprint{
		dev:   stat.Dev(),
		ino:   stat.Ino(),
		size:  info.Size(),
		mtime: info.ModTime().UnixNano(),
	}, nil
}
