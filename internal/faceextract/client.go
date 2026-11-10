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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	// ErrAmbiguous means the frame was valid and faces were found, but more than one
	// could be the subject (a group photo). Enrollment only accepts a single portrait,
	// so the provider refuses to guess: the caller must answer 422 and have a human
	// pick the right photo. Enrolling the top-scoring face of a group shot writes the
	// wrong person into the gallery, and no later verification can tell.
	ErrAmbiguous = errors.New("multiple candidate faces in the supplied image")
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
	// ErrEntry is returned when the provider package has no runnable entry. It wraps
	// the underlying resolution error (algorunner.ErrMissingEntry / ErrLegacyEntry /
	// ErrInvalidEntry) so callers can distinguish "not installed yet" from "broken".
	ErrEntry = errors.New("provider package entry is unavailable")
)

// Internal verdicts. They never cross the package boundary — callers only ever see
// ErrInternal — but they decide whether the child process is replaced.
var (
	// errProtocolDesync means the byte stream can no longer be trusted: a failed
	// write, a failed or timed-out read, a non-JSON line, a mismatched request_id, or
	// a feature dimension the host cannot store. The child must be replaced, because a
	// half-written frame or an unread reply would poison the next request.
	//
	// A frame-level verdict (no face, invalid input) is *not* a desync: the child is
	// healthy and merely reported that this photo has no usable face. Killing it there
	// would make the next frame pay another cold start (spec §6.1: a bad photo must not
	// destroy the hot process).
	errProtocolDesync = errors.New("provider protocol desync")

	// errCallerCancelled means the caller went away mid-round-trip. It is never
	// retried: the replacement child would be killed the instant it started.
	errCallerCancelled = errors.New("caller cancelled the request")

	errDeadlineExceeded = errors.New("read deadline exceeded")
	errReplyTooLong     = errors.New("provider reply line exceeds the protocol limit")
)

// Pixel formats accepted by the provider protocol. The numbers are part of the wire
// contract (the package's C ABI uses the same values), so they are declared next to
// Frame and stay the single source of truth for both the client and the decoder.
const (
	FormatBGR24 uint32 = 1
	FormatRGB24 uint32 = 2
)

// Defaults for Options.
const (
	DefaultRequestTimeout = 30 * time.Second
	DefaultIdleTimeout    = 60 * time.Second
	// IdleTimeoutEnv lets operators keep the historical knob. The retired loader
	// read the same variable, so existing deployments keep their memory profile.
	IdleTimeoutEnv = "ZHULONG_FACE_PLUGIN_IDLE_TIMEOUT_SEC"
)

// Protocol constants. They are part of the SDK contract (see algo-plugin-contract
// §7), so they are named once here and asserted by tests rather than inlined.
const (
	frameMagic    uint32 = 0x45464C5A // "ZLFE" little-endian
	headerBytes          = 28
	maxDimension  uint32 = 16384
	maxFeatureDim        = 512

	// maxReplyLineBytes caps one stdout line. 512 floats are a few KiB of JSON; the cap
	// exists so a runaway package cannot exhaust host memory with a single line, not to
	// express a model requirement.
	maxReplyLineBytes = 256 << 10

	// extractAttempts bounds the retry: the first failure may be a crash, the second is
	// final.
	extractAttempts = 2

	// reapTimeout bounds the wait for a killed child. SIGKILL cannot be blocked, so this
	// only fires if the kill itself never landed — and then it must not be allowed to
	// wedge the client while it holds the request mutex.
	reapTimeout = 10 * time.Second
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
	Format uint32 // FormatBGR24 or FormatRGB24
	Pixels []byte
}

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
	closed   bool // set by Close; a closed client must not spawn an unreaped child

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

	if c.closed {
		// Spawning here would leak a child that no reaper is left to retire.
		return nil, fmt.Errorf("%w: client is closed", ErrInternal)
	}

	var lastErr error
	for range extractAttempts {
		if err := ctx.Err(); err != nil {
			return nil, callerGone(err)
		}

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

		// Only a desynced stream needs a fresh process. A frame-level verdict means the
		// child answered correctly and is still healthy; killing it there would charge
		// the next frame another cold start.
		if errors.Is(err, errProtocolDesync) || errors.Is(err, errCallerCancelled) {
			c.discardLocked(p, "protocol desync")
		}

		// Retry once, and only for host-side failures. A cancelled caller is never
		// retried: the replacement child would be killed the moment it started.
		if !errors.Is(err, ErrInternal) || errors.Is(err, errCallerCancelled) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrInternal, lastErr)
}

// Close retires the provider process and stops the idle reaper. It is safe to call
// twice and safe to call while a request is in flight (the request finishes first).
// After Close, Extract fails instead of spawning a child that nothing would reclaim.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.reaperStop)
		<-c.reaperDone
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
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

	if p := c.reuseProcessLocked(entry, finger); p != nil {
		return p, nil
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

// reuseProcessLocked returns c.proc if it is still alive and matches the current
// executable; otherwise it discards any stale or exited process and returns nil.
func (c *Client) reuseProcessLocked(entry string, finger fileFingerprint) *proc {
	if c.proc == nil {
		return nil
	}
	if c.proc.entry != entry || c.proc.finger != finger {
		c.discardLocked(c.proc, "provider executable changed")
		return nil
	}
	select {
	case <-c.proc.cmd.Done():
		// Exited on its own: replace it, but report nothing — the retry logic
		// in Extract decides whether the frame was answered.
		c.discardLocked(c.proc, "provider process exited")
		return nil
	default:
		return c.proc
	}
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
		// Nothing owns stdin yet: without this close the fd outlives the spawn attempt.
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: stdout pipe: %v", ErrInternal, err)
	}
	// stderr is inherited on purpose: package logs go to the host's stderr already
	// (the skeleton's log writer targets stderr), and swallowing them would hide the
	// only diagnostic a third-party package can produce.
	cmd.Stderr = os.Stderr

	pinned, err := algorunner.StartPinned(cmd)
	if err != nil {
		return nil, startFailure(err)
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

	started := time.Now()
	if err := writeFrame(p.stdin, requestID, f); err != nil {
		return nil, roundTripFailure(ctx, "write frame", err)
	}

	line, err := readLineBefore(p.stdout, deadline)
	if err != nil {
		if errors.Is(err, errDeadlineExceeded) {
			// A hung child still holds the model; only a group kill frees it.
			_ = p.cmd.KillGroup()
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The budget came from the caller going away, not from the child stalling.
				return nil, callerGone(ctxErr)
			}
			// Report the wait that actually elapsed: the budget may have come from the
			// caller's context, not from RequestTimeout.
			return nil, fmt.Errorf(
				"%w: %w: provider did not answer within %s (configured budget %s)",
				ErrInternal,
				errProtocolDesync,
				time.Since(started).Round(time.Millisecond),
				c.opts.RequestTimeout,
			)
		}
		return nil, roundTripFailure(ctx, "read reply", err)
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
	// Never signal a process that Done already reported as reaped: its pid is free for
	// reuse, so kill(-pid) would hit whatever process group inherited the number.
	select {
	case <-p.cmd.Done():
	default:
		if err := p.cmd.KillGroup(); err != nil {
			c.log.Warn("kill provider process group", zap.Int("pid", p.cmd.PID()), zap.Error(err))
		}
	}
	// SIGKILL cannot be blocked, so this normally returns at once. The timeout exists
	// so a kill that never lands cannot wedge the client forever while it holds the
	// request mutex.
	select {
	case <-p.cmd.Done():
	case <-time.After(reapTimeout):
		c.log.Error("provider process did not exit after SIGKILL", zap.Int("pid", p.cmd.PID()))
	}
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

// roundTripFailure classifies a transport-level failure. A caller that went away is
// reported as such — never retried, never blamed on the package — while every other
// transport error means the stream is no longer trustworthy.
func roundTripFailure(ctx context.Context, stage string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return callerGone(ctxErr)
	}
	return fmt.Errorf(
		"%w: %w: %s: %v",
		ErrInternal,
		errProtocolDesync,
		stage,
		err,
	)
}

// callerGone wraps the caller's cancellation. The result still satisfies
// errors.Is(err, ErrInternal), so the HTTP layer keeps its 500 contract, but it
// carries errCallerCancelled so Extract knows a retry would be pure waste.
func callerGone(err error) error {
	return fmt.Errorf(
		"%w: %w: %v",
		ErrInternal,
		errCallerCancelled,
		err,
	)
}

// readLineBefore reads one '\n'-terminated protocol line, honouring a wall-clock
// deadline.
//
// The read runs on a helper goroutine because there is no fd-level deadline to lean
// on: a bufio.Reader over an os.Pipe cannot be interrupted, and bufio.Reader has no
// SetReadDeadline. On the deadline path the goroutine stays blocked only until
// roundTrip kills the process group, which closes the pipe and releases it.
func readLineBefore(reader *bufio.Reader, deadline time.Time) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := readReplyLine(reader, maxReplyLineBytes)
		done <- result{line: line, err: err}
	}()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case r := <-done:
		// A partial line (io.EOF before '\n') is a desync, not a reply: parsing it
		// would be one byte away from trusting a truncated vector.
		if r.err != nil {
			return "", r.err
		}
		return r.line, nil
	case <-timer.C:
		return "", errDeadlineExceeded
	}
}

// readReplyLine reads one line, refusing to grow past limit: a runaway provider must
// not be able to exhaust host memory with a single unbounded line.
func readReplyLine(reader *bufio.Reader, limit int) (string, error) {
	line := make([]byte, 0, 256)
	for {
		chunk, err := reader.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > limit {
			return "", errReplyTooLong
		}
		if err == nil {
			// Trim both terminators: a provider that emits CRLF must not make us fail on
			// a stray '\r', which would be our parser's problem, not its bug.
			return string(bytes.TrimRight(line, "\r\n")), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
}

// startFailure keeps "this package cannot be run" (missing, not executable, no
// permission) apart from "the spawn failed for a transient reason" — ETXTBSY while a
// package is being replaced in place, or EAGAIN under load. Reporting the second as
// ErrUnsupported would send operators looking for a capability bug that does not
// exist: the package is fine, it is just being written to right now.
func startFailure(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist),
		errors.Is(err, fs.ErrPermission),
		errors.Is(err, syscall.ENOEXEC):
		return fmt.Errorf("%w: start provider: %v", ErrEntry, err)
	default:
		return fmt.Errorf("%w: start provider: %v", ErrInternal, err)
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

func parseReply(line string, wantRequestID uint32) ([]float32, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed[0] != '{' {
		// Anything else is either a stray log line or garbage. Treating it as a
		// desync (retryable, and the child is replaced) is the only safe reading: a
		// silent skip could pair this request with the *next* answer.
		return nil, fmt.Errorf(
			"%w: %w: provider wrote a non-JSON line on stdout: %.120q",
			ErrInternal,
			errProtocolDesync,
			trimmed,
		)
	}
	var r reply
	if err := json.Unmarshal([]byte(trimmed), &r); err != nil {
		return nil, fmt.Errorf(
			"%w: %w: decode provider reply: %v",
			ErrInternal,
			errProtocolDesync,
			err,
		)
	}
	if r.RequestID != wantRequestID {
		// Mismatched ids mean the stream is out of step; attaching these features to
		// this request would store someone else's embedding.
		return nil, fmt.Errorf(
			"%w: %w: reply request_id %d does not match request %d",
			ErrInternal,
			errProtocolDesync,
			r.RequestID,
			wantRequestID,
		)
	}
	if r.Status != "passed" {
		// A reply that echoes our id is in sync: the child answered this request. The
		// code decides what to tell the caller, never whether the child is replaced —
		// a "no face" verdict must leave the hot process alone.
		detail := r.Error
		if detail == "" {
			detail = r.Status
		}
		return nil, failureForCode(r.Code, detail)
	}
	validDim := r.FeatureDim > 0 && r.FeatureDim <= maxFeatureDim
	dimMatches := len(r.Features) == int(r.FeatureDim)
	if !validDim || !dimMatches {
		// "passed" with an unusable dimension is a fake success: the child is in sync
		// but produced something the gallery cannot store, so replace it rather than
		// trust the next answer.
		return nil, fmt.Errorf(
			"%w: %w: provider returned feature_dim=%d with %d values",
			ErrInternal,
			errProtocolDesync,
			r.FeatureDim,
			len(r.Features),
		)
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
	case "MULTIPLE_FACES":
		return fmt.Errorf("%w: %s", ErrAmbiguous, detail)
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
	if f.Format != FormatRGB24 && f.Format != FormatBGR24 {
		return fmt.Errorf("%w: unsupported pixel format %d", ErrInvalidArgument, f.Format)
	}
	stride := f.Stride
	if stride == 0 {
		stride = f.Width * 3
	}
	if stride < f.Width*3 {
		return fmt.Errorf(
			"%w: stride %d is smaller than a packed row (%d)",
			ErrInvalidArgument,
			stride,
			f.Width*3,
		)
	}
	if want := uint64(stride) * uint64(f.Height); uint64(len(f.Pixels)) != want {
		return fmt.Errorf(
			"%w: payload is %d bytes, want %d",
			ErrInvalidArgument,
			len(f.Pixels),
			want,
		)
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
