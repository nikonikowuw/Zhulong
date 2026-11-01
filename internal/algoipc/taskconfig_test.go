package algoipc_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
)

// TestEncodeTaskConfigPayload pins the MSG_TASK_CONFIG layout: a little-endian
// uint32 config length, a 64-byte NUL-padded algorithm id, then the raw JSON.
func TestEncodeTaskConfigPayload(t *testing.T) {
	config := []byte(`{"threshold":0.55,"top_k":2}`)
	payload, err := algoipc.EncodeTaskConfigPayload("face_recognition", config)
	if err != nil {
		t.Fatalf("EncodeTaskConfigPayload: %v", err)
	}
	if len(payload) != algoipc.TaskConfigHeaderSize+len(config) {
		t.Fatalf("payload length = %d, want header %d + config %d",
			len(payload), algoipc.TaskConfigHeaderSize, len(config))
	}
	if got := binary.LittleEndian.Uint32(payload[0:4]); got != uint32(len(config)) {
		t.Fatalf("config length field = %d, want %d", got, len(config))
	}
	if got := string(payload[4 : 4+len("face_recognition")]); got != "face_recognition" {
		t.Fatalf("algorithm id prefix = %q", got)
	}
	for i := 4 + len("face_recognition"); i < algoipc.TaskConfigHeaderSize; i++ {
		if payload[i] != 0 {
			t.Fatalf("algorithm id field must be NUL padded, byte %d = %d", i, payload[i])
		}
	}
	if got := string(payload[algoipc.TaskConfigHeaderSize:]); got != string(config) {
		t.Fatalf("config body = %q, want %q", got, string(config))
	}

	// An empty object is a legitimate value ("no per-task overrides"), not an error.
	if _, err := algoipc.EncodeTaskConfigPayload("face_recognition", []byte("{}")); err != nil {
		t.Fatalf("empty object config must encode: %v", err)
	}
}

func TestEncodeTaskConfigPayloadRejectsInvalidInput(t *testing.T) {
	if _, err := algoipc.EncodeTaskConfigPayload("", []byte("{}")); err == nil {
		t.Fatal("empty algorithm id must be rejected: the field is the only routing key")
	}
	// 64 bytes would leave no room for the NUL terminator the reader relies on.
	tooLong := string(make([]byte, algoipc.TaskConfigHeaderSize-4))
	if _, err := algoipc.EncodeTaskConfigPayload(tooLong, []byte("{}")); err == nil {
		t.Fatal("algorithm id at the field width must be rejected")
	}
	oversized := make([]byte, algoipc.MaxIpcPayloadSize)
	if _, err := algoipc.EncodeTaskConfigPayload("face_recognition", oversized); !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Fatalf("oversized config must return ErrIpcPayloadTooLarge, got: %v", err)
	}
	// Exactly at the boundary (header + body == MaxIpcPayloadSize) must still fit.
	exact := make([]byte, algoipc.MaxIpcPayloadSize-algoipc.TaskConfigHeaderSize)
	if _, err := algoipc.EncodeTaskConfigPayload("face_recognition", exact); err != nil {
		t.Fatalf("config filling the payload budget must encode: %v", err)
	}
}

// TestTaskConfigHeaderMatchesNativeStruct keeps the Go header size tied to the
// C++ struct. A silent drift here shows up as "the package reads a truncated id
// and drops every config" — no crash, no error, just a feature that never works.
func TestTaskConfigHeaderMatchesNativeStruct(t *testing.T) {
	candidates := []string{
		filepath.Join("..", "..", "native/src/algorithm/ipc/ipc_protocol.hpp"),
		"native/src/algorithm/ipc/ipc_protocol.hpp",
	}
	var path string
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if fi, statErr := os.Stat(abs); statErr == nil && !fi.IsDir() {
			path = abs
			break
		}
	}
	if path == "" {
		t.Skip("ipc_protocol.hpp not found, skipping cross-language layout check")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read native header: %v", err)
	}
	header := string(raw)

	sizeRe := regexp.MustCompile(`sizeof\(\s*TaskConfigMsg\s*\)\s*==\s*(\d+)`)
	m := sizeRe.FindStringSubmatch(header)
	if m == nil {
		t.Fatal("native header has no sizeof(TaskConfigMsg) assertion")
	}
	size, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse native TaskConfigMsg size: %v", err)
	}
	if size != algoipc.TaskConfigHeaderSize {
		t.Fatalf("TaskConfigMsg size drift: native %d, Go TaskConfigHeaderSize %d",
			size, algoipc.TaskConfigHeaderSize)
	}

	// Field order is part of the contract: length first, id second.
	fieldRe := regexp.MustCompile(`struct\s+TaskConfigMsg\s*\{([\s\S]*?)\n\};`)
	decl := fieldRe.FindStringSubmatch(header)
	if decl == nil {
		t.Fatal("native header has no TaskConfigMsg declaration")
	}
	lenIdx := regexp.MustCompile(`config_json_len`).FindStringIndex(decl[1])
	idIdx := regexp.MustCompile(`algorithm_id\[`).FindStringIndex(decl[1])
	if lenIdx == nil || idIdx == nil || lenIdx[0] > idIdx[0] {
		t.Fatal("TaskConfigMsg must declare config_json_len before algorithm_id")
	}
}
