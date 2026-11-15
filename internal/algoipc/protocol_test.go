package algoipc_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
)

func TestIpcPayloadSizeGate(t *testing.T) {
	// Exactly at the limit must pass.
	exact := make([]byte, algoipc.MaxIpcPayloadSize)
	msgBytes, err := algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, exact)
	if err != nil {
		t.Fatalf("payload of exactly MaxIpcPayloadSize must be accepted, got: %v", err)
	}
	if len(msgBytes) != algoipc.IpcHeaderSize+algoipc.MaxIpcPayloadSize {
		t.Fatalf("unexpected framed length: %d", len(msgBytes))
	}
	hdr, err := algoipc.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("DecodeIpcHeader on boundary payload failed: %v", err)
	}
	if hdr.PayloadSize != uint32(algoipc.MaxIpcPayloadSize) {
		t.Errorf("expected payload_size %d, got %d", algoipc.MaxIpcPayloadSize, hdr.PayloadSize)
	}

	// One byte over the limit must be rejected by the send path.
	tooLarge := make([]byte, algoipc.MaxIpcPayloadSize+1)
	_, err = algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, tooLarge)
	if !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Errorf("expected ErrIpcPayloadTooLarge for 64KB+1, got: %v", err)
	}

	// The receive path must also reject a header declaring an oversized payload.
	buf := make([]byte, algoipc.IpcHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], algoipc.IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], algoipc.MsgGallerySync)
	binary.LittleEndian.PutUint16(buf[6:8], algoipc.IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(algoipc.MaxIpcPayloadSize+1))
	binary.LittleEndian.PutUint32(buf[12:16], 1)
	_, err = algoipc.DecodeIpcHeader(buf)
	if !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Errorf("expected DecodeIpcHeader to reject oversized payload_size, got: %v", err)
	}

	// Short header
	if _, err := algoipc.DecodeIpcHeader([]byte{1, 2, 3}); err == nil {
		t.Errorf("expected error on short buffer")
	}

	// Invalid magic
	badMagic := make([]byte, algoipc.IpcHeaderSize)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0x12345678)
	if _, err := algoipc.DecodeIpcHeader(badMagic); err == nil {
		t.Errorf("expected error on invalid magic")
	}

	// Invalid version
	badVer := make([]byte, algoipc.IpcHeaderSize)
	binary.LittleEndian.PutUint32(badVer[0:4], algoipc.IpcMagic)
	binary.LittleEndian.PutUint16(badVer[6:8], 99)
	if _, err := algoipc.DecodeIpcHeader(badVer); err == nil {
		t.Errorf("expected error on invalid version")
	}
}

const nativeIpcProtocolRelPath = "native/src/algorithm/ipc/ipc_protocol.hpp"

func TestIpcContractMatchesNativeHeader(t *testing.T) {
	candidates := []string{
		filepath.Join("..", "..", nativeIpcProtocolRelPath),
		nativeIpcProtocolRelPath,
	}
	var path string
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil || fi.IsDir() {
			continue
		}
		path = abs
		break
	}
	if path == "" {
		t.Skipf("%s not found, skipping cross-language IPC contract test", nativeIpcProtocolRelPath)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read native header: %v", err)
	}
	content := string(raw)

	uint32Tests := []struct {
		name string
		want uint64
	}{
		{name: "IPC_MAGIC", want: uint64(algoipc.IpcMagic)},
		{name: "IPC_VERSION", want: uint64(algoipc.IpcVersion)},
		{name: "MAX_IPC_PAYLOAD_SIZE", want: uint64(algoipc.MaxIpcPayloadSize)},
	}
	for _, tc := range uint32Tests {
		checkUint32Const(
			t,
			content,
			tc.name,
			tc.want,
		)
	}

	enumTests := []struct {
		name string
		want uint64
	}{
		{name: "MSG_RESULT", want: uint64(algoipc.MsgResult)},
		{name: "MSG_GALLERY_SYNC", want: uint64(algoipc.MsgGallerySync)},
		{name: "MSG_GALLERY_UPSERT", want: uint64(algoipc.MsgGalleryUpsert)},
		{name: "MSG_GALLERY_DELETE", want: uint64(algoipc.MsgGalleryDelete)},
		{name: "MSG_GALLERY_ACK", want: uint64(algoipc.MsgGalleryAck)},
		{name: "MSG_TASK_CONFIG", want: uint64(algoipc.MsgTaskConfig)},
		{name: "MSG_TASK_REGIONS", want: uint64(algoipc.MsgTaskRegions)},
	}
	for _, tc := range enumTests {
		checkEnumConst(
			t,
			content,
			tc.name,
			tc.want,
		)
	}
}

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

func evalConstExpr(expr string) (uint64, bool) {
	if i := strings.Index(expr, "//"); i >= 0 {
		expr = expr[:i]
	}
	parts := regexp.MustCompile(`\s*\*\s*`).Split(expr, -1)
	product := uint64(1)
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		v, err := strconv.ParseUint(trimmed, 0, 64)
		if err != nil {
			return 0, false
		}
		product *= v
	}
	return product, true
}
