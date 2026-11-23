package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	// WSDiscoveryMulticastAddress IPv4 WS-Discovery 多播组播地址。
	WSDiscoveryMulticastAddress = "239.255.255.250:3702"

	// ProbeAction ONVIF WS-Discovery Probe 动作 URI。
	ProbeAction = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe"

	// NetworkVideoTransmitterType ONVIF 网络视频传输设备类型。
	NetworkVideoTransmitterType = "dn:NetworkVideoTransmitter"
)

// BuildProbeMessage 生成符合 WS-Discovery 规范的 SOAP 1.2 Probe 组播报文。
func BuildProbeMessage(messageID string) []byte {
	if messageID == "" {
		messageID = uuid.NewString()
	}

	payload := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<Envelope xmlns="http://www.w3.org/2003/05/soap-envelope" `+
		`xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" `+
		`xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" `+
		`xmlns:dn="http://www.onvif.org/ver10/network/wsdl">`+
		`<Header>`+
		`<wsa:MessageID>uuid:%s</wsa:MessageID>`+
		`<wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>`+
		`<wsa:Action>%s</wsa:Action>`+
		`</Header>`+
		`<Body>`+
		`<d:Probe>`+
		`<d:Types>%s</d:Types>`+
		`</d:Probe>`+
		`</Body>`+
		`</Envelope>`,
		messageID,
		ProbeAction,
		NetworkVideoTransmitterType,
	)

	return []byte(payload)
}

// soapProbeMatchesEnvelope 用于反序列化 ProbeMatches 的通用 SOAP 结构。
type soapProbeMatchesEnvelope struct {
	XMLName xml.Name             `xml:"Envelope"`
	Body    soapProbeMatchesBody `xml:"Body"`
}

type soapProbeMatchesBody struct {
	ProbeMatches []soapProbeMatch `xml:"ProbeMatches>ProbeMatch"`
}

type soapProbeMatch struct {
	EndpointReference soapEndpointRef `xml:"EndpointReference"`
	Types             string          `xml:"Types"`
	Scopes            string          `xml:"Scopes"`
	XAddrs            string          `xml:"XAddrs"`
	MetadataVersion   string          `xml:"MetadataVersion"`
}

type soapEndpointRef struct {
	Address string `xml:"Address"`
}

// ParseProbeMatches 解析来自摄像机的 SOAP ProbeMatches 响应报文。
func ParseProbeMatches(xmlData []byte) ([]DiscoveredDevice, error) {
	if len(xmlData) == 0 {
		return []DiscoveredDevice{}, nil
	}

	var env soapProbeMatchesEnvelope
	if err := xml.Unmarshal(xmlData, &env); err != nil {
		return []DiscoveredDevice{}, fmt.Errorf("unmarshal probe matches: %w", err)
	}

	devices := make([]DiscoveredDevice, 0, len(env.Body.ProbeMatches))
	for _, match := range env.Body.ProbeMatches {
		device := parseSingleMatch(match)
		if device.IP != "" {
			devices = append(devices, device)
		}
	}

	return devices, nil
}

func parseSingleMatch(match soapProbeMatch) DiscoveredDevice {
	rawScopes := strings.Fields(match.Scopes)
	rawTypes := strings.Fields(match.Types)
	rawXAddrs := strings.Fields(match.XAddrs)

	device := DiscoveredDevice{
		Types:  rawTypes,
		Scopes: rawScopes,
	}

	// 1. 提取 EndpointReference UUID
	addr := strings.TrimSpace(match.EndpointReference.Address)
	if strings.HasPrefix(addr, "urn:uuid:") {
		device.ID = strings.TrimPrefix(addr, "urn:uuid:")
	} else if strings.HasPrefix(addr, "uuid:") {
		device.ID = strings.TrimPrefix(addr, "uuid:")
	} else {
		device.ID = addr
	}

	// 2. 解析 Scopes
	for _, scope := range rawScopes {
		unescaped, err := url.PathUnescape(scope)
		if err != nil {
			unescaped = scope
		}

		lower := strings.ToLower(unescaped)
		switch {
		case strings.HasPrefix(unescaped, "onvif://www.onvif.org/name/"):
			device.Name = strings.TrimPrefix(unescaped, "onvif://www.onvif.org/name/")
		case strings.HasPrefix(unescaped, "onvif://www.onvif.org/hardware/"):
			device.Hardware = strings.TrimPrefix(unescaped, "onvif://www.onvif.org/hardware/")
			if device.Model == "" {
				device.Model = device.Hardware
			}
		case strings.HasPrefix(unescaped, "onvif://www.onvif.org/model/"):
			device.Model = strings.TrimPrefix(unescaped, "onvif://www.onvif.org/model/")
		case strings.HasPrefix(unescaped, "onvif://www.onvif.org/manufacturer/"):
			device.Manufacturer = strings.TrimPrefix(unescaped, "onvif://www.onvif.org/manufacturer/")
		case strings.Contains(lower, "hikvision") && device.Manufacturer == "":
			device.Manufacturer = "Hikvision"
		case strings.Contains(lower, "dahua") && device.Manufacturer == "":
			device.Manufacturer = "Dahua"
		case strings.Contains(lower, "uniview") && device.Manufacturer == "":
			device.Manufacturer = "Uniview"
		case strings.Contains(lower, "tp-link") && device.Manufacturer == "":
			device.Manufacturer = "TP-Link"
		}
	}

	// 3. 解析 XAddrs 获取 IP 与 Port
	for _, xAddr := range rawXAddrs {
		parsedURL, err := url.Parse(xAddr)
		if err != nil || parsedURL.Host == "" {
			continue
		}

		device.XAddr = xAddr
		host, portStr, err := net.SplitHostPort(parsedURL.Host)
		if err == nil {
			device.IP = host
			if p, convErr := strconv.Atoi(portStr); convErr == nil {
				device.Port = p
			}
		} else {
			device.IP = parsedURL.Host
			if parsedURL.Scheme == "https" {
				device.Port = 443
			} else {
				device.Port = 80
			}
		}
		break
	}

	// 若名称仍为空，使用 Model 或 IP 兜底
	if device.Name == "" {
		if device.Model != "" {
			device.Name = device.Model
		} else if device.IP != "" {
			device.Name = "Camera-" + device.IP
		}
	}

	// 若 ID 仍为空，通过 IP 和 XAddr 生成伪 UUID
	if device.ID == "" && device.IP != "" {
		hash := sha256.Sum256([]byte(device.IP + ":" + device.XAddr))
		device.ID = hex.EncodeToString(hash[:16])
	}

	// 生成建议的 RTSP URL
	if device.IP != "" {
		device.SuggestedRtspUrl = DeriveSuggestedRtspUrl(device.Manufacturer, device.IP)
	}

	return device
}
