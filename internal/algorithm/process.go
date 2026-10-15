package algorithm

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// IpcMagic matches native IPC_MAGIC ('ZHLG' = 0x5A484C47).
	IpcMagic uint32 = 0x5A484C47
	// IpcVersion matches native IPC_VERSION = 1.
	IpcVersion uint16 = 1

	// MaxIpcPayloadSize mirrors native ipc_protocol.hpp's MAX_IPC_PAYLOAD_SIZE.
	// Both the send path (EncodeIpcMessage) and the receive path (DecodeIpcHeader)
	// must reject payloads beyond this limit. Cross-checked against the native
	// header by ipc_contract_test.go.
	MaxIpcPayloadSize = 64 * 1024

	// FailureStageBrokerConnect marks a runner that exited because it could not
	// connect to at least one frame broker socket. The runner emits this on
	// stderr as {"status":"failed","stage":"connect","index":N,"path":...,"error":...}
	// and exits with code 5 (see .trellis/spec/native/algo-plugin-contract.md §5.2).
	// Under multiple --broker arguments, any single path failing fails the whole
	// runner (D6), so this stage still means "the broker path list is not usable".
	FailureStageBrokerConnect = "connect"

	// runnerExitBrokerUnavailable is the runner's exit code for a failed broker
	// connect. Used as the secondary detection channel because the stderr marker
	// is the more robust primary signal.
	runnerExitBrokerUnavailable = 5

	// MsgResult is the inference result egress message type (Runner -> Broker/Go).
	MsgResult uint16 = 7

	// Gallery IPC Message Types.
	//
	// ⚠️ NOT ALIGNED: native ipc_protocol.hpp:14-20 currently stops at
	// MSG_DISCONNECT=6 and does NOT define 10/11/12. These values are a Go-side
	// reservation only. Any task implementing the gallery hot-update channel
	// (R6) MUST first add matching MSG_GALLERY_* entries to the native header.
	// The numeric values are intentionally kept stable.
	MsgGallerySync   uint16 = 10
	MsgGalleryUpsert uint16 = 11
	MsgGalleryDelete uint16 = 12
)

// ErrIpcPayloadTooLarge is returned when a payload exceeds MaxIpcPayloadSize.
var ErrIpcPayloadTooLarge = errors.New("IPC payload exceeds the 64KB protocol limit")

// IpcHeader models the 16-byte fixed-size IPC header.
type IpcHeader struct {
	Magic       uint32
	MsgType     uint16
	Version     uint16
	PayloadSize uint32
	Sequence    uint32
}

// EncodeIpcMessage frames a payload with a 16-byte binary header.
// It returns ErrIpcPayloadTooLarge if the payload exceeds MaxIpcPayloadSize
// (mirrors native ipc_protocol.hpp MAX_IPC_PAYLOAD_SIZE enforcement).
func EncodeIpcMessage(msgType uint16, seq uint32, payload []byte) ([]byte, error) {
	if len(payload) > MaxIpcPayloadSize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrIpcPayloadTooLarge, len(payload))
	}
	buf := make([]byte, 16+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], msgType)
	binary.LittleEndian.PutUint16(buf[6:8], IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[12:16], seq)
	if len(payload) > 0 {
		copy(buf[16:], payload)
	}
	return buf, nil
}

// DecodeIpcHeader parses a 16-byte header from a byte slice.
func DecodeIpcHeader(data []byte) (*IpcHeader, error) {
	if len(data) < 16 {
		return nil, errors.New("insufficient data for IPC header")
	}
	hdr := &IpcHeader{
		Magic:       binary.LittleEndian.Uint32(data[0:4]),
		MsgType:     binary.LittleEndian.Uint16(data[4:6]),
		Version:     binary.LittleEndian.Uint16(data[6:8]),
		PayloadSize: binary.LittleEndian.Uint32(data[8:12]),
		Sequence:    binary.LittleEndian.Uint32(data[12:16]),
	}
	if hdr.Magic != IpcMagic {
		return nil, fmt.Errorf("invalid IPC magic 0x%X (expected 0x%X)", hdr.Magic, IpcMagic)
	}
	if hdr.PayloadSize > MaxIpcPayloadSize {
		return nil, fmt.Errorf("%w: header declares %d bytes", ErrIpcPayloadTooLarge, hdr.PayloadSize)
	}
	return hdr, nil
}

// helper for endianness
func (h *IpcHeader) validate() bool {
	return h.Magic == IpcMagic
}

// LoadDotEnv reads key-value environment variable pairs from a .env file.
func LoadDotEnv(filePath string) (map[string]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	defer file.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if key != "" {
				env[key] = val
			}
		}
	}
	return env, scanner.Err()
}

// ProcessConfig defines options for launching a runner subprocess.
type ProcessConfig struct {
	AlgorithmID   string
	AlgorithmName string
	RunnerPath    string
	PluginPath    string
	BrokerSocks   []string
	EnvFile       string
	ExtraEnv      map[string]string
	Logger        *zap.Logger
}

// RunnerProcess supervises an individual zhulong_algo_runner subprocess.
type RunnerProcess struct {
	config       ProcessConfig
	logger       *zap.Logger
	mu           sync.Mutex
	cmd          *exec.Cmd
	state        string
	doneCh       chan struct{}
	exitErr      error
	exitCode     int
	failureStage string
	startTime    time.Time
}

// NewRunnerProcess creates a new RunnerProcess instance.
func NewRunnerProcess(cfg ProcessConfig) *RunnerProcess {
	l := cfg.Logger
	if l == nil {
		l = zap.NewNop()
	}
	return &RunnerProcess{
		config: cfg,
		logger: l.With(zap.String("algorithm", cfg.AlgorithmName)),
		state:  StatusDisabled,
	}
}

// Start launches the runner subprocess with environment injection and output logging.
func (p *RunnerProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == StatusRunning || p.state == StatusStarting {
		return errors.New("process is already running")
	}

	p.state = StatusStarting
	p.doneCh = make(chan struct{})
	p.exitErr = nil
	p.exitCode = 0
	p.failureStage = ""

	args := []string{
		"--plugin", p.config.PluginPath,
	}
	if len(p.config.BrokerSocks) > 0 {
		for _, sock := range p.config.BrokerSocks {
			if sock != "" {
				args = append(args, "--broker", sock)
			}
		}
	}
	if p.config.AlgorithmName != "" {
		args = append(args, "--name", p.config.AlgorithmName)
	}

	cmd := exec.Command(p.config.RunnerPath, args...)

	// Load and inject .env variables
	envMap := make(map[string]string)
	if p.config.EnvFile != "" {
		if loaded, err := LoadDotEnv(p.config.EnvFile); err == nil {
			for k, v := range loaded {
				envMap[k] = v
			}
		}
	}
	for k, v := range p.config.ExtraEnv {
		envMap[k] = v
	}

	cmd.Env = os.Environ()
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture stdout
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("create stdout pipe: %w", err)
	}

	// Capture stderr
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("start runner process: %w", err)
	}

	p.cmd = cmd
	p.startTime = time.Now()
	p.state = StatusRunning

	// Drain stdout asynchronously
	go p.streamLogs(stdoutPipe, zap.DebugLevel, false)
	// Drain stderr asynchronously (source of the broker-connect marker)
	go p.streamLogs(stderrPipe, zap.WarnLevel, true)

	// Watcher goroutine
	go p.watchExit(cmd)

	return nil
}

func (p *RunnerProcess) streamLogs(r io.Reader, level zapcore.Level, isStderr bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		text := scanner.Text()
		p.logger.Log(level, "runner log", zap.String("text", text))
		// Primary failure-stage channel: the runner reports a broker connect
		// failure on stderr as {"stage":"connect"}.
		if isStderr && strings.Contains(text, `"stage":"connect"`) {
			p.mu.Lock()
			p.failureStage = FailureStageBrokerConnect
			p.mu.Unlock()
		}
	}
}

func (p *RunnerProcess) watchExit(cmd *exec.Cmd) {
	err := cmd.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	p.exitErr = err
	if cmd.ProcessState != nil {
		p.exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		p.exitCode = -1
	}

	// Secondary failure-stage channel: exit code 5 means the runner could not
	// connect to the broker. Robustness note: the stderr marker above is the
	// primary signal because exit codes are more fragile to native changes.
	if p.exitCode == runnerExitBrokerUnavailable {
		p.failureStage = FailureStageBrokerConnect
	}

	if p.state == StatusRunning {
		p.state = StatusCrashed

		if p.failureStage == FailureStageBrokerConnect {
			// The runner could not connect to every broker socket it was given.
			// This is a distinguishable signal, NOT an ordinary plugin crash. We do
			// not fake success or change watchdog restart/circuit-breaker behaviour.
			p.logger.Error("runner exited because it could not connect to its frame broker sockets",
				zap.Int("exit_code", p.exitCode),
				zap.Strings("broker_socks", p.config.BrokerSocks),
				zap.String("reason", "broker_unavailable"),
				zap.String("algorithm", p.config.AlgorithmID),
			)
		} else {
			p.logger.Warn("runner process exited unexpectedly",
				zap.Int("exit_code", p.exitCode),
				zap.Error(err),
			)
		}
	}

	close(p.doneCh)
}

// Stop gracefully shuts down the process: SIGTERM -> wait up to timeout -> SIGKILL.
func (p *RunnerProcess) Stop(timeout time.Duration) error {
	p.mu.Lock()
	cmd := p.cmd
	state := p.state
	doneCh := p.doneCh
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil || state == StatusDisabled {
		return nil
	}

	// Mark target state as disabled to prevent treating clean stop as crash
	p.mu.Lock()
	p.state = StatusDisabled
	p.mu.Unlock()

	// 1. Send SIGTERM
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		p.logger.Debug("signal SIGTERM to runner process", zap.Error(err))
	}

	termTimer := time.NewTimer(timeout)
	defer termTimer.Stop()

	select {
	case <-doneCh:
		// Clean exit
		return nil
	case <-termTimer.C:
		// 2. Timeout: Force kill with SIGKILL
		p.logger.Warn("runner process did not exit within timeout, killing with SIGKILL",
			zap.Int("pid", cmd.Process.Pid),
		)
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			p.logger.Warn("kill runner process with SIGKILL", zap.Error(err))
		}
		killTimer := time.NewTimer(timeout)
		defer killTimer.Stop()
		select {
		case <-doneCh:
			return nil
		case <-killTimer.C:
			p.logger.Error("runner process did not exit after SIGKILL timeout", zap.Int("pid", cmd.Process.Pid))
			return errors.New("runner process failed to terminate after SIGKILL")
		}
	}
}

// Done returns a channel that is closed when the runner process terminates.
func (p *RunnerProcess) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doneCh
}

// State returns the current status of the process.
func (p *RunnerProcess) State() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// PID returns the OS process ID, or 0 if not running.
func (p *RunnerProcess) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// RunningDuration returns how long the current process has been alive.
func (p *RunnerProcess) RunningDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StatusRunning {
		return time.Since(p.startTime)
	}
	return 0
}

// GallerySyncPayload represents the data payload for MSG_GALLERY_SYNC.
type GallerySyncPayload struct {
	Items []GallerySyncItem `json:"items"`
}

// GallerySyncItem represents one registered face item in MSG_GALLERY_SYNC.
type GallerySyncItem struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}

// GalleryUpsertPayload represents MSG_GALLERY_UPSERT.
type GalleryUpsertPayload struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}

// GalleryDeletePayload represents MSG_GALLERY_DELETE.
type GalleryDeletePayload struct {
	FaceID   string `json:"face_id,omitempty"`
	PersonID string `json:"person_id,omitempty"`
}

// CreateGallerySyncMsg serializes a full gallery sync message into framed IPC bytes.
//
// ⚠️ NOT WIRED: no caller exists. The gallery hot-update channel (transport,
// ACK semantics, and sharding) is not implemented — see
// .trellis/tasks/10-12-algo-lifecycle-remediation/prd.md R6. Native has no
// MSG_GALLERY_* message types yet (ipc_protocol.hpp).
func CreateGallerySyncMsg(seq uint32, items []GallerySyncItem) ([]byte, error) {
	payload, err := json.Marshal(GallerySyncPayload{Items: items})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery sync payload: %w", err)
	}
	return EncodeIpcMessage(MsgGallerySync, seq, payload)
}

// CreateGalleryUpsertMsg serializes an incremental face enrollment update into framed IPC bytes.
//
// ⚠️ NOT WIRED: no caller exists. The gallery hot-update channel (transport,
// ACK semantics, and sharding) is not implemented — see
// .trellis/tasks/10-12-algo-lifecycle-remediation/prd.md R6. Native has no
// MSG_GALLERY_* message types yet (ipc_protocol.hpp).
func CreateGalleryUpsertMsg(seq uint32, item GalleryUpsertPayload) ([]byte, error) {
	payload, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("marshal gallery upsert payload: %w", err)
	}
	return EncodeIpcMessage(MsgGalleryUpsert, seq, payload)
}

// CreateGalleryDeleteMsg serializes a delete face/person update into framed IPC bytes.
//
// ⚠️ NOT WIRED: no caller exists. The gallery hot-update channel (transport,
// ACK semantics, and sharding) is not implemented — see
// .trellis/tasks/10-12-algo-lifecycle-remediation/prd.md R6. Native has no
// MSG_GALLERY_* message types yet (ipc_protocol.hpp).
func CreateGalleryDeleteMsg(seq uint32, faceID, personID string) ([]byte, error) {
	payload, err := json.Marshal(GalleryDeletePayload{FaceID: faceID, PersonID: personID})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery delete payload: %w", err)
	}
	return EncodeIpcMessage(MsgGalleryDelete, seq, payload)
}
