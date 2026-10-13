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

// TestIpcContractMatchesNativeHeader parses the native header and cross-checks
// IPC_MAGIC / IPC_VERSION / MAX_IPC_PAYLOAD_SIZE against the Go constants.
// This is the cheap guardrail against the enum/constant drift that already
// happened once (Go defined gallery types 10/11/12 that native lacks).
//
// The test skips when the native source tree is absent (e.g. cross-compiled or
// source-stripped test environments) so it never becomes a brittle dependency.
func TestIpcContractMatchesNativeHeader(t *testing.T) {
	path := findNativeIpcHeader(t)
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
}

func findNativeIpcHeader(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", nativeIpcProtocolRelPath),
		nativeIpcProtocolRelPath,
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
