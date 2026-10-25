package gb28181

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// The fixtures below are synthetic: they follow the GB/T 28181 Notify shape
// (<Notify><CmdType>Keepalive</CmdType><SN>..</SN><DeviceID>..</DeviceID><Status>OK</Status></Notify>)
// and are not captured from a vendor device. Vendor compatibility is verified in S4.

func TestDecodeXMLAcceptsUTF8Keepalive(t *testing.T) {
	body := []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<Notify><CmdType>Keepalive</CmdType><SN>17</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><Status>OK</Status></Notify>")

	message, err := decodeXML(body)
	if err != nil {
		t.Fatalf("decodeXML failed: %v", err)
	}
	if message.XMLName.Local != "Notify" {
		t.Errorf("expected root Notify, got %q", message.XMLName.Local)
	}
	if message.CmdType != "Keepalive" || message.SN != 17 {
		t.Errorf("unexpected command payload: %+v", message)
	}
	if message.DeviceID != "34020000001320000001" {
		t.Errorf("unexpected device id %q", message.DeviceID)
	}
}

func TestDecodeXMLTranscodesUndeclearedGB18030(t *testing.T) {
	// Field devices frequently send GB2312/GB18030 bytes with no declaration at all.
	// The decoder must go by the bytes, not by the (absent) declaration.
	raw := []byte("<Notify><CmdType>Keepalive</CmdType><SN>1</SN>" +
		"<DeviceID>34020000001320000001</DeviceID><Status>正常</Status></Notify>")
	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes(raw)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}

	message, err := decodeXML(encoded)
	if err != nil {
		t.Fatalf("decodeXML failed for GB18030: %v", err)
	}
	if message.Status != "正常" {
		t.Fatalf("expected the GB18030 body to be transcoded, got %q", message.Status)
	}
}

func TestDecodeXMLRejectsHostileDocuments(t *testing.T) {
	deep := "<Notify>"
	for range maxInboundXMLDepth {
		deep += "<Nested>"
	}
	deep += "<CmdType>Keepalive</CmdType>"
	for range maxInboundXMLDepth {
		deep += "</Nested>"
	}
	deep += "</Notify>"

	tests := []struct {
		name string
		body []byte
		want error
	}{
		{
			name: "oversized document",
			body: bytes.Repeat([]byte("A"), maxInboundXMLBytes+1),
			want: ErrXMLTooLarge,
		},
		{
			name: "deep nesting",
			body: []byte(deep),
			want: ErrXMLTooDeep,
		},
		{
			name: "doctype with entity",
			body: []byte(`<?xml version="1.0"?>
<!DOCTYPE Notify [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>
<Notify><CmdType>&xxe;</CmdType></Notify>`),
			want: ErrXMLExternalEntity,
		},
		{
			name: "unsupported declared charset",
			body: []byte(`<?xml version="1.0" encoding="UTF-16"?><Notify><CmdType>Keepalive</CmdType></Notify>`),
			want: ErrXMLUnsupported,
		},
		{
			name: "truncated document",
			body: []byte("<Notify><CmdType>Keepalive</CmdType>"),
			want: ErrXMLMalformed,
		},
		{
			name: "empty body",
			body: nil,
			want: ErrXMLMalformed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeXML(test.body); !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestDeclaredCharsetParsing(t *testing.T) {
	tests := []struct {
		instruction string
		want        string
	}{
		{`version="1.0" encoding="GB2312"`, "gb2312"},
		{`version='1.0' encoding='UTF-8'`, "utf-8"},
		{`version="1.0"`, ""},
		{`version="1.0" encoding="`, ""},
	}
	for _, test := range tests {
		if got := declaredCharset(test.instruction); got != test.want {
			t.Errorf("declaredCharset(%q) = %q, want %q", test.instruction, got, test.want)
		}
	}
}
