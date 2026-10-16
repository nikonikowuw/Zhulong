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

func TestIpcPayloadSizeGate(t *testing.T) {
	// Exactly at the limit must pass.
	exact := make([]byte, algoipc.MaxIpcPayloadSize)
	msgBytes, err := algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, exact)
	if err != nil {
		t.Fatalf("payload of exactly MaxIpcPayloadSize must be accepted, got: %v", err)
	}
	if len(msgBytes) != 16+algoipc.MaxIpcPayloadSize {
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
	if _, err := algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, tooLarge); !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Errorf("expected ErrIpcPayloadTooLarge for 64KB+1, got: %v", err)
	}

	// The receive path must also reject a header declaring an oversized payload.
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint32(buf[0:4], algoipc.IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], algoipc.MsgGallerySync)
	binary.LittleEndian.PutUint16(buf[6:8], algoipc.IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(algoipc.MaxIpcPayloadSize+1))
	binary.LittleEndian.PutUint32(buf[12:16], 1)
	if _, err := algoipc.DecodeIpcHeader(buf); !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Errorf("expected DecodeIpcHeader to reject oversized payload_size, got: %v", err)
	}

	// Short header
	if _, err := algoipc.DecodeIpcHeader([]byte{1, 2, 3}); err == nil {
		t.Errorf("expected error on short buffer")
	}

	// Invalid magic
	badMagic := make([]byte, 16)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0x12345678)
	if _, err := algoipc.DecodeIpcHeader(badMagic); err == nil {
		t.Errorf("expected error on invalid magic")
	}

	// Invalid version
	badVer := make([]byte, 16)
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
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				path = abs
				break
			}
		}
	}
	if path == "" {
		t.Skipf("%s not found, skipping cross-language IPC contract test", nativeIpcProtocolRelPath)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read native header: %v", err)
	}
	content := string(raw)

	checkUint32Const(t, content, "IPC_MAGIC", uint64(algoipc.IpcMagic))
	checkUint32Const(t, content, "IPC_VERSION", uint64(algoipc.IpcVersion))
	checkUint32Const(t, content, "MAX_IPC_PAYLOAD_SIZE", uint64(algoipc.MaxIpcPayloadSize))
	checkEnumConst(t, content, "MSG_RESULT", uint64(algoipc.MsgResult))
	checkEnumConst(t, content, "MSG_GALLERY_SYNC", uint64(algoipc.MsgGallerySync))
	checkEnumConst(t, content, "MSG_GALLERY_UPSERT", uint64(algoipc.MsgGalleryUpsert))
	checkEnumConst(t, content, "MSG_GALLERY_DELETE", uint64(algoipc.MsgGalleryDelete))
	checkEnumConst(t, content, "MSG_GALLERY_ACK", uint64(algoipc.MsgGalleryAck))
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
