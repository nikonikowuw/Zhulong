package faceextract_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/faceextract"
)

// All tests drive a *stub provider executable*, never a real algorithm package:
// the client's contract is "speak this protocol to the child", so what the child
// computes is irrelevant, while its failure modes are exactly what must be pinned.

// stubEntry writes a shell script that reads one frame and replies with reply for
// the first $REQUEST_LIMIT frames (default: unbounded), then exits if
// $EXIT_AFTER_FIRST is set.
func stubEntry(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stub_provider")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub provider: %v", err)
	}
	return path
}

func staticEntry(path string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return path, nil }
}

func newClient(t *testing.T, opts faceextract.Options) *faceextract.Client {
	t.Helper()
	if opts.RequestTimeout == 0 {
		opts.RequestTimeout = 5 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = time.Minute
	}
	c := faceextract.New(opts)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func validFrame() faceextract.Frame {
	width, height := uint32(8), uint32(6)
	pixels := make([]byte, width*height*3)
	for i := range pixels {
		pixels[i] = byte(i % 251)
	}
	return faceextract.Frame{Width: width, Height: height, Stride: width * 3, Format: 2, Pixels: pixels}
}

// echoProvider reads exactly the 28-byte header plus payload the client writes and
// answers with a fixed 4-dim vector, echoing request_id. It is the "happy path"
// shape: it proves framing, id echo and reply parsing without depending on any
// inference code.
func echoProvider(dim int, status string, code string) string {
	features := make([]string, dim)
	for i := range features {
		features[i] = fmt.Sprintf("%.6f", float64(i+1)/10)
	}
	// 失败时维度与特征都为空：客户端必须只信 status/code，不能从空数组推断成功。
	reply := fmt.Sprintf(`{"status":"%s","code":"%s","feature_dim":%d,"features":[%s]}`,
		status, code, dim, strings.Join(features, ","))
	if status != "passed" {
		reply = fmt.Sprintf(`{"status":"%s","code":"%s","feature_dim":0,"features":[]}`, status, code)
	}

	// 用 python 当桩：解析 28 字节头 + payload 比 shell 里的 od/read 干净得多。
	return fmt.Sprintf(`#!/usr/bin/env python3
import json, struct, sys

template = %s

while True:
    header = sys.stdin.buffer.read(28)
    if len(header) < 28:
        break
    magic, request_id, width, height, stride, fmt, payload_len = struct.unpack("<7I", header)
    payload = sys.stdin.buffer.read(payload_len)
    if len(payload) != payload_len:
        break
    body = json.loads(template)
    body["request_id"] = request_id
    sys.stdout.write(json.dumps(body) + "\n")
    sys.stdout.flush()
`, strconv.Quote(reply))
}

func TestExtractHappyPath(t *testing.T) {
	entry := stubEntry(t, echoProvider(4, "passed", ""))
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	features, err := client.Extract(context.Background(), validFrame())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(features) != 4 {
		t.Fatalf("feature length = %d, want 4", len(features))
	}
	if features[0] != 0.1 || features[3] != 0.4 {
		t.Fatalf("unexpected features: %v", features)
	}
}

// TestExtractReusesProcess pins the reason this channel exists at all: model load is
// expensive, so N enrolments must cost one process, not N.
func TestExtractReusesProcess(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "spawns")
	body := strings.Replace(echoProvider(2, "passed", ""), "import json, struct, sys",
		"import json, struct, sys, os\nopen('"+counter+"', 'a').write('x')\n", 1)
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	for i := 0; i < 3; i++ {
		if _, err := client.Extract(context.Background(), validFrame()); err != nil {
			t.Fatalf("extract %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read spawn counter: %v", err)
	}
	if got := len(strings.TrimSpace(string(data))); got != 1 {
		t.Fatalf("provider spawned %d times, want 1 (idle reuse is the whole point)", got)
	}
}

func TestExtractMapsFailureCodes(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr error
	}{
		{"no face", "NO_FACE_DETECTED", faceextract.ErrNoFaceDetected},
		{"invalid input", "INVALID_INPUT", faceextract.ErrInvalidArgument},
		{"unsupported", "UNSUPPORTED", faceextract.ErrUnsupported},
		{"model unavailable", "MODEL_UNAVAILABLE", faceextract.ErrUnsupported},
		{"internal", "INTERNAL", faceextract.ErrInternal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := stubEntry(t, echoProvider(0, "failed", tc.code))
			client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

			_, err := client.Extract(context.Background(), validFrame())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestExtractRejectsMalformedFeatureDim covers the "fake success" shape: status
// passed but a dimension the host cannot store. It must never reach the caller,
// because a zero/short vector written to the gallery silently never matches.
func TestExtractRejectsMalformedFeatureDim(t *testing.T) {
	for _, dim := range []int{0, 513} {
		t.Run(fmt.Sprintf("dim=%d", dim), func(t *testing.T) {
			entry := stubEntry(t, echoProvider(dim, "passed", ""))
			client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

			_, err := client.Extract(context.Background(), validFrame())
			if !errors.Is(err, faceextract.ErrInternal) {
				t.Fatalf("error = %v, want ErrInternal", err)
			}
		})
	}
}

// TestExtractRejectsRequestIDMismatch pins the anti-misattribution rule: a reply
// carrying someone else's id must never be accepted, even if it "looks" fine.
func TestExtractRejectsRequestIDMismatch(t *testing.T) {
	body := `#!/usr/bin/env python3
import json, struct, sys
while True:
    header = sys.stdin.buffer.read(28)
    if len(header) < 28:
        break
    magic, request_id, w, h, stride, fmt, payload_len = struct.unpack("<7I", header)
    sys.stdin.buffer.read(payload_len)
    # 始终回一个错的 id
    sys.stdout.write(json.dumps({"request_id": 999, "status": "passed", "feature_dim": 2, "features": [0.1, 0.2]}) + "\n")
    sys.stdout.flush()
`
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	_, err := client.Extract(context.Background(), validFrame())
	if !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal (retried once, still mismatched)", err)
	}
}

// TestExtractRetriesOnceAfterCrash drives the "child dies mid-request" path: the
// first process exits before answering, the second answers. The caller must see one
// success, and exactly two spawns.
func TestExtractRetriesOnceAfterCrash(t *testing.T) {
	dir := t.TempDir()
	spawns := filepath.Join(dir, "spawns")
	body := `#!/usr/bin/env python3
import json, struct, sys, os
path = "` + spawns + `"
n = 1
if os.path.exists(path):
    n = int(open(path).read().strip()) + 1
open(path, "w").write(str(n))
header = sys.stdin.buffer.read(28)
if len(header) < 28:
    sys.exit(0)
magic, request_id, w, h, stride, fmt, payload_len = struct.unpack("<7I", header)
sys.stdin.buffer.read(payload_len)
if n == 1:
    sys.exit(9)   # 第一次直接崩，不给回复
sys.stdout.write(json.dumps({"request_id": request_id, "status": "passed", "feature_dim": 2, "features": [0.5, 0.5]}) + "\n")
sys.stdout.flush()
sys.stdin.read()
`
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	features, err := client.Extract(context.Background(), validFrame())
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if len(features) != 2 {
		t.Fatalf("feature length = %d, want 2", len(features))
	}
	data, err := os.ReadFile(spawns)
	if err != nil {
		t.Fatalf("read spawn counter: %v", err)
	}
	if string(data) != "2" {
		t.Fatalf("spawns = %q, want 2 (one crash + one retry, never more)", string(data))
	}
}

// TestExtractSecondFailureIsFinal keeps the retry bound honest: if the fresh child
// also fails, the request fails rather than looping.
func TestExtractSecondFailureIsFinal(t *testing.T) {
	dir := t.TempDir()
	spawns := filepath.Join(dir, "spawns")
	body := `#!/usr/bin/env python3
import sys, os
path = "` + spawns + `"
n = 1
if os.path.exists(path):
    n = int(open(path).read().strip()) + 1
open(path, "w").write(str(n))
sys.exit(7)
`
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	if _, err := client.Extract(context.Background(), validFrame()); !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal", err)
	}
	data, _ := os.ReadFile(spawns)
	if string(data) != "2" {
		t.Fatalf("spawns = %q, want exactly 2", string(data))
	}
}

// TestExtractTimeoutKillsProcessGroup is the resource-safety case: a hung child
// holding a loaded model must be killed, not abandoned.
func TestExtractTimeoutKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	body := `#!/usr/bin/env python3
import os, sys, time
open("` + pidFile + `", "w").write(str(os.getpid()))
sys.stdin.buffer.read(28)
time.sleep(60)
`
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{
		Entry:          staticEntry(entry),
		RequestTimeout: 300 * time.Millisecond,
	})

	started := time.Now()
	_, err := client.Extract(context.Background(), validFrame())
	if !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout was not enforced, took %s", elapsed)
	}

	pidBytes, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("read provider pid: %v", readErr)
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(pidBytes)), "%d", &pid); err != nil {
		t.Fatalf("parse pid: %v", err)
	}
	// 进程被 SIGKILL 后不应再存活（僵尸不算存活，但 -0 探测仍会成功直到被回收；
	// 客户端会 Wait，所以这里应为「不存在」）。
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := processAlive(pid); err == nil && !aliveIsZombie(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("provider pid %d still alive after timeout kill", pid)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		break
	}
}

// TestExtractRejectsNonJSONStdout pins the "stdout is protocol only" rule: a package
// that prints a debug line must be detected, not misparsed.
func TestExtractRejectsNonJSONStdout(t *testing.T) {
	body := `#!/usr/bin/env python3
import sys
header = sys.stdin.buffer.read(28)
sys.stdin.buffer.read(0)
sys.stdout.write("debug: model loaded from /tmp/x.onnx\n")
sys.stdout.flush()
sys.stdin.read()
`
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	_, err := client.Extract(context.Background(), validFrame())
	if !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal", err)
	}
}

// TestExtractRetiresOnExecutableChange pins the correctness rule (not a perf one):
// after an in-place upgrade the child must be replaced, or new enrolments would be
// produced by the old model while streaming recognition uses the new one.
func TestExtractRetiresOnExecutableChange(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "stub_provider")
	write := func(dim int) {
		if err := os.WriteFile(entry, []byte(echoProvider(dim, "passed", "")), 0o755); err != nil {
			t.Fatalf("write provider: %v", err)
		}
		// 同一纳秒内的改写可能拿到相同 mtime：显式后移，模拟真实替换
		future := time.Now().Add(time.Duration(dim) * time.Millisecond)
		if err := os.Chtimes(entry, future, future); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	write(2)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})
	if features, err := client.Extract(context.Background(), validFrame()); err != nil || len(features) != 2 {
		t.Fatalf("first extract: features=%d err=%v", len(features), err)
	}

	write(4)
	features, err := client.Extract(context.Background(), validFrame())
	if err != nil {
		t.Fatalf("extract after upgrade: %v", err)
	}
	if len(features) != 4 {
		t.Fatalf("feature length = %d, want 4 (provider was not replaced)", len(features))
	}
}

// TestExtractIdleRetires keeps the memory curve: an unused client must not pin a
// loaded model forever.
func TestExtractIdleRetires(t *testing.T) {
	dir := t.TempDir()
	spawns := filepath.Join(dir, "spawns")
	pidFile := filepath.Join(dir, "pid")
	body := strings.Replace(echoProvider(2, "passed", ""), "import json, struct, sys",
		"import json, struct, sys, os\n"+
			"open('"+spawns+"', 'a').write('x')\n"+
			"open('"+pidFile+"', 'w').write(str(os.getpid()))\n", 1)
	entry := stubEntry(t, body)

	client := newClient(t, faceextract.Options{
		Entry:       staticEntry(entry),
		IdleTimeout: 200 * time.Millisecond,
	})
	if _, err := client.Extract(context.Background(), validFrame()); err != nil {
		t.Fatalf("first extract: %v", err)
	}
	firstPID := readPID(t, pidFile)

	// 等 reaper 真的回收：以「子进程消失」为观察到的事实，而不是猜时间。
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := processAlive(firstPID); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle reaper never retired provider pid %d", firstPID)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := client.Extract(context.Background(), validFrame()); err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if got := countSpawns(t, spawns); got != 2 {
		t.Fatalf("provider spawned %d times, want 2 (idle retire must force a new one)", got)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			var pid int
			if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); scanErr == nil {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider never published its pid to %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestExtractRejectsBadFrames keeps validation on the Go side: a malformed frame
// must never cost a child process or a model load.
func TestExtractRejectsBadFrames(t *testing.T) {
	var spawned atomic.Bool
	client := newClient(t, faceextract.Options{
		Entry: func(context.Context) (string, error) {
			spawned.Store(true)
			return "/nonexistent", nil
		},
	})

	good := validFrame()
	cases := []struct {
		name  string
		frame faceextract.Frame
	}{
		{"zero width", faceextract.Frame{Width: 0, Height: 1, Format: 2}},
		{"zero height", faceextract.Frame{Width: 1, Height: 0, Format: 2}},
		{"bad format", faceextract.Frame{Width: 2, Height: 2, Format: 9, Pixels: make([]byte, 12)}},
		{"short stride", faceextract.Frame{Width: 4, Height: 2, Stride: 4, Format: 2, Pixels: make([]byte, 8)}},
		{"payload mismatch", faceextract.Frame{Width: 4, Height: 2, Format: 2, Pixels: make([]byte, 23)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.Extract(context.Background(), tc.frame); !errors.Is(err, faceextract.ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
	if spawned.Load() {
		t.Fatal("a malformed frame must be rejected before any provider process is started")
	}
	_ = good
}

// TestExtractReportsMissingEntry maps "package not installed / not runnable" onto a
// distinct error so the HTTP layer can answer 500 with an actionable message.
func TestExtractReportsMissingEntry(t *testing.T) {
	client := newClient(t, faceextract.Options{
		Entry: func(context.Context) (string, error) {
			return "", errors.New("no such package")
		},
	})
	if _, err := client.Extract(context.Background(), validFrame()); !errors.Is(err, faceextract.ErrEntry) {
		t.Fatalf("error = %v, want ErrEntry", err)
	}
}

// processAlive reports whether pid still exists (zombies count as existing).
func processAlive(pid int) error {
	return syscall.Kill(pid, 0)
}

// aliveIsZombie reads /proc/<pid>/stat and reports whether the process is a zombie:
// after a SIGKILL the child stays visible until someone reaps it, and the client
// reaps it, so a lingering zombie would mean our own Wait is broken.
func aliveIsZombie(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return false
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	return len(fields) > 0 && fields[0] == "Z"
}

func countSpawns(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read spawn counter: %v", err)
	}
	return len(strings.TrimSpace(string(data)))
}
