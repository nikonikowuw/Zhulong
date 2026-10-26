package algorithm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

var (
	ErrPlatformMismatch        = errors.New("algorithm platform does not match host platform")
	ErrInvalidManifest         = errors.New("invalid manifest.json")
	ErrMissingPluginFile       = errors.New("missing convention plugin.so in package root")
	ErrMissingSampleFile       = errors.New("missing convention sample.jpg in package root")
	ErrMissingManifestFile     = errors.New("missing convention manifest.json in package root")
	ErrInvalidConfigSchema     = errors.New("invalid config.schema.json: not a valid JSON schema")
	ErrVersionAlreadyInstalled = errors.New("algorithm version is already installed")
	ErrVerificationFailed      = errors.New("dynamic plugin verification failed")
	ErrResultTypeMismatch      = errors.New("plugin exported result_type does not match manifest task_type")
	ErrInvalidComputeBudget    = errors.New("invalid compute budget in manifest")

	// ErrPlatformTargetRetired marks a package whose manifest.platform.target is
	// the retired "cpu" spelling. The host target is now "onnx" (the ONNX Runtime
	// tier); such packages must be rebuilt for that target. Deliberately not
	// silently mapped: a cpu-built package carries a different runtime and assets,
	// so the failure would surface much later as an opaque loader error.
	ErrPlatformTargetRetired = errors.New("manifest.platform.target \"cpu\" is retired, rebuild the package for target \"onnx\"")

	// ErrInvalidManifestToken is returned when a manifest identity field is unsafe:
	// id/version outside the token charset, or a name exceeding the handshake
	// buffer. Because manifest.ID and manifest.Version are used to build the
	// install path via filepath.Join, an unvalidated value such as
	// "../../../tmp/pwned" would escape installDir.
	ErrInvalidManifestToken = errors.New("manifest field is invalid")
)

// manifestTokenPattern restricts the PATH-BEARING manifest identity fields
// (id/version) to a safe charset. The first character must be alphanumeric,
// which forbids leading "." / "-" / path separators and prevents ".." or
// "../" from ever forming a whole segment.
//
// manifest.Name is NOT subject to this pattern: it is a human-readable display
// field that never participates in path construction. See manifestNameMaxBytes.
var manifestTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// manifestNameMaxBytes bounds manifest.Name to the runner's handshake buffer
// (HandshakeReq.client_name[32], ipc_protocol.hpp:37). The value travels to the
// runner via --name (supervisor.go) and is copied with strncpy, which silently
// truncates on overflow — two packages could then share one client name.
const manifestNameMaxBytes = 32

// VerificationReport reflects the final JSON line emitted by zhulong_algo_runner --verify-plugin.
type VerificationReport struct {
	Status     string `json:"status"`
	Sample     string `json:"sample"`
	ResultType int    `json:"result_type"`
	Count      int    `json:"count"`
	// SampleDecoded reports whether the runner decoded the sample into a real frame.
	// False means the run fell back to a synthetic frame, so passing verification
	// proves the ABI lifecycle only — never that the packaged sample was inferred.
	SampleDecoded bool   `json:"sample_decoded"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Error         string `json:"error,omitempty"`
}

// HostPlatform identifies the machine an algorithm package must match: the CPU
// architecture plus the accelerator target from [algorithm].target. It is the Go
// counterpart of manifest.platform.
type HostPlatform struct {
	// Arch is x86_64 / aarch64, spelled as packages declare it.
	Arch string
	// Target is onnx or an accelerator family such as rk3568. Empty means onnx.
	Target string
}

// DetectHostPlatform reports the platform of the machine this process runs on,
// with the accelerator target taken from the operator configuration.
func DetectHostPlatform(target string) HostPlatform {
	return HostPlatform{Arch: detectedArch(), Target: normalizeHostTarget(target)}
}

func detectedArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return runtime.GOARCH
	}
}

// retiredHostTarget is the spelling this field used before the onnx rename.
// It is rejected at install time instead of being mapped onto onnx: a package
// built for the old cpu target carries a different runtime and different assets.
const retiredHostTarget = "cpu"

func normalizeHostTarget(target string) string {
	if target == "" {
		return "onnx"
	}
	return target
}

// DefaultVerifyTimeout bounds a single dynamic verification run of the runner.
const DefaultVerifyTimeout = 15 * time.Second

// VerifyOptions tunes verification. Zero values select the defaults.
type VerifyOptions struct {
	// Timeout bounds one dynamic verification run; <= 0 means DefaultVerifyTimeout.
	Timeout time.Duration
	// Logger receives non-fatal observations (a sample frame that could not be fed,
	// or a decoded sample that produced no results). Nil silences them.
	Logger *zap.Logger
}

// Verifier executes static contract audits and dynamic black-box verification.
//
// Immutable after construction: the host identity, the runner binary and the
// timeout are captured once, so a verdict can never depend on a field mutated
// between calls.
type Verifier struct {
	runnerPath string
	host       HostPlatform
	timeout    time.Duration
	logger     *zap.Logger
}

// NewVerifier creates a Verifier. An empty runnerPath keeps the verifier to
// static checks only; host.Target defaults to onnx.
func NewVerifier(runnerPath string, host HostPlatform, opts ...VerifyOptions) *Verifier {
	timeout := DefaultVerifyTimeout
	var logger *zap.Logger
	if len(opts) > 0 {
		if opts[0].Timeout > 0 {
			timeout = opts[0].Timeout
		}
		logger = opts[0].Logger
	}

	host.Target = normalizeHostTarget(host.Target)

	return &Verifier{
		runnerPath: runnerPath,
		host:       host,
		timeout:    timeout,
		logger:     logger,
	}
}

// warn emits a non-fatal verification observation when a logger is configured.
func (v *Verifier) warn(msg string, fields ...zap.Field) {
	if v.logger == nil {
		return
	}
	v.logger.Warn(msg, fields...)
}

// HostPlatform reports the platform this verifier compares packages against.
func (v *Verifier) HostPlatform() HostPlatform {
	return v.host
}

// VerifyDirectory verifies an extracted package directory.
// Returns the parsed Manifest, configSchemaJSON, and .env contents if any.
func (v *Verifier) VerifyDirectory(ctx context.Context, pkgDir string) (*Manifest, string, string, error) {
	manifestPath := filepath.Join(pkgDir, "manifest.json")
	pluginPath := filepath.Join(pkgDir, algorunner.EntryFileName)
	samplePath := filepath.Join(pkgDir, "sample.jpg")

	// 1. Convention files presence check
	if _, err := os.Stat(manifestPath); err != nil {
		return nil, "", "", ErrMissingManifestFile
	}
	if _, err := os.Stat(pluginPath); err != nil {
		return nil, "", "", ErrMissingPluginFile
	}
	if _, err := os.Stat(samplePath); err != nil {
		return nil, "", "", ErrMissingSampleFile
	}

	// 2. Parse and validate manifest.json
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", "", fmt.Errorf("read manifest.json: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, "", "", fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}

	if manifest.ID == "" || manifest.Version == "" || manifest.Name == "" {
		return nil, "", "", fmt.Errorf("%w: id, version, and name are required", ErrInvalidManifest)
	}

	// Reject path traversal / illegal characters in the PATH-BEARING identity
	// fields before they are ever joined into an install path (see
	// ErrInvalidManifestToken). Only ID and Version are path-bearing:
	//   targetDir = Runtime.PackageDir(manifest.ID, manifest.Version)
	// Name is deliberately NOT charset-restricted here — it is a human-readable
	// display field (e.g. "安全帽佩戴检测算法") and never participates in path
	// construction. It only needs to fit the runner's handshake buffer.
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "id", value: manifest.ID},
		{name: "version", value: manifest.Version},
	} {
		if !manifestTokenPattern.MatchString(field.value) {
			return nil, "", "", fmt.Errorf("%w: %s %q", ErrInvalidManifestToken, field.name, field.value)
		}
	}

	// Name flows to the runner as --name and is copied into HandshakeReq's
	// fixed-size client_name[32] buffer (ipc_protocol.hpp:37, runner.cpp:201),
	// so it must be byte-bounded. strncpy truncates silently, which would make
	// two packages share a client name; reject instead.
	if len(manifest.Name) > manifestNameMaxBytes {
		return nil, "", "", fmt.Errorf("%w: name exceeds %d bytes", ErrInvalidManifestToken, manifestNameMaxBytes)
	}

	// Validate task_type
	switch manifest.TaskType {
	case TaskTypeDetection, TaskTypeClassification, TaskTypePersonStructure, TaskTypeVehicleStructure:
	default:
		return nil, "", "", fmt.Errorf("%w: invalid task_type %q", ErrInvalidManifest, manifest.TaskType)
	}

	// Validate category
	switch manifest.Category {
	case CategorySafety, CategorySecurity, CategoryFire, CategoryTraffic, CategoryGeneral:
	default:
		return nil, "", "", fmt.Errorf("%w: invalid category %q", ErrInvalidManifest, manifest.Category)
	}

	// Validate resources
	if manifest.Resources.ComputeCost <= 0 || manifest.Resources.ComputeCost > 1000 {
		return nil, "", "", fmt.Errorf("%w: compute_cost must be between 1 and 1000", ErrInvalidComputeBudget)
	}
	if manifest.Resources.MemoryMB <= 0 {
		return nil, "", "", fmt.Errorf("%w: memory_mb must be positive", ErrInvalidComputeBudget)
	}

	// 3. Stage 1: Static platform compatibility gate
	//
	// The retired "cpu" target is rejected before any comparison: silently
	// treating it as "onnx" would install a package whose assets and runtime do
	// not match the host, and the failure would surface much later as an opaque
	// loader/exec error.
	if manifest.Platform.Target == retiredHostTarget {
		return nil, "", "", fmt.Errorf("%w: package %s %s was built for platform.target=%q",
			ErrPlatformTargetRetired, manifest.ID, manifest.Version, retiredHostTarget)
	}
	if manifest.Platform.Arch != "" && manifest.Platform.Arch != v.host.Arch {
		return nil, "", "", fmt.Errorf("%w: package requires arch %q, host is %q", ErrPlatformMismatch, manifest.Platform.Arch, v.host.Arch)
	}
	// An empty target on either side means "unspecified", not "mismatch": the
	// package may not pin a device, and the host may not know its own.
	bothTargetsKnown := manifest.Platform.Target != "" && v.host.Target != ""
	if bothTargetsKnown && manifest.Platform.Target != v.host.Target {
		return nil, "", "", fmt.Errorf("%w: package requires target %q, host is %q", ErrPlatformMismatch, manifest.Platform.Target, v.host.Target)
	}

	// Optional config.schema.json validation
	configSchemaJSON := "{}"
	schemaPath := filepath.Join(pkgDir, "config.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err == nil {
		configSchemaJSON = string(schemaBytes)
		if _, err := compileConfigSchema(configSchemaJSON); err != nil {
			return nil, "", "", fmt.Errorf("%w: %v", ErrInvalidConfigSchema, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, "", "", fmt.Errorf("read config.schema.json: %w", err)
	}

	// Optional .env
	var envContent string
	envPath := filepath.Join(pkgDir, algorunner.EnvFileName)
	if b, err := os.ReadFile(envPath); err == nil {
		envContent = string(b)
	}

	// 4. Stage 2: Dynamic black-box verification via runner
	if v.runnerPath != "" {
		if err := v.runDynamicVerification(ctx, pluginPath, samplePath, manifest.TaskType); err != nil {
			return nil, "", "", err
		}
	}

	return &manifest, configSchemaJSON, envContent, nil
}

func (v *Verifier) runDynamicVerification(ctx context.Context, pluginPath, samplePath, taskType string) error {
	vCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	// 样本帧：Go 用标准库把 sample.jpg/png 解码成 PPM 交给 runner。runner 不链接任何
	// 图像解码器（它不属于推理链路），所以「真的把包里的样本图喂进模型」这件事由调用侧
	// 负责。解码失败不阻断安装（老包可能用了标准库不支持的编码），但会让 runner 回退
	// 合成帧，并在报告里标记 sample_decoded=false，再以告警显式暴露给运维。
	framePath, cleanup := writeSampleFramePPM(samplePath)
	if cleanup != nil {
		defer cleanup()
	}
	sampleArg := samplePath
	if framePath != "" {
		sampleArg = framePath
	}

	cmd := exec.CommandContext(vCtx, v.runnerPath, "--verify-plugin", pluginPath, "--sample", sampleArg)
	// 以包目录为 CWD：验包必须复现部署布局（plugin.so 与 models/ 同级）。
	// 否则「从源码树 CWD 意外命中权重」会让模型路径类缺陷（拼出 models/models/…）
	// 在开发机上全部逃过准入，到部署环境才以 model_create 失败爆出。
	cmd.Dir = filepath.Dir(pluginPath)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: execution failed (%v): %s", ErrVerificationFailed, err, stderrBuf.String())
	}

	// Parse the final JSON line
	output := strings.TrimSpace(stdoutBuf.String())
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return fmt.Errorf("%w: empty output from runner", ErrVerificationFailed)
	}

	lastLine := strings.TrimSpace(lines[len(lines)-1])
	var report VerificationReport
	if err := json.Unmarshal([]byte(lastLine), &report); err != nil {
		return fmt.Errorf("%w: cannot parse runner report (%s): %v", ErrVerificationFailed, lastLine, err)
	}

	if report.Status != "passed" {
		return fmt.Errorf("%w: runner reported status %q: %s", ErrVerificationFailed, report.Status, report.Error)
	}

	// 准入结论的可信度取决于「样本帧是否真的进了模型」：没解码成功就只能证明 ABI 生命周期。
	if !report.SampleDecoded {
		v.warn("algorithm verification ran without a decoded sample frame; only the ABI lifecycle was exercised",
			zap.String("sample", samplePath),
			zap.String("plugin", pluginPath))
	} else if report.Count == 0 {
		v.warn("algorithm verification decoded the sample frame but the plugin reported zero results",
			zap.String("sample", samplePath),
			zap.Int("width", report.Width),
			zap.Int("height", report.Height))
	}

	// Cross-check result_type against manifest task_type:
	switch taskType {
	case TaskTypeDetection:
		if report.ResultType != int(ResultTypeDetection) {
			return fmt.Errorf("%w: expected result_type %d for %s, got %d", ErrResultTypeMismatch, ResultTypeDetection, taskType, report.ResultType)
		}
	case TaskTypeClassification:
		if report.ResultType != int(ResultTypeClassification) {
			return fmt.Errorf("%w: expected result_type %d for %s, got %d", ErrResultTypeMismatch, ResultTypeClassification, taskType, report.ResultType)
		}
	case TaskTypePersonStructure:
		if report.ResultType != int(ResultTypePersonStructure) {
			return fmt.Errorf("%w: expected result_type %d for %s, got %d", ErrResultTypeMismatch, ResultTypePersonStructure, taskType, report.ResultType)
		}
	case TaskTypeVehicleStructure:
		if report.ResultType != int(ResultTypeVehicleStructure) {
			return fmt.Errorf("%w: expected result_type %d for %s, got %d", ErrResultTypeMismatch, ResultTypeVehicleStructure, taskType, report.ResultType)
		}
	}

	return nil
}

// StageAndVerify extracts an archive into a temporary folder, verifies it,
// and atomically publishes the immutable version directory under finalInstallDir.
func (v *Verifier) StageAndVerify(ctx context.Context, archivePath, finalInstallDir string) (algo *Algorithm, retErr error) {
	if err := os.MkdirAll(finalInstallDir, 0o755); err != nil {
		return nil, fmt.Errorf("create install directory: %w", err)
	}
	tempExtractDir, err := os.MkdirTemp(finalInstallDir, ".zhulong_verify_*")
	if err != nil {
		return nil, fmt.Errorf("create temp extraction directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tempExtractDir); err != nil && retErr == nil {
			retErr = fmt.Errorf("remove verification directory: %w", err)
		}
	}()

	if err := ExtractArchive(archivePath, tempExtractDir); err != nil {
		return nil, fmt.Errorf("extract archive: %w", err)
	}

	manifest, configSchemaJSON, _, err := v.VerifyDirectory(ctx, tempExtractDir)
	if err != nil {
		return nil, err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal verified manifest: %w", err)
	}

	rt := algorunner.Runtime{AlgorithmsDir: finalInstallDir}
	targetDir := rt.PackageDir(manifest.ID, manifest.Version)
	absBase, err := filepath.Abs(finalInstallDir)
	if err != nil {
		return nil, fmt.Errorf("resolve install root: %w", err)
	}
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve install path: %v", ErrInvalidManifestToken, err)
	}
	relTarget, err := filepath.Rel(absBase, absTarget)
	if err != nil || escapesInstallRoot(relTarget) {
		return nil, fmt.Errorf("%w: resolved install path escapes the install root", ErrInvalidManifestToken)
	}

	idDir := rt.IDDir(manifest.ID)
	if err := os.Mkdir(idDir, 0o755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("create algorithm directory: %w", err)
	}
	idInfo, err := os.Lstat(idDir)
	if err != nil {
		return nil, fmt.Errorf("inspect algorithm directory: %w", err)
	}
	if !idInfo.IsDir() || idInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: algorithm install path is not a directory", ErrInvalidManifestToken)
	}
	_, statErr := os.Lstat(targetDir)
	switch {
	case statErr == nil:
		return nil, ErrVersionAlreadyInstalled
	case !os.IsNotExist(statErr):
		return nil, fmt.Errorf("inspect install target: %w", statErr)
	}
	if err := installDirectoryAtomically(tempExtractDir, targetDir); err != nil {
		if errors.Is(err, ErrVersionAlreadyInstalled) {
			return nil, err
		}
		return nil, fmt.Errorf("install package directory: %w", err)
	}

	now := time.Now().UTC()
	algo = &Algorithm{
		ID:               manifest.ID,
		Name:             manifest.Name,
		Version:          manifest.Version,
		TaskType:         manifest.TaskType,
		Category:         manifest.Category,
		TargetArch:       manifest.Platform.Arch,
		TargetDevice:     manifest.Platform.Target,
		ComputeCost:      manifest.Resources.ComputeCost,
		MemoryMB:         manifest.Resources.MemoryMB,
		Description:      manifest.Description,
		ManifestJSON:     string(manifestJSON),
		ConfigSchemaJSON: configSchemaJSON,
		Status:           StatusDisabled,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return algo, nil
}

// escapesInstallRoot reports whether relTarget, obtained from filepath.Rel
// against the install root, fails to resolve to a strict descendant of it.
// "." is rejected because the install target must live *below* the root.
func escapesInstallRoot(relTarget string) bool {
	switch relTarget {
	case ".", "..":
		return true
	}
	if filepath.IsAbs(relTarget) {
		return true
	}
	return strings.HasPrefix(relTarget, ".."+string(filepath.Separator))
}

func installDirectoryAtomically(src, dst string) (retErr error) {
	renameErr := renameDirectoryNoReplace(src, dst)
	if renameErr == nil {
		return nil
	}

	// EXDEV means the staging directory sits on another filesystem; anything
	// else is a real failure and must not be retried through the copy path.
	if !errors.Is(renameErr, unix.EXDEV) {
		if _, statErr := os.Lstat(dst); statErr == nil {
			return ErrVersionAlreadyInstalled
		}
		return renameErr
	}

	stagingDir, err := os.MkdirTemp(filepath.Dir(dst), ".zhulong_install_*")
	if err != nil {
		return fmt.Errorf("create same-filesystem staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stagingDir); err != nil && retErr == nil {
			retErr = fmt.Errorf("remove install staging directory: %w", err)
		}
	}()
	if err := copyDir(src, stagingDir); err != nil {
		return fmt.Errorf("copy package into staging directory: %w", err)
	}
	if err := renameDirectoryNoReplace(stagingDir, dst); err != nil {
		if _, statErr := os.Lstat(dst); statErr == nil {
			return ErrVersionAlreadyInstalled
		}
		return fmt.Errorf("publish staging directory: %w", err)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in staged package: %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			if closeErr := in.Close(); closeErr != nil {
				return errors.Join(err, closeErr)
			}
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeOutErr := out.Close()
		closeInErr := in.Close()
		return errors.Join(copyErr, closeOutErr, closeInErr)
	})
}
