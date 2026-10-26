package gb28181

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestCatalogXML(t *testing.T) {
	// 1. Build Query XML
	queryXML := buildCatalogQuery(12345, "34020000001180000001")
	if !strings.Contains(string(queryXML), "<CmdType>Catalog</CmdType>") {
		t.Errorf("missing CmdType in query: %s", string(queryXML))
	}
	if !strings.Contains(string(queryXML), "<SN>12345</SN>") {
		t.Errorf("missing SN in query: %s", string(queryXML))
	}
	if !strings.Contains(string(queryXML), "<DeviceID>34020000001180000001</DeviceID>") {
		t.Errorf("missing DeviceID in query: %s", string(queryXML))
	}

	// 2. Parse Catalog Response with multiple items, PTZType, PtzType, Resolution
	xmlBody := []byte(`<?xml version="1.0" encoding="GB2312"?>
<Response>
  <CmdType>Catalog</CmdType>
  <SN>12345</SN>
  <DeviceID>34020000001180000001</DeviceID>
  <SumNum>2</SumNum>
  <DeviceList Num="2">
    <Item>
      <DeviceID>34020000001310000001</DeviceID>
      <Name>Front Gate Camera</Name>
      <Manufacturer>Hikvision</Manufacturer>
      <Model>DS-2CD2T47</Model>
      <Owner>Zhulong</Owner>
      <CivilCode>34020000</CivilCode>
      <Address>East Gate</Address>
      <Parental>0</Parental>
      <ParentID>34020000001180000001</ParentID>
      <SafetyWay>0</SafetyWay>
      <RegisterWay>1</RegisterWay>
      <PTZType>1</PTZType>
      <Resolution>1920*1080</Resolution>
      <Status>ON</Status>
    </Item>
    <Item>
      <DeviceID>34020000001310000002</DeviceID>
      <Name>Back Yard Camera</Name>
      <Manufacturer>Dahua</Manufacturer>
      <Model>DH-IPC-HFW</Model>
      <PtzType>2</PtzType>
      <Resolution>2560x1440</Resolution>
      <Status>ON</Status>
    </Item>
  </DeviceList>
</Response>`)

	resp, err := decodeCatalogResponse(xmlBody)
	if err != nil {
		t.Fatalf("decodeCatalogResponse failed: %v", err)
	}
	if resp.CmdType != "Catalog" || resp.SN != 12345 || resp.DeviceID != "34020000001180000001" {
		t.Fatalf("unexpected envelope fields: %+v", resp)
	}
	if resp.SumNum != 2 || len(resp.DeviceList) != 2 {
		t.Fatalf("expected SumNum=2 and 2 items, got sum=%d, len=%d", resp.SumNum, len(resp.DeviceList))
	}

	now := time.Now()
	ch1 := resp.DeviceList[0].toChannel("34020000001180000001", now)
	if ch1.ChannelID != "34020000001310000001" || ch1.Name != "Front Gate Camera" {
		t.Errorf("unexpected ch1: %+v", ch1)
	}
	if ch1.PTZType == nil || *ch1.PTZType != 1 {
		t.Errorf("expected PTZType=1, got %v", ch1.PTZType)
	}
	if ch1.ResolutionWidth == nil || *ch1.ResolutionWidth != 1920 || ch1.ResolutionHeight == nil || *ch1.ResolutionHeight != 1080 {
		t.Errorf("expected 1920x1080, got %v x %v", ch1.ResolutionWidth, ch1.ResolutionHeight)
	}

	ch2 := resp.DeviceList[1].toChannel("34020000001180000001", now)
	if ch2.ChannelID != "34020000001310000002" || ch2.Name != "Back Yard Camera" {
		t.Errorf("unexpected ch2: %+v", ch2)
	}
	if ch2.PTZType == nil || *ch2.PTZType != 2 {
		t.Errorf("expected PtzType=2, got %v", ch2.PTZType)
	}
	if ch2.ResolutionWidth == nil || *ch2.ResolutionWidth != 2560 || ch2.ResolutionHeight == nil || *ch2.ResolutionHeight != 1440 {
		t.Errorf("expected 2560x1440, got %v x %v", ch2.ResolutionWidth, ch2.ResolutionHeight)
	}
}
