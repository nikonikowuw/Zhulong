package algorithm_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
)

// nativeIpcProtocolRelPath is the native single source of truth for the IPC
// wire contract. The Go constants must stay aligned with it.
const nativeIpcProtocolRelPath = "native/src/algorithm/ipc/ipc_protocol.hpp"

// nativePluginHeaderRelPath is the native single source of truth for the Plugin C ABI.
const nativePluginHeaderRelPath = "native/sdk/include/zhulong/plugin.h"

// TestPluginContractMatchesNativeHeader cross-checks result_type enums and
// string/dimension constraints between zhulong/plugin.h and Go ResultType.
func TestPluginContractMatchesNativeHeader(t *testing.T) {
	path := findNativeHeader(t, nativePluginHeaderRelPath)
	if path == "" {
		t.Skipf("%s not found, skipping cross-language Plugin contract test", nativePluginHeaderRelPath)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read native plugin header: %v", err)
	}
	content := string(raw)

	checkDefineConst(t, content, "ZHULONG_PERSON_ID_MAX_LEN", 64)
	checkDefineConst(t, content, "ZHULONG_PERSON_NAME_MAX_LEN", 64)
	checkDefineConst(t, content, "ZHULONG_FACE_LANDMARK_COUNT", 5)

	checkEnumConst(t, content, "ZHULONG_RESULT_TYPE_UNKNOWN", uint64(algorithm.ResultTypeUnknown))
	checkEnumConst(t, content, "ZHULONG_RESULT_TYPE_DETECTION", uint64(algorithm.ResultTypeDetection))
	checkEnumConst(t, content, "ZHULONG_RESULT_TYPE_CLASSIFICATION", uint64(algorithm.ResultTypeClassification))
	checkEnumConst(t, content, "ZHULONG_RESULT_TYPE_PERSON_STRUCTURE", uint64(algorithm.ResultTypePersonStructure))
	checkEnumConst(t, content, "ZHULONG_RESULT_TYPE_CUSTOM", uint64(algorithm.ResultTypeCustom))
}

func findNativeHeader(t *testing.T, relPath string) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", relPath),
		relPath,
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
			return abs
		}
	}
	return ""
}

func checkDefineConst(t *testing.T, content, name string, want uint64) {
	t.Helper()
	re := regexp.MustCompile(`#define\s+` + regexp.QuoteMeta(name) + `\s+([0-9]+)`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		t.Fatalf("native header does not define %s", name)
	}
	got, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		t.Fatalf("cannot parse %s value %q: %v", name, m[1], err)
	}
	if got != want {
		t.Errorf("%s drift: native=%d, Go=%d", name, got, want)
	}
}

func checkEnumConst(t *testing.T, content, name string, want uint64) {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(name) + `\s*=\s*([0-9]+)`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		t.Fatalf("native header does not declare enum %s", name)
	}
	got, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		t.Fatalf("cannot parse enum %s value %q: %v", name, m[1], err)
	}
	if got != want {
		t.Errorf("enum %s drift: native=%d, Go=%d", name, got, want)
	}
}

// TestIpcContractMatchesNativeHeader parses the native header and cross-checks
// IPC_MAGIC / IPC_VERSION / MAX_IPC_PAYLOAD_SIZE against the Go constants.
// This is the cheap guardrail against the enum/constant drift that already
// happened once (Go defined gallery types 10/11/12 that native lacks).
//
// The test skips when the native source tree is absent (e.g. cross-compiled or
// source-stripped test environments) so it never becomes a brittle dependency.
func TestIpcContractMatchesNativeHeader(t *testing.T) {
	path := findNativeHeader(t, nativeIpcProtocolRelPath)
	if path == "" {
		t.Skipf("%s not found, skipping cross-language IPC contract test", nativeIpcProtocolRelPath)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read native header: %v", err)
	}
	content := string(raw)

	checkUint32Const(t, content, "IPC_MAGIC", uint64(algorithm.IpcMagic))
	checkUint32Const(t, content, "IPC_VERSION", uint64(algorithm.IpcVersion))
	checkUint32Const(t, content, "MAX_IPC_PAYLOAD_SIZE", uint64(algorithm.MaxIpcPayloadSize))
	checkEnumConst(t, content, "MSG_RESULT", uint64(algorithm.MsgResult))
}

// checkUint32Const extracts `constexpr uint32_t NAME = <expr>;` (or uint16_t)
// from the native header and asserts it equals want. MAX_IPC_PAYLOAD_SIZE is
// written as `64 * 1024`, so a plain integer literal parse is not enough — we
// evaluate a small subset of constant expressions.
func checkUint32Const(t *testing.T, content, name string, want uint64) {
	t.Helper()
	re := regexp.MustCompile(`constexpr\s+uint(?:16|32)_t\s+` + regexp.QuoteMeta(name) + `\s*=\s*([^;]+);`)
	m := re.FindStringSubmatch(content)
	if m == nil {
		t.Fatalf("native header does not declare %s", name)
	}
	got, ok := evalConstExpr(m[1])
	if !ok {
		t.Fatalf("cannot evaluate native %s expression %q", name, m[1])
	}
	if got != want {
		t.Errorf("%s drift: native=%d, Go=%d", name, got, want)
	}
}

// evalConstExpr evaluates the tiny subset of C++ constant expressions used in
// ipc_protocol.hpp: decimal/hex integer literals and `a * b` products, ignoring
// trailing line comments.
func evalConstExpr(expr string) (uint64, bool) {
	if i := indexOfComment(expr); i >= 0 {
		expr = expr[:i]
	}
	parts := regexp.MustCompile(`\s*\*\s*`).Split(expr, -1)
	product := uint64(1)
	for _, p := range parts {
		p = regexp.MustCompile(`\s+`).ReplaceAllString(p, "")
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 0, 64)
		if err != nil {
			return 0, false
		}
		product *= v
	}
	return product, true
}

func indexOfComment(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '/' && s[i+1] == '/' {
			return i
		}
	}
	return -1
}
