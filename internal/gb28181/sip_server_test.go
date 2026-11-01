package gb28181

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

const (
	testDeviceID  = "34020000001320000001"
	testPassword  = "digest-secret"
	testSourceIP  = "192.168.1.64:5060"
	testCallID    = "3c2f9a1e-test-call"
	testTransport = "UDP"
)

// recordingTx is a synchronous sip.ServerTransaction that only records responses.
//
// It replaces sipgo's siptest recorder because that recorder is written from the
// transaction FSM goroutine and read from the test goroutine, which the race detector
// reports (correctly). Nothing here needs a real transaction: the handlers under test
// only call Respond.
type recordingTx struct {
	mu        sync.Mutex
	responses []*sip.Response
}

func newRecordingTx() *recordingTx { return &recordingTx{} }

func (t *recordingTx) Respond(res *sip.Response) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.responses = append(t.responses, res.Clone())
	return nil
}

func (t *recordingTx) Result() []*sip.Response {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sip.Response(nil), t.responses...)
}

func (t *recordingTx) Acks() <-chan *sip.Request          { return nil }
func (t *recordingTx) OnCancel(sip.FnTxCancel) bool       { return false }
func (t *recordingTx) Terminate()                         {}
func (t *recordingTx) OnTerminate(sip.FnTxTerminate) bool { return false }
func (t *recordingTx) Done() <-chan struct{}              { return nil }
func (t *recordingTx) Err() error                         { return nil }

// newTestIngress wires the ingress internals without binding sockets, so the request
// handlers can be driven directly. Sockets are covered by the framedConn tests and by
// the host's own lifecycle tests.
func newTestIngress(t *testing.T) (*sipIngress, *Store, *Runtime, *testClock, func()) {
	t.Helper()
	store, _, cleanup := setupStore(t)
	runtime := NewRuntime()
	start := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	clock := newTestClock(start)

	ingress := newSIPIngress(store, runtime, zap.NewNop(), defaultIngressLimits())
	ingress.cfg = enabledConfig()
	ingress.nonces = newNonceManager([]byte("test-secret"), time.Minute)
	ingress.nonces.now = clock.now
	ingress.limiter = newRateLimiter(ingress.limits.RateBurst, ingress.limits.RateRefill)
	ingress.limiter.now = clock.now
	ingress.devices = newDeviceStateMachine(deviceStateOptions{
		Store:             store,
		Runtime:           runtime,
		KeepaliveTimeout:  3 * time.Minute,
		RegistrationGrace: 10 * time.Second,
		Now:               clock.now,
	})
	return ingress, store, runtime, clock, cleanup
}

func newRegisterRequest(t *testing.T, deviceID string, expires *int) *sip.Request {
	t.Helper()
	realm := enabledConfig().SIPRealm
	req := sip.NewRequest(sip.REGISTER, sip.Uri{User: deviceID, Host: realm})

	viaParams := sip.NewParams()
	viaParams.Add("branch", sip.GenerateBranch())
	req.AppendHeader(&sip.ViaHeader{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       testTransport,
		Host:            "192.168.1.64",
		Port:            5060,
		Params:          viaParams,
	})
	req.AppendHeader(&sip.FromHeader{
		Address: sip.Uri{User: deviceID, Host: realm},
		Params:  sip.HeaderParams{{K: "tag", V: "from-tag"}},
	})
	req.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: deviceID, Host: realm}})
	contact := &sip.ContactHeader{Address: sip.Uri{User: deviceID, Host: "192.168.1.64", Port: 5060}}
	req.AppendHeader(contact)
	callID := sip.CallIDHeader(testCallID)
	req.AppendHeader(&callID)
	req.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.REGISTER})
	if expires != nil {
		req.AppendHeader(sip.NewHeader("Expires", fmt.Sprintf("%d", *expires)))
	}
	req.SetSource(testSourceIP)
	req.SetTransport(testTransport)
	return req
}

func newMessageRequest(t *testing.T, deviceID string, body []byte) *sip.Request {
	t.Helper()
	req := newRegisterRequest(t, deviceID, nil)
	req.Method = sip.MESSAGE
	req.AppendHeader(sip.NewHeader("Content-Type", "Application/MANSCDP+xml"))
	req.SetBody(body)
	return req
}

func withAuthorization(t *testing.T, req *sip.Request, password, nonce string) {
	t.Helper()
	deviceID := req.From().Address.User
	realm := enabledConfig().SIPRealm
	uri := "sip:" + realm
	response := testDigest(deviceID, realm, password, string(req.Method), uri, nonce, "", "", "")
	req.AppendHeader(sip.NewHeader("Authorization", fmt.Sprintf(
		`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=MD5`,
		deviceID, realm, nonce, uri, response)))
}

// challengeNonce runs one request through the handler and returns the nonce from the
// 401 response.
func challengeNonce(t *testing.T, ingress *sipIngress, req *sip.Request) string {
	t.Helper()
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)

	responses := recorder.Result()
	if len(responses) != 1 || responses[0].StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected one 401 challenge, got %+v", responses)
	}
	header := responses[0].GetHeader("WWW-Authenticate")
	if header == nil {
		t.Fatal("the 401 response must carry WWW-Authenticate")
	}
	value := header.Value()
	const marker = `nonce="`
	start := strings.Index(value, marker)
	if start < 0 {
		t.Fatalf("unexpected challenge %q", value)
	}
	rest := value[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("unterminated nonce in %q", value)
	}
	return rest[:end]
}

func singleResponse(t *testing.T, recorder *recordingTx) *sip.Response {
	t.Helper()
	responses := recorder.Result()
	if len(responses) != 1 {
		t.Fatalf("expected exactly one response, got %d", len(responses))
	}
	return responses[0]
}

func TestRegisterRejectsUnprovisionedDevice(t *testing.T) {
	ingress, _, _, _, cleanup := newTestIngress(t)
	defer cleanup()

	req := newRegisterRequest(t, testDeviceID, nil)
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)

	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusForbidden {
		t.Fatalf("expected 403 for an unknown device, got %d", response.StatusCode)
	}
}

func TestRegisterChallengesAndAcceptsValidDigest(t *testing.T) {
	ingress, store, runtime, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	nonce := challengeNonce(t, ingress, newRegisterRequest(t, testDeviceID, nil))

	req := newRegisterRequest(t, testDeviceID, nil)
	withAuthorization(t, req, testPassword, nonce)
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)

	response := singleResponse(t, recorder)
	if response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 for a valid digest, got %d", response.StatusCode)
	}
	if header := response.GetHeader("Expires"); header == nil || header.Value() != "3600" {
		t.Fatalf("expected the granted lease to be echoed, got %v", header)
	}
	if header := response.GetHeader("Contact"); header == nil {
		t.Fatal("a 200 OK to REGISTER must echo Contact")
	}
	to := response.To()
	if to == nil || !to.Params.Has("tag") {
		t.Fatal("a final response to REGISTER must carry a To tag")
	}

	device, err := store.GetDevice(context.Background(), testDeviceID)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if device.State != DeviceStateOnline {
		t.Fatalf("expected the device online after REGISTER, got %q", device.State)
	}
	if device.RemoteAddr == nil || *device.RemoteAddr != testSourceIP {
		t.Fatalf("expected the source address to be persisted, got %v", device.RemoteAddr)
	}
	if snapshot := runtime.Snapshot(); snapshot.Devices.Online != 1 {
		t.Fatalf("expected one online device in the snapshot, got %+v", snapshot.Devices)
	}
}

// countingCipher delegates to fakeCipher and counts decryptions, so a test can
// assert that a code path did not materialize the stored Digest secret.
type countingCipher struct {
	fakeCipher
	decrypts int
}

func (c *countingCipher) Decrypt(ciphertext []byte, aad string) ([]byte, error) {
	c.decrypts++
	return c.fakeCipher.Decrypt(ciphertext, aad)
}

// TestRegisterChallengesWithoutDecryptingTheSecret pins the order of the allowlist
// check: an unauthenticated REGISTER must prove the device exists and then get the 401,
// without paying a decrypt per datagram from anyone who can spoof a source address.
func TestRegisterChallengesWithoutDecryptingTheSecret(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	cipher := &countingCipher{}
	store.cipher = cipher

	req := newRegisterRequest(t, testDeviceID, nil)
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)

	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401 for an unauthenticated REGISTER, got %d", response.StatusCode)
	}
	if cipher.decrypts != 0 {
		t.Fatalf("an unauthenticated REGISTER must not decrypt the stored secret, got %d decryptions", cipher.decrypts)
	}
}

func TestRegisterRejectsWrongPassword(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	nonce := challengeNonce(t, ingress, newRegisterRequest(t, testDeviceID, nil))

	req := newRegisterRequest(t, testDeviceID, nil)
	withAuthorization(t, req, "wrong-password", nonce)
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)

	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong password, got %d", response.StatusCode)
	}
	if device, err := store.GetDevice(context.Background(), testDeviceID); err != nil {
		t.Fatalf("read device: %v", err)
	} else if device.State != DeviceStateOffline {
		t.Fatalf("a failed authentication must not register the device, got %q", device.State)
	}
}

func TestRegisterReportsStaleNonce(t *testing.T) {
	ingress, store, _, clock, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	nonce := challengeNonce(t, ingress, newRegisterRequest(t, testDeviceID, nil))
	clock.advance(2 * time.Minute)

	req := newRegisterRequest(t, testDeviceID, nil)
	withAuthorization(t, req, testPassword, nonce)
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)

	response := singleResponse(t, recorder)
	if response.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401 for an expired nonce, got %d", response.StatusCode)
	}
	header := response.GetHeader("WWW-Authenticate")
	if header == nil || !strings.Contains(header.Value(), "stale=true") {
		t.Fatalf("expected stale=true so the device retries immediately, got %v", header)
	}
}

func TestRegisterUnregisterClearsState(t *testing.T) {
	ingress, store, runtime, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	nonce := challengeNonce(t, ingress, newRegisterRequest(t, testDeviceID, nil))
	register := newRegisterRequest(t, testDeviceID, nil)
	withAuthorization(t, register, testPassword, nonce)
	recorder := newRecordingTx()
	ingress.handleRegister(register, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 on the initial REGISTER, got %d", response.StatusCode)
	}

	zero := 0
	unregister := newRegisterRequest(t, testDeviceID, &zero)
	withAuthorization(t, unregister, testPassword, nonce)
	recorder = newRecordingTx()
	ingress.handleRegister(unregister, recorder)

	response := singleResponse(t, recorder)
	if response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 on EXPIRES=0, got %d", response.StatusCode)
	}
	if header := response.GetHeader("Expires"); header == nil || header.Value() != "0" {
		t.Fatalf("expected Expires: 0 in the answer, got %v", header)
	}
	device, err := store.GetDevice(context.Background(), testDeviceID)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if device.State != DeviceStateOffline || device.RemoteAddr != nil {
		t.Fatalf("expected an offline device without a remote address, got %q %v", device.State, device.RemoteAddr)
	}
	if snapshot := runtime.Snapshot(); snapshot.Devices.Online != 0 {
		t.Fatalf("expected zero online devices, got %+v", snapshot.Devices)
	}
}

func TestMessageKeepaliveUpdatesLiveness(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	nonce := challengeNonce(t, ingress, newRegisterRequest(t, testDeviceID, nil))
	register := newRegisterRequest(t, testDeviceID, nil)
	withAuthorization(t, register, testPassword, nonce)
	recorder := newRecordingTx()
	ingress.handleRegister(register, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 on REGISTER, got %d", response.StatusCode)
	}

	body := []byte("<Notify><CmdType>Keepalive</CmdType><SN>3</SN>" +
		"<DeviceID>" + testDeviceID + "</DeviceID><Status>OK</Status></Notify>")
	message := newMessageRequest(t, testDeviceID, body)
	recorder = newRecordingTx()
	ingress.handleMessage(message, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 for a keepalive, got %d", response.StatusCode)
	}

	device, err := store.GetDevice(context.Background(), testDeviceID)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if device.LastKeepaliveAt == nil {
		t.Fatal("expected the keepalive to be persisted")
	}
	if device.State != DeviceStateOnline {
		t.Fatalf("expected the device to stay online, got %q", device.State)
	}
}

func TestMessageRejectsHostileBodies(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	oversized := []byte(strings.Repeat("A", ingress.limits.MaxXMLBytes+1))
	malformed := []byte("<Notify><CmdType>Keepalive</CmdType>")

	tests := []struct {
		name string
		body []byte
		want int
	}{
		{name: "oversized", body: oversized, want: sip.StatusRequestEntityTooLarge},
		{name: "malformed", body: malformed, want: sip.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := newMessageRequest(t, testDeviceID, test.body)
			recorder := newRecordingTx()
			ingress.handleMessage(req, recorder)
			if response := singleResponse(t, recorder); response.StatusCode != test.want {
				t.Fatalf("expected %d, got %d", test.want, response.StatusCode)
			}
		})
	}
}

func TestRegisterRateLimited(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	// Once the burst is spent the next attempt must be refused even before
	// authentication, so a storm cannot force password verification work.
	for i := range ingress.limits.RateBurst {
		req := newRegisterRequest(t, testDeviceID, nil)
		recorder := newRecordingTx()
		ingress.handleRegister(req, recorder)
		if response := singleResponse(t, recorder); response.StatusCode != sip.StatusUnauthorized {
			t.Fatalf("attempt %d must reach the challenge, got %d", i, response.StatusCode)
		}
	}
	req := newRegisterRequest(t, testDeviceID, nil)
	recorder := newRecordingTx()
	ingress.handleRegister(req, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusServiceUnavailable {
		t.Fatalf("expected 503 once the burst is spent, got %d", response.StatusCode)
	}
}

// TestIngressServesREGISTEROverUDP covers the socket path: a real datagram in, a real
// 401 out. It is the only test that proves the sipgo wiring (listener, transport,
// transaction layer) is connected, because the handler tests bypass all of it.
func TestIngressServesREGISTEROverUDP(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	cfg := enabledConfig()
	cfg.SIPListen = "127.0.0.1:0"
	cfg.SIPTransport = TransportUDP

	runtime := NewRuntime()
	ingress := newSIPIngress(store, runtime, zap.NewNop(), defaultIngressLimits())
	// A received registration must not depend on the media address in a unit test.
	if err := ingress.Start(context.Background(), cfg, "127.0.0.1"); err != nil {
		t.Fatalf("start ingress: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ingress.Stop(stopCtx); err != nil {
			t.Fatalf("stop ingress: %v", err)
		}
	}()

	if ingress.udpConn == nil {
		t.Fatal("expected a bound UDP listener")
	}
	serverAddr := ingress.udpConn.LocalAddr().String()

	client, err := net.Dial("udp", serverAddr)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer func() { _ = client.Close() }()

	// REGISTER from a source the server can see; the From identity is the allowlist key.
	request := newRegisterRequest(t, testDeviceID, nil)
	request.RemoveHeader("Via")
	viaParams := sip.NewParams()
	viaParams.Add("branch", sip.GenerateBranch())
	request.PrependHeader(&sip.ViaHeader{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       "UDP",
		Host:            "127.0.0.1",
		Port:            client.LocalAddr().(*net.UDPAddr).Port,
		Params:          viaParams,
	})

	if _, err := client.Write([]byte(request.String())); err != nil {
		t.Fatalf("send register: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buffer := make([]byte, 4096)
	n, err := client.Read(buffer)
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}

	message, err := sip.NewParser().ParseSIP(buffer[:n])
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	response, ok := message.(*sip.Response)
	if !ok {
		t.Fatalf("expected a SIP response, got %T", message)
	}
	if response.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401 for an unauthenticated REGISTER, got %d", response.StatusCode)
	}
	if response.GetHeader("WWW-Authenticate") == nil {
		t.Fatal("the challenge must carry WWW-Authenticate")
	}
}

// A second Start must be refused rather than silently accepted: the caller asked the
// running process to adopt a configuration it cannot adopt, and answering "ok" would
// hide the restart the operator still has to do. Stop stays idempotent.
func TestIngressStartTwiceAndStopTwice(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	cfg := enabledConfig()
	cfg.SIPListen = "127.0.0.1:0"
	cfg.SIPTransport = TransportUDP

	ingress := newSIPIngress(store, NewRuntime(), zap.NewNop(), defaultIngressLimits())
	if err := ingress.Start(context.Background(), cfg, "127.0.0.1"); err != nil {
		t.Fatalf("start ingress: %v", err)
	}
	err := ingress.Start(context.Background(), cfg, "127.0.0.1")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeRuntimeNotReady {
		t.Fatalf("expected %s for a second start, got %v", CodeRuntimeNotReady, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ingress.Stop(ctx); err != nil {
		t.Fatalf("stop ingress: %v", err)
	}
	if err := ingress.Stop(ctx); err != nil {
		t.Fatalf("a second stop must be a no-op: %v", err)
	}
}

// TestReadFilterDropsOversizedDatagramWithoutStoppingUDP pins the difference between
// "drop this packet" and "this transport is broken": sipgo stops the UDP read loop on
// a filter error, so an oversized datagram must be dropped with a nil error.
func TestReadFilterDropsOversizedDatagramWithoutStoppingUDP(t *testing.T) {
	ingress, _, _, _, cleanup := newTestIngress(t)
	defer cleanup()

	remote := &net.UDPAddr{IP: net.ParseIP("192.168.1.64"), Port: 5060}
	props := sip.TransportReadProps{
		Transport:  "UDP",
		LocalAddr:  &net.UDPAddr{IP: net.IPv4zero, Port: 5060},
		RemoteAddr: remote,
	}

	small := make([]byte, ingress.limits.MaxSIPMessageBytes)
	if filtered, err := ingress.readFilter(props, small); err != nil || len(filtered) != len(small) {
		t.Fatalf("a datagram within the cap must pass untouched: len=%d err=%v", len(filtered), err)
	}

	oversized := make([]byte, ingress.limits.MaxSIPMessageBytes+1)
	filtered, err := ingress.readFilter(props, oversized)
	if err != nil {
		t.Fatalf("an oversized datagram must not report a transport error: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("an oversized datagram must be dropped, got %d bytes through", len(filtered))
	}
	if dropped := ingress.Stats().OversizedMessages; dropped != 1 {
		t.Fatalf("expected the drop to be counted, got %d", dropped)
	}

	// TCP chunks are exempt: framedConn enforces the per-message cap there.
	props.Transport = "TCP"
	if _, err := ingress.readFilter(props, oversized); err != nil {
		t.Fatalf("TCP chunks must pass the filter: %v", err)
	}
}

func TestMessageCatalogResponseCannotCompleteAnotherDevicesSync(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()

	deviceA := testDeviceID
	deviceB := "34020000001320000002"
	provisionDevice(t, store, deviceA, testPassword)
	provisionDevice(t, store, deviceB, testPassword)

	const existingChannelID = "34020000001310000009"
	if err := store.SyncChannels(context.Background(), deviceB, []Channel{{
		DeviceID:  deviceB,
		ChannelID: existingChannelID,
		Name:      "Existing channel",
	}}); err != nil {
		t.Fatalf("seed device B catalog: %v", err)
	}

	sender := &mockSipSender{}
	catalogs := NewCatalogManager(sender, store, zap.NewNop())
	catalogs.timeout = 300 * time.Millisecond
	ingress.catalogs = catalogs
	querySN := make(chan int, 1)
	sender.onSend = func(_ Device, body []byte) {
		message, err := decodeXML(body)
		if err != nil {
			t.Errorf("decode outbound catalog query: %v", err)
			return
		}
		querySN <- message.SN
	}

	syncDone := make(chan error, 1)
	go func() {
		_, err := catalogs.Sync(context.Background(), Device{DeviceID: deviceB})
		syncDone <- err
	}()

	var sn int
	select {
	case sn = <-querySN:
	case err := <-syncDone:
		t.Fatalf("catalog sync exited before query was sent: %v", err)
	case <-time.After(time.Second):
		t.Fatal("catalog query was not sent")
	}

	const forgedChannelID = "34020000001310000010"
	body := []byte(fmt.Sprintf(
		"<Response><CmdType>Catalog</CmdType><SN>%d</SN><DeviceID>%s</DeviceID><SumNum>1</SumNum><DeviceList><Item><DeviceID>%s</DeviceID><Name>Forged</Name></Item></DeviceList></Response>",
		sn, deviceB, forgedChannelID,
	))
	request := newMessageRequest(t, deviceA, body)
	recorder := newRecordingTx()
	ingress.handleMessage(request, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("malformedly attributed Catalog response should be acknowledged, got %d", response.StatusCode)
	}

	select {
	case err := <-syncDone:
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Code != CodeCatalogTimeout {
			t.Fatalf("device A must not complete device B's sync, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("device B catalog sync did not time out after the forged response")
	}

	channels, err := store.ListChannels(context.Background(), deviceB)
	if err != nil {
		t.Fatalf("read device B catalog: %v", err)
	}
	if len(channels) != 1 || channels[0].ChannelID != existingChannelID {
		t.Fatalf("forged response changed device B catalog: %+v", channels)
	}
}

// TestMessageFromUnexpectedSourceIsRejected stops a spoofed datagram from refreshing
// another device's liveness (and therefore from hiding a dead link).
func TestMessageFromUnexpectedSourceIsRejected(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	nonce := challengeNonce(t, ingress, newRegisterRequest(t, testDeviceID, nil))
	register := newRegisterRequest(t, testDeviceID, nil)
	withAuthorization(t, register, testPassword, nonce)
	recorder := newRecordingTx()
	ingress.handleRegister(register, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 on REGISTER, got %d", response.StatusCode)
	}

	body := []byte("<Notify><CmdType>Keepalive</CmdType><SN>4</SN>" +
		"<DeviceID>" + testDeviceID + "</DeviceID></Notify>")
	message := newMessageRequest(t, testDeviceID, body)
	message.SetSource("192.168.1.99:5060")
	recorder = newRecordingTx()
	ingress.handleMessage(message, recorder)

	if response := singleResponse(t, recorder); !isClientError(response.StatusCode) {
		t.Fatalf("expected the spoofed source to be refused, got %d", response.StatusCode)
	}
	device, err := store.GetDevice(context.Background(), testDeviceID)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if device.LastKeepaliveAt != nil {
		t.Fatal("a keepalive from an unexpected source must not be recorded")
	}

	// The registered source still works.
	message = newMessageRequest(t, testDeviceID, body)
	recorder = newRecordingTx()
	ingress.handleMessage(message, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 from the registered source, got %d", response.StatusCode)
	}
}

func isClientError(status int) bool { return status >= 400 && status < 500 }

// freeUDPAddress reserves and immediately releases a loopback UDP port so a stored
// configuration can name it. The window between release and bind is why this is only
// used by tests.
func freeUDPAddress(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve udp port: %v", err)
	}
	addr := conn.LocalAddr().String()
	if err := conn.Close(); err != nil {
		t.Fatalf("release udp port: %v", err)
	}
	return addr
}

func TestIngressBindFailureRollsBackPartialStart(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	// Occupy the TCP port so the TCP bind inside a "both" ingress fails after UDP has
	// already bound: the UDP socket must not stay open behind a failed start.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy tcp port: %v", err)
	}
	defer func() { _ = occupied.Close() }()

	cfg := enabledConfig()
	cfg.SIPListen = occupied.Addr().String()
	cfg.SIPTransport = TransportBoth

	ingress := newSIPIngress(store, NewRuntime(), zap.NewNop(), defaultIngressLimits())
	if err := ingress.Start(context.Background(), cfg, "127.0.0.1"); err == nil {
		t.Fatal("expected the TCP bind to fail")
	}
	if ingress.udpConn != nil {
		t.Fatal("the UDP listener must be released when a later bind fails")
	}
	// A failed start must leave nothing for Stop to do.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ingress.Stop(ctx); err != nil {
		t.Fatalf("stop after a failed start must be a no-op: %v", err)
	}
}

func TestUnhandledMethodsAndOptions(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	options := newRegisterRequest(t, testDeviceID, nil)
	options.Method = sip.OPTIONS
	recorder := newRecordingTx()
	ingress.handleOptions(options, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 for OPTIONS, got %d", response.StatusCode)
	}

	// INVITE is out of scope for S1 but must still be answered, otherwise devices
	// retransmit until they give up.
	invite := newRegisterRequest(t, testDeviceID, nil)
	invite.Method = sip.INVITE
	recorder = newRecordingTx()
	ingress.handleUnsupported(invite, recorder)
	if response := singleResponse(t, recorder); response.StatusCode != sip.StatusNotImplemented {
		t.Fatalf("expected 501 for an unsupported method, got %d", response.StatusCode)
	}
}

// TestLifecycleStartsTheRealIngress is the seam test: the lifecycle, not just the
// ingress unit, must reach state=running with the real SIP implementation and release
// the socket on stop.
func TestLifecycleStartsTheRealIngress(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()

	cfg := enabledConfig()
	// The stored configuration is validated, so the listener needs a concrete port.
	cfg.SIPListen = freeUDPAddress(t)
	cfg.SIPTransport = TransportUDP
	cfg.MediaAdvertisedIP = "127.0.0.1"
	if _, err := store.SaveConfig(context.Background(), cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runtime := NewRuntime()
	ingress := newSIPIngress(store, runtime, zap.NewNop(), defaultIngressLimits())
	manager := NewLifecycleManager(LifecycleOptions{
		Repo:    store,
		Runtime: runtime,
		Ingress: ingress,
		Addrs:   fixedAddrs("127.0.0.1"),
	})

	ctx := context.Background()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("lifecycle start failed: %v", err)
	}
	if status := runtime.Status(); status.State != RuntimeStateRunning {
		t.Fatalf("expected state %q, got %q (detail %q)", RuntimeStateRunning, status.State, status.Detail)
	}
	if ingress.udpConn == nil {
		t.Fatal("expected the ingress to hold a bound UDP socket")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Stop(stopCtx); err != nil {
		t.Fatalf("lifecycle stop failed: %v", err)
	}
	if status := runtime.Status(); status.State != RuntimeStateDisabled {
		t.Fatalf("expected state %q after stop, got %q", RuntimeStateDisabled, status.State)
	}
	if ingress.udpConn != nil {
		t.Fatal("the UDP socket must be released on stop")
	}
}

// TestPeerDeviceRegistersOverUDP drives the running ingress with sipgo's own client
// and its own Digest implementation (github.com/icholy/digest). That makes the peer an
// independent implementation of the REGISTER/Digest handshake: our server is not
// validated against our own digest code.
//
// This is the closest substitute for a vendor device that can run in CI; see the
// "无真机验收" section of the task's implement.md for what it does and does not cover.
func TestPeerDeviceRegistersOverUDP(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	cfg := enabledConfig()
	cfg.SIPListen = freeUDPAddress(t)
	cfg.SIPTransport = TransportUDP
	runtime := NewRuntime()
	ingress := newSIPIngress(store, runtime, zap.NewNop(), defaultIngressLimits())
	if err := ingress.Start(context.Background(), cfg, "127.0.0.1"); err != nil {
		t.Fatalf("start ingress: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ingress.Stop(stopCtx); err != nil {
			t.Fatalf("stop ingress: %v", err)
		}
	}()
	serverAddr := ingress.udpConn.LocalAddr().String()

	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatalf("create peer ua: %v", err)
	}
	defer func() { _ = ua.Close() }()
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname("127.0.0.1"))
	if err != nil {
		t.Fatalf("create peer client: %v", err)
	}

	realm := cfg.SIPRealm
	request := sip.NewRequest(sip.REGISTER, sip.Uri{Host: hostOnly(serverAddr), Port: portOf(t, serverAddr)})
	request.AppendHeader(&sip.FromHeader{Address: sip.Uri{User: testDeviceID, Host: realm}})
	request.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: testDeviceID, Host: realm}})
	callID := sip.CallIDHeader("peer-register-" + testDeviceID)
	request.AppendHeader(&callID)
	request.AppendHeader(&sip.CSeqHeader{SeqNo: 1, MethodName: sip.REGISTER})
	request.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: testDeviceID, Host: "127.0.0.1", Port: 50620}})
	request.AppendHeader(sip.NewHeader("Expires", "3600"))
	request.SetTransport("UDP")
	request.SetDestination(serverAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	challenge, err := client.Do(ctx, request)
	if err != nil {
		t.Fatalf("send register: %v", err)
	}
	if challenge.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", challenge.StatusCode)
	}

	accepted, err := client.DoDigestAuth(ctx, request, challenge, sipgo.DigestAuth{
		Username: testDeviceID,
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("authenticated register: %v", err)
	}
	if accepted.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 after the digest handshake, got %d", accepted.StatusCode)
	}

	device, err := store.GetDevice(context.Background(), testDeviceID)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if device.State != DeviceStateOnline {
		t.Fatalf("expected the peer device online, got %q", device.State)
	}
	if snapshot := runtime.Snapshot(); snapshot.Devices.Online != 1 {
		t.Fatalf("expected one online device, got %+v", snapshot.Devices)
	}

	// Rotating the secret must break authentication for the same peer: this proves the
	// password is really read from the database per request rather than cached.
	newPassword := "rotated-secret"
	if _, err := store.UpsertDevice(context.Background(), DeviceUpsert{
		DeviceID: testDeviceID,
		Password: &newPassword,
	}); err != nil {
		t.Fatalf("rotate password: %v", err)
	}
	stale, err := client.DoDigestAuth(ctx, request, challenge, sipgo.DigestAuth{
		Username: testDeviceID,
		Password: testPassword,
	})
	if err != nil {
		t.Fatalf("stale-credential attempt failed to complete: %v", err)
	}
	if stale.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401 after the password rotated, got %d", stale.StatusCode)
	}
}

func portOf(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split %q: %v", address, err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	return value
}

// TestPeerDeviceRegistersOverTCP covers the stream transport: framing across reads,
// the same Digest handshake, and connection reuse for the second request.
func TestPeerDeviceRegistersOverTCP(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	cfg := enabledConfig()
	cfg.SIPListen = freeTCPAddress(t)
	cfg.SIPTransport = TransportTCP
	runtime := NewRuntime()
	ingress := newSIPIngress(store, runtime, zap.NewNop(), defaultIngressLimits())
	if err := ingress.Start(context.Background(), cfg, "127.0.0.1"); err != nil {
		t.Fatalf("start ingress: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ingress.Stop(stopCtx); err != nil {
			t.Fatalf("stop ingress: %v", err)
		}
	}()

	conn, err := net.Dial("tcp", cfg.SIPListen)
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	defer func() { _ = conn.Close() }()

	localPort := conn.LocalAddr().(*net.TCPAddr).Port
	newTCPRegister := func(cseq uint32, authorization string) *sip.Request {
		request := sip.NewRequest(sip.REGISTER, sip.Uri{
			Host: hostOnly(cfg.SIPListen),
			Port: portOf(t, cfg.SIPListen),
		})
		viaParams := sip.NewParams()
		viaParams.Add("branch", sip.GenerateBranch())
		request.AppendHeader(&sip.ViaHeader{
			ProtocolName: "SIP", ProtocolVersion: "2.0", Transport: "TCP",
			Host: "127.0.0.1", Port: localPort, Params: viaParams,
		})
		request.AppendHeader(&sip.FromHeader{Address: sip.Uri{User: testDeviceID, Host: cfg.SIPRealm}})
		request.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: testDeviceID, Host: cfg.SIPRealm}})
		callID := sip.CallIDHeader("peer-tcp-" + testDeviceID)
		request.AppendHeader(&callID)
		request.AppendHeader(&sip.CSeqHeader{SeqNo: cseq, MethodName: sip.REGISTER})
		request.AppendHeader(&sip.ContactHeader{Address: sip.Uri{User: testDeviceID, Host: "127.0.0.1", Port: localPort}})
		request.AppendHeader(sip.NewHeader("Expires", "3600"))
		request.AppendHeader(sip.NewHeader("Content-Length", "0"))
		if authorization != "" {
			request.AppendHeader(sip.NewHeader("Authorization", authorization))
		}
		return request
	}

	write := func(request *sip.Request) {
		t.Helper()
		if _, err := conn.Write([]byte(request.String())); err != nil {
			t.Fatalf("write register over tcp: %v", err)
		}
	}
	read := func() *sip.Response {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buffer := make([]byte, 0, 4096)
		chunk := make([]byte, 4096)
		for {
			n, err := conn.Read(chunk)
			if err != nil {
				t.Fatalf("read response over tcp: %v", err)
			}
			buffer = append(buffer, chunk[:n]...)
			if bytes.Contains(buffer, []byte("\r\n\r\n")) {
				break
			}
		}
		message, err := sip.NewParser().ParseSIP(buffer)
		if err != nil {
			t.Fatalf("parse response: %v", err)
		}
		response, ok := message.(*sip.Response)
		if !ok {
			t.Fatalf("expected a SIP response, got %T", message)
		}
		return response
	}

	write(newTCPRegister(1, ""))
	challenge := read()
	if challenge.StatusCode != sip.StatusUnauthorized {
		t.Fatalf("expected 401 over TCP, got %d", challenge.StatusCode)
	}
	nonce := nonceFromChallenge(t, challenge)

	uri := "sip:" + cfg.SIPRealm
	response := testDigest(testDeviceID, cfg.SIPRealm, testPassword, "REGISTER", uri, nonce, "", "", "")
	authorization := fmt.Sprintf(
		`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=MD5`,
		testDeviceID, cfg.SIPRealm, nonce, uri, response)
	write(newTCPRegister(2, authorization))

	accepted := read()
	if accepted.StatusCode != sip.StatusOK {
		t.Fatalf("expected 200 over TCP after the digest handshake, got %d", accepted.StatusCode)
	}
	device, err := store.GetDevice(context.Background(), testDeviceID)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if device.State != DeviceStateOnline {
		t.Fatalf("expected the TCP peer online, got %q", device.State)
	}
	if device.Transport != TransportTCP {
		t.Fatalf("expected the transport to be persisted as tcp, got %q", device.Transport)
	}

	// Outbound signalling must ride the flow the device registered on. This is the S1
	// gate that says "a TCP registration is usable for device-directed requests": the
	// persisted remote_addr is an ephemeral port that is only reachable while this very
	// connection is alive.
	readRequest := func() *sip.Request {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		buffer := make([]byte, 0, 4096)
		chunk := make([]byte, 4096)
		for {
			if len(buffer) > 0 {
				if message, err := sip.NewParser().ParseSIP(buffer); err == nil {
					request, ok := message.(*sip.Request)
					if !ok {
						t.Fatalf("expected a SIP request, got %T", message)
					}
					return request
				}
			}
			n, readErr := conn.Read(chunk)
			if readErr != nil {
				t.Fatalf("read request over tcp: %v (buffered %q)", readErr, buffer)
			}
			buffer = append(buffer, chunk[:n]...)
		}
	}

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- ingress.SendMessage(context.Background(), *device, []byte(buildCatalogQuery(1, testDeviceID)))
	}()

	outbound := readRequest()
	if outbound.Method != sip.MESSAGE {
		t.Fatalf("expected an outbound MESSAGE on the registered flow, got %s", outbound.Method)
	}
	if !bytes.Contains(outbound.Body(), []byte("<CmdType>Catalog</CmdType>")) {
		t.Fatalf("unexpected outbound catalog query body: %q", outbound.Body())
	}
	if _, err := conn.Write([]byte(sip.NewResponseFromRequest(outbound, sip.StatusOK, "OK", nil).String())); err != nil {
		t.Fatalf("answer outbound message: %v", err)
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("SendMessage over the registered flow: %v", err)
	}
}

func TestSendMessageRejectsClosedStreamFlow(t *testing.T) {
	store, _, cleanup := setupStore(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	cfg := enabledConfig()
	cfg.SIPListen = freeTCPAddress(t)
	cfg.SIPTransport = TransportTCP
	ingress := newSIPIngress(store, NewRuntime(), zap.NewNop(), defaultIngressLimits())
	if err := ingress.Start(context.Background(), cfg, "127.0.0.1"); err != nil {
		t.Fatalf("start ingress: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ingress.Stop(stopCtx); err != nil {
			t.Fatalf("stop ingress: %v", err)
		}
	}()

	// A persisted address whose flow is gone: dialing it would hit an ephemeral port the
	// device closed, which shows up as an unexplained timeout instead of "register again".
	stale := "127.0.0.1:9"
	err := ingress.SendMessage(context.Background(), Device{
		DeviceID:   testDeviceID,
		Transport:  TransportTCP,
		RemoteAddr: &stale,
	}, []byte(buildCatalogQuery(1, testDeviceID)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeDeviceFlowClosed {
		t.Fatalf("expected %s, got %v", CodeDeviceFlowClosed, err)
	}
}

func nonceFromChallenge(t *testing.T, response *sip.Response) string {
	t.Helper()
	header := response.GetHeader("WWW-Authenticate")
	if header == nil {
		t.Fatal("expected WWW-Authenticate in the challenge")
	}
	value := header.Value()
	const marker = `nonce="`
	start := strings.Index(value, marker)
	if start < 0 {
		t.Fatalf("unexpected challenge %q", value)
	}
	rest := value[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("unterminated nonce in %q", value)
	}
	return rest[:end]
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release tcp port: %v", err)
	}
	return addr
}

// newTestIngressWithClient wires the ingress internals plus a real sipgo client, which
// SendMessage needs. Nothing is bound to a socket: the outbound path is exercised
// against an explicit destination.
func newTestIngressWithClient(t *testing.T) (*sipIngress, *Store, func()) {
	t.Helper()
	store, _, cleanup := setupStore(t)

	ua, err := sipgo.NewUA()
	if err != nil {
		t.Fatalf("create ua: %v", err)
	}
	client, err := sipgo.NewClient(ua, sipgo.WithClientHostname("127.0.0.1"))
	if err != nil {
		closeQuietly(zap.NewNop(), ua, "test ua")
		cleanup()
		t.Fatalf("create client: %v", err)
	}

	ingress := newSIPIngress(store, NewRuntime(), zap.NewNop(), defaultIngressLimits())
	ingress.cfg = enabledConfig()
	if err := ingress.initialize(); err != nil {
		closeQuietly(zap.NewNop(), ua, "test ua")
		cleanup()
		t.Fatalf("initialize ingress: %v", err)
	}
	ingress.ua, ingress.client = ua, client

	return ingress, store, func() {
		ingress.closeStack()
		cleanup()
	}
}

// The Request-URI must name the device's address, not the platform's realm: devices
// route on the request line, and `sip:<device>@<platform-id>` is not an address any
// peer can resolve.
func TestBuildMessageRequestAddressesTheDevice(t *testing.T) {
	const (
		realm    = "3402000000"
		serverID = "34020000002000000001"
		dest     = "192.168.1.64:5060"
	)
	device := Device{DeviceID: testDeviceID, RemoteAddr: stringPtr(dest)}

	recipient, err := recipientURI(device)
	if err != nil {
		t.Fatalf("recipientURI failed: %v", err)
	}
	req := buildMessageRequest(device, recipient, realm, serverID, []byte{1, 2, 3, 4, 5, 6, 7, 8}, []byte("<Query/>"))

	startLine := strings.SplitN(req.String(), "\r\n", 2)[0]
	want := "MESSAGE sip:" + testDeviceID + "@" + dest + " SIP/2.0"
	if startLine != want {
		t.Fatalf("expected request line %q, got %q", want, startLine)
	}
	if to := req.To().Address.String(); !strings.Contains(to, "@"+realm) {
		t.Fatalf("the To header keeps the realm identity, got %q", to)
	}
	if from := req.From().Address.String(); !strings.Contains(from, serverID) {
		t.Fatalf("expected the platform identity in From, got %q", from)
	}
}

func TestRecipientURIRejectsAnUnusableRemoteAddress(t *testing.T) {
	tests := []struct {
		name string
		addr *string
	}{
		{name: "missing", addr: nil},
		{name: "empty", addr: stringPtr("")},
		{name: "no port", addr: stringPtr("192.168.1.64")},
		{name: "bad port", addr: stringPtr("192.168.1.64:not-a-port")},
		{name: "port out of range", addr: stringPtr("192.168.1.64:0")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := recipientURI(Device{DeviceID: testDeviceID, RemoteAddr: test.addr}); err == nil {
				t.Fatal("expected an error for an unusable remote address")
			}
		})
	}
}

// A device that challenges a platform request is a capability gap, not a network
// failure: the operator must be told which one it is.
func TestSendMessageReportsAChallengingDeviceAndLimitsOutbound(t *testing.T) {
	ingress, store, cleanup := newTestIngressWithClient(t)
	defer cleanup()
	provisionDevice(t, store, testDeviceID, testPassword)

	// The device side: a real sipgo UAS that answers every MESSAGE with the status the
	// current phase asks for.
	var phase atomic.Int32
	responded := make(chan int, 16)
	peerUA, err := sipgo.NewUA()
	if err != nil {
		t.Fatalf("create peer ua: %v", err)
	}
	peerServer, err := sipgo.NewServer(peerUA)
	if err != nil {
		t.Fatalf("create peer server: %v", err)
	}
	peerServer.OnMessage(func(req *sip.Request, tx sip.ServerTransaction) {
		status := int(phase.Load())
		res := sip.NewResponseFromRequest(req, status, "test", nil)
		if status == sip.StatusUnauthorized {
			res.AppendHeader(sip.NewHeader("WWW-Authenticate", `Digest realm="3402000000", nonce="peer"`))
		}
		select {
		case responded <- status:
		default:
		}
		_ = tx.Respond(res)
	})
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen peer: %v", err)
	}
	go func() { _ = peerServer.ServeUDP(conn) }()
	defer func() {
		closeQuietly(zap.NewNop(), conn, "peer udp")
		closeQuietly(zap.NewNop(), peerUA, "peer ua")
	}()

	peerAddr := conn.LocalAddr().String()
	device := Device{DeviceID: testDeviceID, Transport: TransportUDP, RemoteAddr: &peerAddr}
	body := buildCatalogQuery(1, testDeviceID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. A 200 OK is a success.
	phase.Store(sip.StatusOK)
	if err := ingress.SendMessage(ctx, device, body); err != nil {
		t.Fatalf("expected a successful MESSAGE, got %v", err)
	}

	// 2. A 401 is reported as "the device wants credentials", not as a send failure.
	phase.Store(sip.StatusUnauthorized)
	err = ingress.SendMessage(ctx, device, body)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != CodeDeviceChallenged {
		t.Fatalf("expected %s, got %v", CodeDeviceChallenged, err)
	}

	// 3. The outbound budget is per device: burst 5, so the sixth request is refused
	// with a 429-shaped error instead of hammering the device.
	phase.Store(sip.StatusOK)
	var limited error
	for i := 0; i < ingress.limits.OutboundBurst+2; i++ {
		if sendErr := ingress.SendMessage(ctx, device, body); sendErr != nil {
			limited = sendErr
			break
		}
	}
	if limited == nil {
		t.Fatal("expected the outbound limiter to refuse eventually")
	}
	if !errors.As(limited, &appErr) || appErr.Code != CodeOutboundRateLimited {
		t.Fatalf("expected %s, got %v", CodeOutboundRateLimited, limited)
	}
	if stats := ingress.Stats(); stats.OutboundLimited == 0 {
		t.Fatal("the snapshot must show that outbound protection fired")
	}
}

// The pre-provisioned lease is the default when the device omits Expires; before this
// it was a stored value nothing read, and every default silently became 3600.
func TestRegisterWithoutExpiresUsesTheProvisionedLease(t *testing.T) {
	ingress, store, _, _, cleanup := newTestIngress(t)
	defer cleanup()
	ctx := context.Background()

	const deviceID = "34020000001320000011"
	provisioned := 600
	if _, err := store.UpsertDevice(ctx, DeviceUpsert{
		DeviceID:       deviceID,
		Password:       stringPtr(testPassword),
		ExpiresSeconds: &provisioned,
	}); err != nil {
		t.Fatalf("provision device: %v", err)
	}

	req := newRegisterRequest(t, deviceID, nil)
	nonce := ingress.nonces.Issue()
	withAuthorization(t, req, testPassword, nonce)
	tx := newRecordingTx()
	ingress.handleRegister(req, tx)

	responses := tx.Result()
	if len(responses) != 1 || responses[0].StatusCode != sip.StatusOK {
		t.Fatalf("expected a single 200 OK, got %+v", responses)
	}
	device := deviceState(t, store, deviceID)
	if device.ExpiresSeconds != provisioned {
		t.Fatalf("expected the provisioned lease %d, got %d", provisioned, device.ExpiresSeconds)
	}
	if device.State != DeviceStateOnline {
		t.Fatalf("expected the device online, got %q", device.State)
	}
}

func TestRegistrationLeaseResolution(t *testing.T) {
	device := &Device{DeviceID: testDeviceID, ExpiresSeconds: 900}
	tests := []struct {
		name      string
		requested int
		provided  bool
		device    *Device
		want      int
	}{
		{name: "device asked", requested: 1200, provided: true, device: device, want: 1200},
		{name: "device unregisters", requested: 0, provided: true, device: device, want: 0},
		{name: "provisioned default", requested: 0, provided: false, device: device, want: 900},
		{name: "no provisioned value", requested: 0, provided: false, device: &Device{}, want: DefaultExpiresSeconds},
		{name: "no device", requested: 0, provided: false, device: nil, want: DefaultExpiresSeconds},
		{
			name: "provisioned value below the floor", requested: 0, provided: false,
			device: &Device{ExpiresSeconds: 5}, want: minRegistrationSeconds,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := registrationLease(test.requested, test.provided, test.device); got != test.want {
				t.Fatalf("expected lease %d, got %d", test.want, got)
			}
		})
	}
}
