package gb28181

import (
	"errors"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// FuzzDecodeXML hammers the inbound document parser with arbitrary bytes.
//
// The parser is the single place where untrusted device input enters the process, and
// its protections (size, depth, charset, entity) are all hand-written, which makes it
// the most likely spot for a panic or an unbounded allocation. Seeds cover the shapes
// seen in the S1 fixtures; the fuzzer is expected to run in CI with -fuzztime.
//
//	go test ./internal/gb28181 -run '^$' -fuzz FuzzDecodeXML -fuzztime 30s
func FuzzDecodeXML(f *testing.F) {
	f.Add([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><Notify><CmdType>Keepalive</CmdType>" +
		"<SN>1</SN><DeviceID>34020000001320000001</DeviceID><Status>OK</Status></Notify>"))
	f.Add([]byte("<Notify><CmdType>Keepalive</CmdType><SN>2</SN></Notify>"))
	f.Add([]byte(`<?xml version="1.0"?><!DOCTYPE Notify [<!ENTITY x SYSTEM "file:///etc/passwd">]><Notify/>`))
	f.Add([]byte("<Notify><A><B><C><D><E><F><G><H><I/></H>/</G></F></E></D></C></B></A></Notify>"))
	f.Add([]byte("<Notify>"))
	f.Add([]byte{})

	gb18030, err := simplifiedchinese.GB18030.NewEncoder().Bytes(
		[]byte("<Notify><CmdType>Keepalive</CmdType><Status>正常</Status></Notify>"))
	if err == nil {
		f.Add(gb18030)
	}

	known := []error{
		ErrXMLTooLarge,
		ErrXMLTooDeep,
		ErrXMLUnsupported,
		ErrXMLExternalEntity,
		ErrXMLMalformed,
	}

	f.Fuzz(func(t *testing.T, body []byte) {
		message, err := decodeXML(body)
		if err != nil {
			recognised := false
			for _, sentinel := range known {
				if errors.Is(err, sentinel) {
					recognised = true
					break
				}
			}
			if !recognised {
				// Every rejection must name one of the documented defences; an
				// anonymous error would be untranslatable into a SIP status.
				t.Fatalf("unclassified rejection for %q: %v", body, err)
			}
			if len(body) > maxInboundXMLBytes && !errors.Is(err, ErrXMLTooLarge) {
				t.Fatalf("oversized input (%d bytes) was not reported as too large: %v", len(body), err)
			}
			return
		}
		// A document that decodes must have respected the size cap before decoding.
		if len(body) > maxInboundXMLBytes {
			t.Fatalf("oversized input (%d bytes) decoded successfully", len(body))
		}
		if message.XMLName.Local == "" {
			t.Fatalf("decoded document without a root element: %q", body)
		}
	})
}
