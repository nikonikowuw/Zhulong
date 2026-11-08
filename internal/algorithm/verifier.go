package algorithm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

var (
	ErrPlatformMismatch        = errors.New("algorithm platform does not match host platform")
	ErrInvalidManifest         = errors.New("invalid manifest.json")
	ErrMissingEntryExecutable  = errors.New("missing package entry executable in package root")
	ErrMissingSampleFile       = errors.New("missing convention sample.jpg in package root")
	ErrMissingManifestFile     = errors.New("missing convention manifest.json in package root")
	ErrInvalidConfigSchema     = errors.New("invalid config.schema.json: not a valid JSON schema")
	ErrVersionAlreadyInstalled = errors.New("algorithm version is already installed")
	ErrVerificationFailed      = errors.New("dynamic package verification failed")
	ErrResultTypeMismatch      = errors.New("plugin exported result_type does not match manifest task_type")
	ErrInvalidComputeBudget    = errors.New("invalid compute budget in manifest")

	// ErrPlatformTargetRetired marks a package whose manifest.platform.target is
	// the retired "cpu" spelling. The host target is now "onnx" (the ONNX Runtime
	// tier); such packages must be rebuilt for that target. Deliberately not
	// silently mapped: a cpu-built package carries a different runtime and assets,
	// so the failure would surface much later as an opaque loader error.
	ErrPlatformTargetRetired = errors.New(
		"manifest.platform.target \"cpu\" is retired, rebuild the package for target \"onnx\"")

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

// manifestNameMaxBytes bounds manifest.Name to the package's handshake buffer
// (HandshakeReq.client_name[32], ipc_protocol.hpp:37). The value travels to the
// package process via --name (supervisor.go) and is copied with strncpy, which silently
// truncates on overflow — two packages could then share one client name.
const manifestNameMaxBytes = 32

// VerificationReport reflects the final JSON line emitted by the package entry
// when invoked as `<entry> --verify --sample <image>`.
type VerificationReport struct {
	Status     string `json:"status"`
	Sample     string `json:"sample"`
	ResultType int    `json:"result_type"`
	Count      int    `json:"count"`
	// SampleDecoded reports whether the entry decoded the sample into a real frame.
	// False means the run fell back to a synthetic frame, so passing verification
	// proves the ABI lifecycle only — never that the packaged sample was inferred.
	SampleDecoded bool                         `json:"sample_decoded"`
	Width         int                          `json:"width"`
	Height        int                          `json:"height"`
	Error         string                       `json:"error,omitempty"`
	CostMs        int64                        `json:"cost_ms,omitempty"`
	Result        *aitypes.RawInferencePayload `json:"result,omitempty"`
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
	// SkipDynamic keeps verification to static checks only. Reserved for tests and
	// for build hosts that cannot execute target-arch binaries: without it a
	// mismatched host/package pair fails with an exec error instead of a verdict
	// about the package itself.
	SkipDynamic bool
}

// Verifier executes static contract audits and dynamic black-box verification.
//
// Immutable after construction: the host identity, the skip flag and the timeout
// are captured once, so a verdict can never depend on a field mutated between
// calls.
//
// Note what is *not* here: there is no host-side runner binary. Dynamic
// verification executes the package's own entry, because that is exactly what
// production executes — verifying through a different launcher would let the two
// code paths drift.
type Verifier struct {
	host        HostPlatform
	timeout     time.Duration
	skipDynamic bool
	logger      *zap.Logger
}

// NewVerifier creates a Verifier. host.Target defaults to onnx and the zero
// VerifyOptions selects the defaults.
func NewVerifier(host HostPlatform, opts VerifyOptions) *Verifier {
	timeout := DefaultVerifyTimeout
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	host.Target = normalizeHostTarget(host.Target)

	return &Verifier{
		host:        host,
		timeout:     timeout,
		skipDynamic: opts.SkipDynamic,
		logger:      opts.Logger,
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

// VerifyResult 是一次目录级验包的产物。字段具名而不是相邻的裸 string 返回值：
// ConfigSchema 与 EnvFile 类型相同、位置相邻，写反了编译器不会提醒。
type VerifyResult struct {
	// Manifest 是解析并校验通过的清单。
	Manifest *Manifest
	// ConfigSchema 是 config.schema.json 的内容，缺失时为 "{}"。
	ConfigSchema string
	// EnvFile 是 .env 的内容，缺失时为空串。
	EnvFile string
}

// VerifyDirectory verifies an extracted package directory.
//
// 各阶段顺序固定：最便宜、最可行动的问题必须先暴露。一个为别的设备构建的包应该
// 因为平台被拒，而不是因为一个它永远不会被要求执行的入口文件缺失。
func (v *Verifier) VerifyDirectory(ctx context.Context, pkgDir string) (*VerifyResult, error) {
	manifest, err := readManifest(pkgDir)
	if err != nil {
		return nil, err
	}
	if err := validateIdentity(manifest); err != nil {
		return nil, err
	}
	if err := validateEnums(manifest); err != nil {
		return nil, err
	}
	if err := validateResources(manifest); err != nil {
		return nil, err
	}
	if err := v.validatePlatform(manifest); err != nil {
		return nil, err
	}

	samplePath, entryPath, err := resolveConventionFiles(pkgDir, manifest.ID)
	if err != nil {
		return nil, err
	}

	configSchema, err := readConfigSchema(pkgDir)
	if err != nil {
		return nil, err
	}

	result := &VerifyResult{
		Manifest:     manifest,
		ConfigSchema: configSchema,
		EnvFile:      readEnvFile(pkgDir),
	}

	// Stage 2: Dynamic black-box verification by running the package entry
	if !v.skipDynamic {
		if err := v.runDynamicVerification(
			ctx,
			entryPath,
			samplePath,
			manifest.TaskType,
		); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// readManifest 读取并解析包根的 manifest.json。
//
// manifest.json 排在约定文件检查的第一位：入口可执行文件是*按 manifest.id 命名*的，
// 所以必须先解析清单并校验 id，才谈得上解析入口；反过来做就意味着信任一个未经
// 校验的路径组件。
func readManifest(pkgDir string) (*Manifest, error) {
	manifestPath := filepath.Join(pkgDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		return nil, ErrMissingManifestFile
	}

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if manifest.ID == "" || manifest.Version == "" || manifest.Name == "" {
		return nil, fmt.Errorf("%w: id, version, and name are required", ErrInvalidManifest)
	}
	return &manifest, nil
}

// validateIdentity 校验清单的身份字段。
//
// 只有 ID 和 Version 参与路径构造（targetDir = Runtime.PackageDir(ID, Version)），
// 所以只有它们受字符集约束，避免 "../../../tmp/pwned" 之类逃出 installDir。
// Name 是人类可读的展示字段（如「安全帽佩戴检测算法」），从不参与路径构造，
// 只需放得下 runner 的握手缓冲区。
func validateIdentity(manifest *Manifest) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "id", value: manifest.ID},
		{name: "version", value: manifest.Version},
	} {
		if !manifestTokenPattern.MatchString(field.value) {
			return fmt.Errorf("%w: %s %q", ErrInvalidManifestToken, field.name, field.value)
		}
	}

	// Name 经 --name 传给包进程，被 strncpy 进 HandshakeReq 的 client_name[32]
	// （ipc_protocol.hpp:37, runner.cpp:201），超长会被静默截断，两个包就可能
	// 共享一个 client 名。这里直接拒绝。
	if len(manifest.Name) > manifestNameMaxBytes {
		return fmt.Errorf("%w: name exceeds %d bytes", ErrInvalidManifestToken, manifestNameMaxBytes)
	}
	return nil
}

func validateEnums(manifest *Manifest) error {
	switch manifest.TaskType {
	case TaskTypeDetection,
		TaskTypeClassification,
		TaskTypePersonStructure,
		TaskTypeVehicleStructure,
		TaskTypeSceneStructure:
	default:
		return fmt.Errorf("%w: invalid task_type %q", ErrInvalidManifest, manifest.TaskType)
	}

	switch manifest.Category {
	case CategorySafety, CategorySecurity, CategoryFire, CategoryTraffic, CategoryGeneral:
	default:
		return fmt.Errorf("%w: invalid category %q", ErrInvalidManifest, manifest.Category)
	}
	return nil
}

func validateResources(manifest *Manifest) error {
	if manifest.Resources.ComputeCost <= 0 || manifest.Resources.ComputeCost > 1000 {
		return fmt.Errorf("%w: compute_cost must be between 1 and 1000", ErrInvalidComputeBudget)
	}
	if manifest.Resources.MemoryMB <= 0 {
		return fmt.Errorf("%w: memory_mb must be positive", ErrInvalidComputeBudget)
	}
	return nil
}

// validatePlatform 是静态平台准入闸门。
//
// 已退役的 "cpu" 档位在任何比较之前就被拒绝：把它当成 "onnx" 静默放行会装进一个
// 运行时与资产都不匹配的包，失败要到很久以后才以一个难懂的 loader/exec 错误爆出来。
func (v *Verifier) validatePlatform(manifest *Manifest) error {
	if manifest.Platform.Target == retiredHostTarget {
		return fmt.Errorf("%w: package %s %s was built for platform.target=%q",
			ErrPlatformTargetRetired, manifest.ID, manifest.Version, retiredHostTarget)
	}
	if manifest.Platform.Arch != "" && manifest.Platform.Arch != v.host.Arch {
		return fmt.Errorf("%w: package requires arch %q, host is %q",
			ErrPlatformMismatch, manifest.Platform.Arch, v.host.Arch)
	}

	// An empty target on either side means "unspecified", not "mismatch": the
	// package may not pin a device, and the host may not know its own.
	bothTargetsKnown := manifest.Platform.Target != "" && v.host.Target != ""
	if bothTargetsKnown && manifest.Platform.Target != v.host.Target {
		return fmt.Errorf("%w: package requires target %q, host is %q",
			ErrPlatformMismatch, manifest.Platform.Target, v.host.Target)
	}
	return nil
}

// resolveConventionFiles 定位动态阶段需要的两个约定文件：样本图与包根入口。
//
// 入口是一个以 manifest.id 命名的普通可执行文件，经 algorunner.ResolveEntry 解析，
// 保证安装期接受的就是 supervisor 稍后要执行的那个文件；仍在发老式 plugin.so 的包
// 在这里被拒，因为安装期是运维唯一还能处理它的时刻。
func resolveConventionFiles(pkgDir, manifestID string) (samplePath, entryPath string, err error) {
	samplePath = filepath.Join(pkgDir, "sample.jpg")
	if _, err := os.Stat(samplePath); err != nil {
		return "", "", ErrMissingSampleFile
	}

	entryPath, err = algorunner.ResolveEntry(pkgDir, manifestID)
	if err != nil {
		return "", "", err
	}

	// 确认是包内普通文件之后才规范权限位，这样 chmod 不可能被诱导跟随符号链接。
	if err := os.Chmod(entryPath, 0o755); err != nil {
		return "", "", fmt.Errorf("make package entry executable: %w", err)
	}
	return samplePath, entryPath, nil
}

// readConfigSchema 读取可选的 config.schema.json：缺失返回 "{}"，
// 存在但编译不通过则整体拒绝安装。
func readConfigSchema(pkgDir string) (string, error) {
	schemaPath := filepath.Join(pkgDir, "config.schema.json")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "{}", nil
		}
		return "", fmt.Errorf("read config.schema.json: %w", err)
	}

	schemaJSON := string(schemaBytes)
	if _, err := compileConfigSchema(schemaJSON); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidConfigSchema, err)
	}
	return schemaJSON, nil
}

// readEnvFile 读取可选的 .env。读不到就当作没有：这个文件是包自己的运行时配置，
// 准入结论（ABI / 平台 / 结果类型）不依赖它，安装路径也不消费它。
func readEnvFile(pkgDir string) string {
	content, err := os.ReadFile(filepath.Join(pkgDir, algorunner.EnvFileName))
	if err != nil {
		return ""
	}
	return string(content)
}

// expectedResultType 把 manifest 的 task_type 映射到插件必须导出的 result_type。
var expectedResultType = map[string]ResultType{
	TaskTypeDetection:        ResultTypeDetection,
	TaskTypeClassification:   ResultTypeClassification,
	TaskTypePersonStructure:  ResultTypePersonStructure,
	TaskTypeVehicleStructure: ResultTypeVehicleStructure,
	TaskTypeSceneStructure:   ResultTypeSceneStructure,
}

func (v *Verifier) runDynamicVerification(ctx context.Context, entryPath, samplePath, taskType string) error {
	vCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	// 样本帧：Go 用标准库把 sample.jpg/png 解码成 PPM 交给包入口。包入口只带骨架，
	// 不链接任何图像解码器（它不属于推理链路），所以「真的把包里的样本图喂进模型」
	// 这件事由调用侧负责。解码失败不阻断安装（老包可能用了标准库不支持的编码），
	// 但会让包入口回退合成帧，并在报告里标记 sample_decoded=false，再以告警暴露给运维。
	framePath, cleanup := writeSampleFramePPM(samplePath)
	if cleanup != nil {
		defer cleanup()
	}
	sampleArg := samplePath
	if framePath != "" {
		sampleArg = framePath
	}

	cmd := exec.CommandContext(
		vCtx,
		entryPath,
		"--verify",
		"--sample",
		sampleArg,
	)
	// 以包目录为 CWD：验包必须复现部署布局（可执行文件与 models/ 同级）。
	// 否则「从源码树 CWD 意外命中权重」会让模型路径类缺陷（拼出 models/models/…）
	// 在开发机上全部逃过准入，到部署环境才以 model_create 失败爆出。
	cmd.Dir = filepath.Dir(entryPath)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: execution failed (%v): %s", ErrVerificationFailed, err, stderrBuf.String())
	}

	report, err := parseVerificationReport(stdoutBuf.String())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}

	if report.Status != "passed" {
		return fmt.Errorf("%w: package reported status %q: %s", ErrVerificationFailed, report.Status, report.Error)
	}

	// 准入结论的可信度取决于「样本帧是否真的进了模型」：
	// 没解码成功就只能证明 ABI 生命周期。
	switch {
	case !report.SampleDecoded:
		v.warn("algorithm verification ran without a decoded sample frame; only the ABI lifecycle was exercised",
			zap.String("sample", samplePath),
			zap.String("entry", entryPath))
	case report.Count == 0:
		v.warn("algorithm verification decoded the sample frame but the plugin reported zero results",
			zap.String("sample", samplePath),
			zap.Int("width", report.Width),
			zap.Int("height", report.Height))
	}

	// Cross-check result_type against manifest task_type.
	// TaskTypePersonStructure allows either ResultTypePersonStructure (v1) or
	// ResultTypeSceneStructure (v2 decoupled scene structure).
	if expected, ok := expectedResultType[taskType]; ok {
		matches := report.ResultType == int(expected) ||
			(taskType == TaskTypePersonStructure && report.ResultType == int(ResultTypeSceneStructure))
		if !matches {
			return fmt.Errorf("%w: expected result_type %d for %q, got %d",
				ErrResultTypeMismatch, expected, taskType, report.ResultType)
		}
	}

	return nil
}

// parseVerificationReport extracts and parses the final VerificationReport JSON from entry output.
// Many vendor libraries (e.g. librga / rknpu) print diagnostic banners upon exit,
// so it searches backwards for the final valid JSON report line.
func parseVerificationReport(output string) (*VerificationReport, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, errors.New("empty output from package entry")
	}
	lines := strings.Split(trimmed, "\n")

	var report VerificationReport
	var parsed bool
	var parseErr error
	var candidateLine string

	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if candidateLine == "" {
			candidateLine = line
		}
		if strings.HasPrefix(line, "{") && strings.Contains(line, `"status"`) {
			if err := json.Unmarshal([]byte(line), &report); err == nil && report.Status != "" {
				parsed = true
				break
			} else if err != nil && parseErr == nil {
				parseErr = err
			}
		}
	}

	if !parsed {
		if parseErr != nil {
			return nil, fmt.Errorf("cannot parse package report (%s): %w", candidateLine, parseErr)
		}
		return nil, fmt.Errorf("no valid verification report found in output (%s)", candidateLine)
	}
	return &report, nil
}

// StageAndVerify extracts an archive into a temporary folder, verifies it,
// and atomically publishes the immutable version directory under finalInstallDir.
func (v *Verifier) StageAndVerify(
	ctx context.Context,
	archivePath string,
	finalInstallDir string,
) (algo *Algorithm, retErr error) {
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

	verified, err := v.VerifyDirectory(ctx, tempExtractDir)
	if err != nil {
		return nil, err
	}
	manifest := verified.Manifest
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
		ID:                 manifest.ID,
		Name:               manifest.Name,
		Version:            manifest.Version,
		TaskType:           manifest.TaskType,
		Category:           manifest.Category,
		TargetArch:         manifest.Platform.Arch,
		TargetDevice:       manifest.Platform.Target,
		ComputeCost:        manifest.Resources.ComputeCost,
		MemoryMB:           manifest.Resources.MemoryMB,
		Description:        manifest.Description,
		ManifestJSON:       string(manifestJSON),
		ConfigSchemaJSON:   verified.ConfigSchema,
		SupportsMotionGate: manifest.SupportsMotionGateOrDefault(),
		Status:             StatusDisabled,
		CreatedAt:          now,
		UpdatedAt:          now,
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

// copyDir 递归复制 src 到 dst，只接受普通文件与目录：暂存区里出现符号链接或其他
// 特殊文件时直接失败，而不是把它们带进安装目录。
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in staged package: %q", path)
		}
		return copyFile(path, target, info.Mode())
	})
}

// copyFile 复制单个普通文件，落盘权限沿用源文件。
func copyFile(src, dst string, mode fs.FileMode) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		return errors.Join(err, out.Close())
	}
	return out.Close()
}
