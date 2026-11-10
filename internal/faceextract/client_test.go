package faceextract_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	if _, err := exec.LookPath("python3"); err != nil {
		// Every stub here is a python3 script; skipping is honest, a confusing cascade
		// of "exec format error" failures is not.
		t.Skip("python3 is required to run the stub provider")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "stub_provider")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub provider: %v", err)
	}
	return path
}

// waitUntil polls cond and fails the test if it never reports true. Every wait in this
// file observes a fact (a process left, a file appeared) instead of guessing a sleep.
func waitUntil(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s within %s", what, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	return faceextract.Frame{
		Width:  width,
		Height: height,
		Stride: width * 3,
		Format: faceextract.FormatRGB24,
		Pixels: pixels,
	}
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
	reply := fmt.Sprintf(
		`{"status":"%s","code":"%s","feature_dim":%d,"features":[%s]}`,
		status,
		code,
		dim,
		strings.Join(features, ","),
	)
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
	body := strings.Replace(
		echoProvider(2, "passed", ""),
		"import json, struct, sys",
		"import json, struct, sys, os\nopen('"+counter+"', 'a').write('x')\n",
		1,
	)
	entry := stubEntry(t, body)
	client := newClient(t, faceextract.Options{Entry: staticEntry(entry)})

	for i := range 3 {
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
		{name: "no face", code: "NO_FACE_DETECTED", wantErr: faceextract.ErrNoFaceDetected},
		{name: "invalid input", code: "INVALID_INPUT", wantErr: faceextract.ErrInvalidArgument},
		{name: "unsupported", code: "UNSUPPORTED", wantErr: faceextract.ErrUnsupported},
		{name: "model unavailable", code: "MODEL_UNAVAILABLE", wantErr: faceextract.ErrUnsupported},
		{name: "multiple faces", code: "MULTIPLE_FACES", wantErr: faceextract.ErrAmbiguous},
		{name: "internal", code: "INTERNAL", wantErr: faceextract.ErrInternal},
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
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if convErr != nil {
		t.Fatalf("parse pid: %v", convErr)
	}
	// 进程被 SIGKILL 后不应再存活（僵尸不算存活，-0 探测仍会成功直到被回收；
	// 客户端会 Wait，所以这里应为「不存在」）。
	waitUntil(
		t,
		3*time.Second,
		fmt.Sprintf("provider pid %d to disappear", pid),
		func() bool {
			return processAlive(pid) != nil || aliveIsZombie(pid)
		},
	)
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
	body := strings.Replace(
		echoProvider(2, "passed", ""),
		"import json, struct, sys",
		"import json, struct, sys, os\n"+
			"open('"+spawns+"', 'a').write('x')\n"+
			"open('"+pidFile+"', 'w').write(str(os.getpid()))\n",
		1,
	)
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
	waitUntil(
		t,
		10*time.Second,
		fmt.Sprintf("idle reaper to retire provider pid %d", firstPID),
		func() bool {
			return processAlive(firstPID) != nil
		},
	)

	if _, err := client.Extract(context.Background(), validFrame()); err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if got := countSpawns(t, spawns); got != 2 {
		t.Fatalf("provider spawned %d times, want 2 (idle retire must force a new one)", got)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitUntil(
		t,
		5*time.Second,
		fmt.Sprintf("provider to publish its pid to %s", path),
		func() bool {
			raw, err := os.ReadFile(path)
			if err != nil {
				return false
			}
			got, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr != nil {
				return false
			}
			pid = got
			return true
		},
	)
	return pid
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

	badFrames := []struct {
		name  string
		frame faceextract.Frame
	}{
		{
			name:  "zero width",
			frame: faceextract.Frame{Width: 0, Height: 1, Format: faceextract.FormatRGB24},
		},
		{
			name:  "zero height",
			frame: faceextract.Frame{Width: 1, Height: 0, Format: faceextract.FormatRGB24},
		},
		{
			name:  "bad format",
			frame: faceextract.Frame{Width: 2, Height: 2, Format: 9, Pixels: make([]byte, 12)},
		},
		{
			name:  "short stride",
			frame: faceextract.Frame{Width: 4, Height: 2, Stride: 4, Format: faceextract.FormatRGB24, Pixels: make([]byte, 8)},
		},
		{
			name:  "payload mismatch",
			frame: faceextract.Frame{Width: 4, Height: 2, Format: faceextract.FormatRGB24, Pixels: make([]byte, 23)},
		},
	}
	for _, tc := range badFrames {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.Extract(context.Background(), tc.frame); !errors.Is(err, faceextract.ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
		})
	}
	if spawned.Load() {
		t.Fatal("a malformed frame must be rejected before any provider process is started")
	}
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

// TestExtractFrameVerdictKeepsProcess pins spec §6.1: a frame-level verdict means the
// child answered correctly, so the hot process (with its loaded model) must survive.
// Killing it here turns every rejected photo into a cold start for the next one.
func TestExtractFrameVerdictKeepsProcess(t *testing.T) {
	for _, code := range []string{"NO_FACE_DETECTED", "INVALID_INPUT"} {
		t.Run(code, func(t *testing.T) {
			dir := t.TempDir()
			counter := filepath.Join(dir, "spawns")
			body := strings.Replace(
				echoProvider(0, "failed", code),
				"import json, struct, sys",
				"import json, struct, sys, os\nopen('"+counter+"', 'a').write('x')\n",
				1,
			)
			client := newClient(t, faceextract.Options{Entry: staticEntry(stubEntry(t, body))})

			for i := range 3 {
				if _, err := client.Extract(context.Background(), validFrame()); !errors.Is(err, faceextract.ErrNoFaceDetected) &&
					!errors.Is(err, faceextract.ErrInvalidArgument) {
					t.Fatalf("extract %d: error = %v, want a frame-level verdict", i, err)
				}
			}
			if got := countSpawns(t, counter); got != 1 {
				t.Fatalf("provider spawned %d times, want 1 (a bad photo must not destroy the hot process)", got)
			}
		})
	}
}

// TestExtractDesyncRetiresProcess is the other half of the same rule: when stdout is
// not a protocol line, the stream is untrustworthy and the child must be replaced
// before the retry — reusing it would read the leftover of the previous answer.
func TestExtractDesyncRetiresProcess(t *testing.T) {
	dir := t.TempDir()
	spawns := filepath.Join(dir, "spawns")
	seq := filepath.Join(dir, "seq")
	body := `#!/usr/bin/env python3
import json, struct, sys, os
open("` + spawns + `", "a").write("x")
path = "` + seq + `"
n = 1
if os.path.exists(path):
    n = int(open(path).read().strip()) + 1
open(path, "w").write(str(n))
while True:
    header = sys.stdin.buffer.read(28)
    if len(header) < 28:
        break
    magic, request_id, w, h, stride, fmt, payload_len = struct.unpack("<7I", header)
    sys.stdin.buffer.read(payload_len)
    if n == 1:
        sys.stdout.write("debug: model loaded from /tmp/x.onnx\n")  # 非协议行，流已失步
    else:
        sys.stdout.write(json.dumps({"request_id": request_id, "status": "passed", "feature_dim": 2, "features": [0.5, 0.5]}) + "\n")
    sys.stdout.flush()
`
	client := newClient(t, faceextract.Options{Entry: staticEntry(stubEntry(t, body))})

	features, err := client.Extract(context.Background(), validFrame())
	if err != nil {
		t.Fatalf("the retry should have succeeded on a fresh process: %v", err)
	}
	if len(features) != 2 {
		t.Fatalf("feature length = %d, want 2", len(features))
	}
	if got := countSpawns(t, spawns); got != 2 {
		t.Fatalf("spawns = %d, want 2 (desync must replace the child)", got)
	}
}

// TestExtractCancellationIsNotRetried covers the caller-goes-away path: the kill is
// expected, but retrying would spawn a child only to kill it again immediately.
func TestExtractCancellationIsNotRetried(t *testing.T) {
	dir := t.TempDir()
	spawns := filepath.Join(dir, "spawns")
	body := `#!/usr/bin/env python3
import json, struct, sys, os, time
open("` + spawns + `", "a").write("x")
while True:
    header = sys.stdin.buffer.read(28)
    if len(header) < 28:
        break
    magic, request_id, w, h, stride, fmt, payload_len = struct.unpack("<7I", header)
    sys.stdin.buffer.read(payload_len)
    time.sleep(60)  # 永不回答：只有 ctx 到期能让这次请求结束
`
	client := newClient(t, faceextract.Options{Entry: staticEntry(stubEntry(t, body))})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := client.Extract(ctx, validFrame())

	if !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal (cancellation is still a host-side failure)", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancellation was not propagated promptly, took %s", elapsed)
	}
	if got := countSpawns(t, spawns); got != 1 {
		t.Fatalf("spawns = %d, want 1 (a cancelled request must not be retried)", got)
	}
}

// TestExtractAcceptsPaddedStride proves the stride field is honoured: a caller may
// hand over rows with padding, and validation must accept exactly that frame.
func TestExtractAcceptsPaddedStride(t *testing.T) {
	dir := t.TempDir()
	strideFile := filepath.Join(dir, "stride")
	body := strings.Replace(
		echoProvider(2, "passed", ""),
		"    payload = sys.stdin.buffer.read(payload_len)",
		"    payload = sys.stdin.buffer.read(payload_len)\n    open('"+strideFile+"', 'w').write('%d,%d' % (stride, len(payload)))",
		1,
	)
	client := newClient(t, faceextract.Options{Entry: staticEntry(stubEntry(t, body))})

	const width, height, stride = uint32(8), uint32(6), uint32(8*3 + 4)
	frame := faceextract.Frame{
		Width:  width,
		Height: height,
		Stride: stride,
		Format: faceextract.FormatRGB24,
		Pixels: make([]byte, stride*height),
	}
	if _, err := client.Extract(context.Background(), frame); err != nil {
		t.Fatalf("padded stride must be accepted: %v", err)
	}

	got, err := os.ReadFile(strideFile)
	if err != nil {
		t.Fatalf("read stride probe: %v", err)
	}
	want := fmt.Sprintf("%d,%d", stride, stride*height)
	if string(got) != want {
		t.Fatalf("provider saw stride,payload = %q, want %q", string(got), want)
	}
}

// TestExtractRejectsOverlongReplyLine keeps a runaway provider from turning one stdout
// line into unbounded host memory: the line cap must fire long before the timeout.
func TestExtractRejectsOverlongReplyLine(t *testing.T) {
	body := `#!/usr/bin/env python3
import sys
sys.stdin.buffer.read(28)
sys.stdout.buffer.write(b"x" * (1024 * 1024))
sys.stdout.buffer.flush()
sys.stdin.read()
`
	client := newClient(t, faceextract.Options{Entry: staticEntry(stubEntry(t, body))})

	started := time.Now()
	_, err := client.Extract(context.Background(), validFrame())
	if !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("error = %v, want ErrInternal", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("an overlong line must be rejected without waiting for the timeout, took %s", elapsed)
	}
}

// TestCloseIsIdempotentAndStopsExtraction pins the lifecycle contract: Close twice is
// harmless, and a closed client must not spawn a child that no reaper would retire.
func TestCloseIsIdempotentAndStopsExtraction(t *testing.T) {
	dir := t.TempDir()
	spawns := filepath.Join(dir, "spawns")
	body := strings.Replace(
		echoProvider(2, "passed", ""),
		"import json, struct, sys",
		"import json, struct, sys, os\nopen('"+spawns+"', 'a').write('x')\n",
		1,
	)
	client := faceextract.New(faceextract.Options{
		Entry:          staticEntry(stubEntry(t, body)),
		RequestTimeout: 5 * time.Second,
		IdleTimeout:    time.Minute,
	})

	if _, err := client.Extract(context.Background(), validFrame()); err != nil {
		t.Fatalf("first extract: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	_, err := client.Extract(context.Background(), validFrame())
	if !errors.Is(err, faceextract.ErrInternal) {
		t.Fatalf("extract after close: error = %v, want ErrInternal", err)
	}
	if got := countSpawns(t, spawns); got != 1 {
		t.Fatalf("spawns = %d, want 1 (a closed client must not spawn)", got)
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
