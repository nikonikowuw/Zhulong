package algorunner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	IpcMagic                    = algoipc.IpcMagic
	IpcVersion                  = algoipc.IpcVersion
	MaxIpcPayloadSize           = algoipc.MaxIpcPayloadSize
	FailureStageBrokerConnect   = algoipc.FailureStageBrokerConnect
	runnerExitBrokerUnavailable = algoipc.RunnerExitBrokerUnavailable
	MsgResult                   = algoipc.MsgResult

	MsgGallerySync   = algoipc.MsgGallerySync
	MsgGalleryUpsert = algoipc.MsgGalleryUpsert
	MsgGalleryDelete = algoipc.MsgGalleryDelete
	MsgGalleryAck    = algoipc.MsgGalleryAck
)

var ErrIpcPayloadTooLarge = algoipc.ErrIpcPayloadTooLarge

type IpcHeader = algoipc.IpcHeader

var (
	EncodeIpcMessage = algoipc.EncodeIpcMessage
	DecodeIpcHeader  = algoipc.DecodeIpcHeader
)

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
	if p.config.AlgorithmID != "" {
		args = append(args, "--algorithm-id", p.config.AlgorithmID)
	}

	cmd := exec.Command(p.config.RunnerPath, args...)

	// Run the runner in its own process group. Stop signals the whole group, so
	// grandchildren the runner spawns cannot leak while holding the broker socket
	// or shared memory after the runner itself has exited.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Load and inject .env variables. A missing file is optional (LoadDotEnv
	// turns os.IsNotExist into an empty map + nil error), so any error here is a
	// real parse failure (e.g. a line longer than bufio.Scanner's 64KB token
	// limit). Do not swallow it: log a Warn and continue with the variables that
	// were parsed, so a broken .env is observable without blocking the runner.
	envMap := make(map[string]string)
	if p.config.EnvFile != "" {
		loaded, err := LoadDotEnv(p.config.EnvFile)
		if err != nil {
			p.logger.Warn("failed to load runner dotenv file",
				zap.String("path", p.config.EnvFile), zap.Error(err))
		}
		for k, v := range loaded {
			envMap[k] = v
		}
	}
	for k, v := range p.config.ExtraEnv {
		envMap[k] = v
	}

	cmd.Env = os.Environ()
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create pipes manually so cmd.Wait() does not close them prematurely while scanning.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("start runner process: %w", err)
	}

	// Close parent's write ends immediately so EOF propagates when child terminates
	stdoutW.Close()
	stderrW.Close()

	p.cmd = cmd
	p.startTime = time.Now()
	p.state = StatusRunning

	var logWg sync.WaitGroup
	logWg.Add(2)
	go func() {
		defer logWg.Done()
		p.streamLogs(stdoutR, zap.DebugLevel, false)
		stdoutR.Close()
	}()
	go func() {
		defer logWg.Done()
		p.streamLogs(stderrR, zap.WarnLevel, true)
		stderrR.Close()
	}()

	// Watcher goroutine
	go p.watchExit(cmd, stdoutR, stderrR, &logWg)

	return nil
}

func (p *RunnerProcess) streamLogs(r *os.File, level zapcore.Level, isStderr bool) {
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

func (p *RunnerProcess) watchExit(cmd *exec.Cmd, stdoutR, stderrR *os.File, logWg *sync.WaitGroup) {
	err := cmd.Wait()

	// Wait for pipe readers to consume all remaining buffered logs.
	// If an orphaned child process keeps the write pipe open, unblock after 100ms.
	logDone := make(chan struct{})
	go func() {
		logWg.Wait()
		close(logDone)
	}()

	select {
	case <-logDone:
	case <-time.After(100 * time.Millisecond):
		stdoutR.Close()
		stderrR.Close()
		<-logDone
	}

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

// signalRunner sends sig to the runner's process group. The runner is started
// with Setpgid (see Start), so its pgid equals its pid and the negative-pid
// form reaches every descendant. If the process group is unavailable (for
// example the process is already gone, reported as ESRCH), the call is treated
// as a no-op; any other error falls back to signalling the process directly.
//
// cmd is passed in rather than read from p.cmd so callers keep using the same
// lock-captured handle they already validated, instead of re-reading a field
// that Start owns under p.mu.
func (p *RunnerProcess) signalRunner(cmd *exec.Cmd, sig syscall.Signal) error {
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return cmd.Process.Signal(sig)
	}
	return nil
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

	// 1. Send SIGTERM to the whole process group.
	if err := p.signalRunner(cmd, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		p.logger.Debug("signal SIGTERM to runner process", zap.Error(err))
	}

	termTimer := time.NewTimer(timeout)
	defer termTimer.Stop()

	select {
	case <-doneCh:
		// Clean exit
		return nil
	case <-termTimer.C:
		// 2. Timeout: Force kill the whole process group with SIGKILL.
		p.logger.Warn("runner process did not exit within timeout, killing with SIGKILL",
			zap.Int("pid", cmd.Process.Pid),
		)
		if err := p.signalRunner(cmd, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
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

// GallerySyncPayload represents the legacy data payload for MSG_GALLERY_SYNC.
type GallerySyncPayload struct {
	Items []GallerySyncItem `json:"items"`
}

// GallerySyncShardPayload represents a sharded MSG_GALLERY_SYNC payload safely bounded within 64KB.
type GallerySyncShardPayload struct {
	SyncID     string            `json:"sync_id"`
	ShardIndex int               `json:"shard_index"`
	ShardTotal int               `json:"shard_total"`
	Items      []GallerySyncItem `json:"items"`
}

// GalleryAckPayload represents MSG_GALLERY_ACK.
type GalleryAckPayload struct {
	SyncID     string `json:"sync_id"`
	Success    bool   `json:"success"`
	TotalFaces int    `json:"total_faces"`
	ErrorMsg   string `json:"error_msg,omitempty"`
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

// shardBudget caps a single gallery sync shard at 90% of the protocol limit so
// the shard header and marshalling drift cannot push a payload past
// MaxIpcPayloadSize. Measured sizes: a 512-dim L2-normalized float32 embedding
// serializes to ~12.3 bytes per component (~6.3KB per item), so the old fixed
// 10-items-per-shard packing left under 1% headroom on real data (10 items
// measured at 64204~65373 bytes against the 65536 limit).
const shardBudget = MaxIpcPayloadSize * 9 / 10

// CreateGallerySyncShards fragments gallery items into a sequence of JSON
// payloads, each strictly within shardBudget (and therefore well under the 64KB
// IPC limit).
//
// Items are packed by measured byte size, not by a fixed count: a shard is
// closed as soon as adding the next item would exceed the budget. An empty
// input still yields exactly one shard with no items (preserved for existing
// callers). If a single item cannot fit on its own, an error naming that item's
// face_id is returned rather than silently dropping it.
func CreateGallerySyncShards(syncID string, items []GallerySyncItem) ([][]byte, error) {
	if len(items) == 0 {
		payload, err := json.Marshal(GallerySyncShardPayload{
			SyncID:     syncID,
			ShardIndex: 0,
			ShardTotal: 1,
			Items:      []GallerySyncItem{},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal empty gallery sync shard: %w", err)
		}
		return [][]byte{payload}, nil
	}

	// Marshal each item exactly once. The JSON encoding of an item is identical
	// whether it is serialized alone or inside a shard's items array, so these
	// measured sizes can be summed arithmetically instead of re-marshalling the
	// whole shard on every packing step (which would be O(n^2) on 10k faces).
	itemSizes := make([]int, len(items))
	for i, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("marshal gallery sync item %s: %w", item.FaceID, err)
		}
		itemSizes[i] = len(encoded)
	}

	// Measure the per-shard overhead with the widest header we can produce
	// (6-digit shard numbers for both index and total) so the budget stays valid
	// for any shard count.
	overheadPayload, err := json.Marshal(GallerySyncShardPayload{
		SyncID:     syncID,
		ShardIndex: 999999,
		ShardTotal: 999999,
		Items:      []GallerySyncItem{},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery sync shard header: %w", err)
	}
	overhead := len(overheadPayload)

	// Greedy packing: accumulate items while the projected shard size stays
	// within budget. Each additional item costs its own bytes plus one comma.
	type shardRange struct{ start, end int }
	var ranges []shardRange
	start := 0
	used := 0
	for i, size := range itemSizes {
		if overhead+size > shardBudget {
			return nil, fmt.Errorf("gallery sync item %s exceeds the shard budget (%d bytes > %d)",
				items[i].FaceID, overhead+size, shardBudget)
		}
		additional := size
		if i > start {
			additional++ // comma separating this item from the previous one
		}
		if used+additional > shardBudget-overhead {
			ranges = append(ranges, shardRange{start: start, end: i})
			start = i
			used = size
			continue
		}
		used += additional
	}
	ranges = append(ranges, shardRange{start: start, end: len(items)})

	shardTotal := len(ranges)
	shards := make([][]byte, 0, shardTotal)
	for i, r := range ranges {
		payload, err := json.Marshal(GallerySyncShardPayload{
			SyncID:     syncID,
			ShardIndex: i,
			ShardTotal: shardTotal,
			Items:      items[r.start:r.end],
		})
		if err != nil {
			return nil, fmt.Errorf("marshal gallery sync shard %d/%d: %w", i, shardTotal, err)
		}
		// Defensive gate: the packing math above should make this unreachable, but
		// it is the last line of defence before the 64KB protocol limit.
		if len(payload) > int(MaxIpcPayloadSize) {
			return nil, fmt.Errorf("gallery sync shard %d exceeds 64KB (%d bytes)", i, len(payload))
		}
		shards = append(shards, payload)
	}
	return shards, nil
}

// CreateGallerySyncMsg serializes a full gallery sync message into framed IPC bytes.
func CreateGallerySyncMsg(seq uint32, items []GallerySyncItem) ([]byte, error) {
	payload, err := json.Marshal(GallerySyncPayload{Items: items})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery sync payload: %w", err)
	}
	return EncodeIpcMessage(MsgGallerySync, seq, payload)
}

// CreateGalleryUpsertMsg serializes an incremental face enrollment update into framed IPC bytes.
func CreateGalleryUpsertMsg(seq uint32, item GalleryUpsertPayload) ([]byte, error) {
	payload, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("marshal gallery upsert payload: %w", err)
	}
	return EncodeIpcMessage(MsgGalleryUpsert, seq, payload)
}

// CreateGalleryDeleteMsg serializes a delete face/person update into framed IPC bytes.
func CreateGalleryDeleteMsg(seq uint32, faceID, personID string) ([]byte, error) {
	payload, err := json.Marshal(GalleryDeletePayload{FaceID: faceID, PersonID: personID})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery delete payload: %w", err)
	}
	return EncodeIpcMessage(MsgGalleryDelete, seq, payload)
}

// GalleryBroadcaster distributes full and incremental gallery updates to running algorithm processes.
type GalleryBroadcaster struct {
	broadcaster AIMessageBroadcaster
	logger      *zap.Logger
}

// NewGalleryBroadcaster creates a GalleryBroadcaster instance.
func NewGalleryBroadcaster(broadcaster AIMessageBroadcaster, logger *zap.Logger) *GalleryBroadcaster {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GalleryBroadcaster{
		broadcaster: broadcaster,
		logger:      logger.Named("gallery_broadcaster"),
	}
}

// BroadcastSync performs cold-start or full synchronization of the face gallery to all active runners.
func (b *GalleryBroadcaster) BroadcastSync(ctx context.Context, syncID string, items []GallerySyncItem) error {
	if b.broadcaster == nil {
		return nil
	}
	shards, err := CreateGallerySyncShards(syncID, items)
	if err != nil {
		return fmt.Errorf("create gallery sync shards: %w", err)
	}
	for i, shard := range shards {
		if err := b.broadcaster.BroadcastAIMessage(ctx, 0, MsgGallerySync, shard); err != nil {
			return fmt.Errorf("broadcast sync shard %d/%d: %w", i, len(shards), err)
		}
	}
	return nil
}

// BroadcastUpsert broadcasts an incremental face template enrollment or update.
func (b *GalleryBroadcaster) BroadcastUpsert(ctx context.Context, item GalleryUpsertPayload) error {
	if b.broadcaster == nil {
		return nil
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal gallery upsert: %w", err)
	}
	return b.broadcaster.BroadcastAIMessage(ctx, 0, MsgGalleryUpsert, payload)
}

// BroadcastDelete broadcasts face or person deletion.
func (b *GalleryBroadcaster) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	if b.broadcaster == nil {
		return nil
	}
	payload, err := json.Marshal(GalleryDeletePayload{FaceID: faceID, PersonID: personID})
	if err != nil {
		return fmt.Errorf("marshal gallery delete: %w", err)
	}
	return b.broadcaster.BroadcastAIMessage(ctx, 0, MsgGalleryDelete, payload)
}
