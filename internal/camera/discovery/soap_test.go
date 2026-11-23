package discovery

import (
	"strings"
	"testing"
)

func TestSOAPBuildProbe(t *testing.T) {
	msgID := "test-message-uuid-1234"
	probe := BuildProbeMessage(msgID)

	probeStr := string(probe)
	if !strings.Contains(probeStr, "uuid:test-message-uuid-1234") {
		t.Fatalf("expected probe to contain message ID, got: %s", probeStr)
	}
	if !strings.Contains(probeStr, ProbeAction) {
		t.Fatalf("expected probe to contain ProbeAction, got: %s", probeStr)
	}
	if !strings.Contains(probeStr, NetworkVideoTransmitterType) {
		t.Fatalf("expected probe to contain NetworkVideoTransmitterType, got: %s", probeStr)
	}
}

func TestSOAPParseProbeMatches_Hikvision(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope"
                   xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
                   xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
                   xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <SOAP-ENV:Header>
    <wsa:MessageID>uuid:12345678-1234-1234-1234-123456789abc</wsa:MessageID>
    <wsa:RelatesTo>uuid:probe-uuid-001</wsa:RelatesTo>
    <wsa:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</wsa:To>
    <wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</wsa:Action>
  </SOAP-ENV:Header>
  <SOAP-ENV:Body>
    <d:ProbeMatches>
      <d:ProbeMatch>
        <wsa:EndpointReference>
          <wsa:Address>urn:uuid:hik-camera-uuid-108</wsa:Address>
        </wsa:EndpointReference>
        <d:Types>dn:NetworkVideoTransmitter</d:Types>
        <d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/type/audio_encoder onvif://www.onvif.org/hardware/DS-2CD2047G2-LU onvif://www.onvif.org/name/FrontGateCamera onvif://www.onvif.org/location/Entrance</d:Scopes>
        <d:XAddrs>http://192.168.1.108/onvif/device_service</d:XAddrs>
        <d:MetadataVersion>1</d:MetadataVersion>
      </d:ProbeMatch>
    </d:ProbeMatches>
  </SOAP-ENV:Body>
</SOAP-ENV:Envelope>`)

	devices, err := ParseProbeMatches(xmlData)
	if err != nil {
		t.Fatalf("unexpected error parsing probe matches: %v", err)
	}

	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}

	d := devices[0]
	if d.ID != "hik-camera-uuid-108" {
		t.Errorf("expected ID 'hik-camera-uuid-108', got %q", d.ID)
	}
	if d.IP != "192.168.1.108" {
		t.Errorf("expected IP '192.168.1.108', got %q", d.IP)
	}
	if d.Port != 80 {
		t.Errorf("expected Port 80, got %d", d.Port)
	}
	if d.Name != "FrontGateCamera" {
		t.Errorf("expected Name 'FrontGateCamera', got %q", d.Name)
	}
	if d.Hardware != "DS-2CD2047G2-LU" {
		t.Errorf("expected Hardware 'DS-2CD2047G2-LU', got %q", d.Hardware)
	}
	if d.Model != "DS-2CD2047G2-LU" {
		t.Errorf("expected Model 'DS-2CD2047G2-LU', got %q", d.Model)
	}
	if d.XAddr != "http://192.168.1.108/onvif/device_service" {
		t.Errorf("expected XAddr 'http://192.168.1.108/onvif/device_service', got %q", d.XAddr)
	}
	if !strings.Contains(d.SuggestedRtspUrl, "192.168.1.108") {
		t.Errorf("expected suggested RTSP url to contain IP, got %q", d.SuggestedRtspUrl)
	}
}

func TestSOAPParseProbeMatches_Dahua(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<Envelope xmlns="http://www.w3.org/2003/05/soap-envelope"
          xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
          xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
          xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <Body>
    <d:ProbeMatches>
      <d:ProbeMatch>
        <wsa:EndpointReference>
          <wsa:Address>uuid:4a961601-5231-11d8-8000-001234567890</wsa:Address>
        </wsa:EndpointReference>
        <d:Types>dn:NetworkVideoTransmitter</d:Types>
        <d:Scopes>onvif://www.onvif.org/manufacturer/Dahua onvif://www.onvif.org/model/IPC-HDBW5442R onvif://www.onvif.org/name/OfficeCorridor</d:Scopes>
        <d:XAddrs>http://192.168.1.109:8080/onvif/device_service</d:XAddrs>
        <d:MetadataVersion>1</d:MetadataVersion>
      </d:ProbeMatch>
    </d:ProbeMatches>
  </Body>
</Envelope>`)

	devices, err := ParseProbeMatches(xmlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}

	d := devices[0]
	if d.ID != "4a961601-5231-11d8-8000-001234567890" {
		t.Errorf("expected ID '4a961601-5231-11d8-8000-001234567890', got %q", d.ID)
	}
	if d.IP != "192.168.1.109" {
		t.Errorf("expected IP '192.168.1.109', got %q", d.IP)
	}
	if d.Port != 8080 {
		t.Errorf("expected Port 8080, got %d", d.Port)
	}
	if d.Manufacturer != "Dahua" {
		t.Errorf("expected Manufacturer 'Dahua', got %q", d.Manufacturer)
	}
	if d.Model != "IPC-HDBW5442R" {
		t.Errorf("expected Model 'IPC-HDBW5442R', got %q", d.Model)
	}
	if !strings.Contains(d.SuggestedRtspUrl, "cam/realmonitor") {
		t.Errorf("expected Dahua suggested RTSP path, got %q", d.SuggestedRtspUrl)
	}
}

func TestSOAPParseProbeMatches_Malformed(t *testing.T) {
	// 1. 空字节切片
	devices, err := ParseProbeMatches(nil)
	if err != nil || len(devices) != 0 {
		t.Errorf("expected empty result without error for nil data, got devices=%v, err=%v", devices, err)
	}

	// 2. 畸形 XML
	_, err = ParseProbeMatches([]byte("<invalid-xml"))
	if err == nil {
		t.Errorf("expected error for malformed xml, got nil")
	}

	// 3. 不相关的 SOAP 消息（无 ProbeMatches）
	irrelevantSOAP := []byte(`<?xml version="1.0"?><Envelope xmlns="http://www.w3.org/2003/05/soap-envelope"><Body><Other/></Body></Envelope>`)
	devices, err = ParseProbeMatches(irrelevantSOAP)
	if err != nil {
		t.Errorf("expected no error for irrelevant SOAP, got: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("expected 0 devices for irrelevant SOAP, got %d", len(devices))
	}
}
