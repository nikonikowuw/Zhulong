package gb28181

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Inbound XML limits. GB/T 28181 devices send small Notify/Response documents;
// anything larger is either a broken device or an attempt to exhaust memory, so the
// cap is enforced before any decoding happens.
const (
	maxInboundXMLBytes = 32 * 1024
	maxInboundXMLDepth = 8
)

// Document-level rejections. They are sentinel values so callers can map them onto
// SIP status codes and tests can assert the specific defence that fired.
var (
	ErrXMLTooLarge       = errors.New("gb28181 xml: document exceeds the size limit")
	ErrXMLTooDeep        = errors.New("gb28181 xml: nesting exceeds the depth limit")
	ErrXMLUnsupported    = errors.New("gb28181 xml: unsupported declared charset")
	ErrXMLExternalEntity = errors.New("gb28181 xml: DOCTYPE/entity declarations are not allowed")
	ErrXMLMalformed      = errors.New("gb28181 xml: document is malformed")
)

// allowedXMLCharsets lists the declarations accepted after byte-level normalization.
// The declared value is never trusted for decoding (see decodeXML); it is only
// rejected when it names a charset this code cannot have produced.
var allowedXMLCharsets = map[string]struct{}{
	"":         {},
	"utf-8":    {},
	"utf8":     {},
	"us-ascii": {},
	"ascii":    {},
	"gb2312":   {},
	"gbk":      {},
	"gb18030":  {},
}

// gbXMLMessage is the shared envelope of every GB28181 XML body. Individual
// commands only differ in the fields they populate, so one struct keeps the parser
// small; unknown elements are ignored by encoding/xml.
type gbXMLMessage struct {
	XMLName  xml.Name
	CmdType  string `xml:"CmdType"`
	SN       int    `xml:"SN"`
	DeviceID string `xml:"DeviceID"`
	Status   string `xml:"Status"`
}

// gbCatalogItem represents an individual <Item> inside <DeviceList>.
type gbCatalogItem struct {
	DeviceID     string  `xml:"DeviceID"`
	Name         string  `xml:"Name"`
	Manufacturer *string `xml:"Manufacturer"`
	Model        *string `xml:"Model"`
	Owner        *string `xml:"Owner"`
	CivilCode    *string `xml:"CivilCode"`
	Address      *string `xml:"Address"`
	Parental     int     `xml:"Parental"`
	ParentID     *string `xml:"ParentID"`
	SafetyWay    *int    `xml:"SafetyWay"`
	RegisterWay  *int    `xml:"RegisterWay"`
	PTZType1     *int    `xml:"PTZType"`
	PTZType2     *int    `xml:"PtzType"`
	Resolution   string  `xml:"Resolution"`
	Status       string  `xml:"Status"`
}

func (item gbCatalogItem) toChannel(deviceID string, now time.Time) Channel {
	ptz := item.PTZType1
	if ptz == nil {
		ptz = item.PTZType2
	}
	width, height := parseResolution(item.Resolution)
	var parentID *string
	if item.ParentID != nil && strings.TrimSpace(*item.ParentID) != "" {
		trimmed := strings.TrimSpace(*item.ParentID)
		parentID = &trimmed
	}
	return Channel{
		DeviceID:         deviceID,
		ChannelID:        strings.TrimSpace(item.DeviceID),
		Name:             strings.TrimSpace(item.Name),
		Manufacturer:     trimStringPtr(item.Manufacturer),
		Model:            trimStringPtr(item.Model),
		Owner:            trimStringPtr(item.Owner),
		CivilCode:        trimStringPtr(item.CivilCode),
		Address:          trimStringPtr(item.Address),
		Parental:         item.Parental,
		ParentID:         parentID,
		SafetyWay:        item.SafetyWay,
		RegisterWay:      item.RegisterWay,
		PTZType:          ptz,
		ResolutionWidth:  width,
		ResolutionHeight: height,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// gbCatalogResponse represents a parsed Catalog response document.
type gbCatalogResponse struct {
	XMLName    xml.Name        `xml:"Response"`
	CmdType    string          `xml:"CmdType"`
	SN         int             `xml:"SN"`
	DeviceID   string          `xml:"DeviceID"`
	SumNum     int             `xml:"SumNum"`
	DeviceList []gbCatalogItem `xml:"DeviceList>Item"`
}

// prepareXMLDocument normalizes and validates an inbound GB28181 XML payload,
// transcoding from GB18030 when the payload is not valid UTF-8.
func prepareXMLDocument(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: empty body", ErrXMLMalformed)
	}
	if len(raw) > maxInboundXMLBytes {
		return nil, ErrXMLTooLarge
	}

	normalized := raw
	if !utf8.Valid(raw) {
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: transcode from GB18030: %v", ErrXMLMalformed, err)
		}
		normalized = decoded
	}

	if err := validateXMLDocument(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// decodeXML normalizes and validates a GB28181 XML body.
func decodeXML(raw []byte) (gbXMLMessage, error) {
	normalized, err := prepareXMLDocument(raw)
	if err != nil {
		return gbXMLMessage{}, err
	}

	var msg gbXMLMessage
	decoder := newXMLDecoder(normalized)
	if err := decoder.Decode(&msg); err != nil {
		return gbXMLMessage{}, fmt.Errorf("%w: %v", ErrXMLMalformed, err)
	}
	if msg.XMLName.Local == "" {
		return gbXMLMessage{}, fmt.Errorf("%w: missing root element", ErrXMLMalformed)
	}
	return msg, nil
}

// decodeCatalogResponse normalizes and decodes a Catalog Response XML body.
func decodeCatalogResponse(raw []byte) (gbCatalogResponse, error) {
	normalized, err := prepareXMLDocument(raw)
	if err != nil {
		return gbCatalogResponse{}, err
	}

	var resp gbCatalogResponse
	decoder := newXMLDecoder(normalized)
	if err := decoder.Decode(&resp); err != nil {
		return gbCatalogResponse{}, fmt.Errorf("%w: %v", ErrXMLMalformed, err)
	}
	if resp.XMLName.Local == "" {
		return gbCatalogResponse{}, fmt.Errorf("%w: missing root element", ErrXMLMalformed)
	}
	return resp, nil
}

// buildCatalogQuery serializes a standard GB/T 28181 Catalog Query XML body.
func buildCatalogQuery(sn int, deviceID string) []byte {
	return []byte(fmt.Sprintf("<?xml version=\"1.0\" encoding=\"GB2312\"?>\r\n"+
		"<Query>\r\n"+
		"<CmdType>Catalog</CmdType>\r\n"+
		"<SN>%d</SN>\r\n"+
		"<DeviceID>%s</DeviceID>\r\n"+
		"</Query>\r\n", sn, deviceID))
}

func parseResolution(raw string) (width, height *int) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	sep := ""
	for _, s := range []string{"*", "x", "X", "/"} {
		if strings.Contains(raw, s) {
			sep = s
			break
		}
	}
	if sep == "" {
		return nil, nil
	}
	parts := strings.Split(raw, sep)
	if len(parts) != 2 {
		return nil, nil
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return nil, nil
	}
	return &w, &h
}

func trimStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// validateXMLDocument walks the token stream before the struct decode so the depth
// and declaration checks apply to the whole document rather than to the subset that
// happens to map onto known fields.
func validateXMLDocument(document []byte) error {
	decoder := newXMLDecoder(document)
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("%w: %v", ErrXMLMalformed, err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if depth > maxInboundXMLDepth {
				return ErrXMLTooDeep
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			// encoding/xml never expands external entities, but a DOCTYPE is never
			// legitimate in a GB28181 body: rejecting it keeps the document shape
			// narrow instead of relying on that implementation detail.
			directive := strings.ToUpper(strings.TrimSpace(string(value)))
			if strings.HasPrefix(directive, "DOCTYPE") || strings.Contains(directive, "ENTITY") {
				return ErrXMLExternalEntity
			}
		case xml.ProcInst:
			if value.Target != "xml" {
				continue
			}
			charset := declaredCharset(string(value.Inst))
			if _, ok := allowedXMLCharsets[charset]; !ok {
				return fmt.Errorf("%w: %q", ErrXMLUnsupported, charset)
			}
		}
	}
	return nil
}

// newXMLDecoder builds a strict decoder that never re-interprets the charset: the
// buffer is already UTF-8 by construction, so the declaration is only inspected for
// plausibility (see validateXMLDocument). Without this, Go would reject a body that
// declares GB2312 before the declaration itself can be judged.
func newXMLDecoder(document []byte) *xml.Decoder {
	decoder := xml.NewDecoder(bytes.NewReader(document))
	decoder.Strict = true
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	return decoder
}

// declaredCharset extracts the encoding pseudo-attribute from an XML declaration.
func declaredCharset(instruction string) string {
	lower := strings.ToLower(instruction)
	const key = "encoding"
	index := strings.Index(lower, key)
	if index < 0 {
		return ""
	}
	rest := instruction[index+len(key):]
	rest = strings.TrimLeft(rest, " \t\r\n")
	if !strings.HasPrefix(rest, "=") {
		return ""
	}
	rest = strings.TrimLeft(rest[1:], " \t\r\n")
	if len(rest) < 2 || (rest[0] != '"' && rest[0] != '\'') {
		return ""
	}
	quote := rest[0]
	end := strings.IndexByte(rest[1:], quote)
	if end < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(rest[1 : 1+end]))
}
