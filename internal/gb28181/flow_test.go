package gb28181

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// writeChunks feeds the guard exactly as the transport would, one read at a time.
func writeChunks(t *testing.T, conn *framedConn, chunks ...string) error {
	t.Helper()
	var err error
	for _, chunk := range chunks {
		if err = conn.observe([]byte(chunk)); err != nil {
			return err
		}
	}
	return nil
}

func newFramedConn(max int) *framedConn {
	return &framedConn{max: max}
}

func TestFramedConnAcceptsPipelinedMessages(t *testing.T) {
	conn := newFramedConn(1024)
	message := "REGISTER sip:x SIP/2.0\r\nContent-Length: 5\r\n\r\nhello"

	if err := writeChunks(t, conn, message, message); err != nil {
		t.Fatalf("two pipelined messages within the cap must pass: %v", err)
	}
	if err := writeChunks(t, conn, "MESSAGE sip:x SIP/2.0\r\n\r\n"); err != nil {
		t.Fatalf("a message without a body must pass: %v", err)
	}
}

func TestFramedConnHandlesSplitReads(t *testing.T) {
	conn := newFramedConn(256)
	body := bytes.Repeat([]byte("x"), 40)
	header := "REGISTER sip:x SIP/2.0\r\nContent-Length: 40\r\n\r\n"

	// Split inside the header, then inside the body: framing must survive both.
	split := len(header) / 2
	if err := writeChunks(t, conn, header[:split]); err != nil {
		t.Fatalf("partial header must not fail: %v", err)
	}
	if err := writeChunks(t, conn, header[split:]+string(body[:10])); err != nil {
		t.Fatalf("header end plus partial body must not fail: %v", err)
	}
	if err := writeChunks(t, conn, string(body[10:])); err != nil {
		t.Fatalf("rest of the body must not fail: %v", err)
	}
}

func TestFramedConnRejectsOversizedMessage(t *testing.T) {
	conn := newFramedConn(128)
	// The header alone is small; only the declared body makes the message too large.
	header := "MESSAGE sip:x SIP/2.0\r\nContent-Length: 4096\r\n\r\n"

	err := writeChunks(t, conn, header)
	if !errors.Is(err, errMessageTooLarge) {
		t.Fatalf("expected an oversized-message error, got %v", err)
	}
}

func TestFramedConnRejectsOversizedHeader(t *testing.T) {
	conn := newFramedConn(128)
	// No terminator, so the message never completes: the cap must still fire instead
	// of growing the buffer.
	err := writeChunks(t, conn, string(bytes.Repeat([]byte("A"), 200)))
	if !errors.Is(err, errMessageTooLarge) {
		t.Fatalf("expected an oversized-header error, got %v", err)
	}
}

func TestContentLengthOf(t *testing.T) {
	tests := []struct {
		header string
		want   int
	}{
		{"Content-Length: 42", 42},
		{"content-length: 7\r\nX: y", 7},
		{"Content-Type: x\r\nContent-Length: 9", 9},
		{"Content-Length: not-a-number", 0},
		{"Content-Length: -3", 0},
		{"X: y", 0},
	}
	for _, test := range tests {
		if got := contentLengthOf([]byte(test.header)); got != test.want {
			t.Errorf("contentLengthOf(%q) = %d, want %d", test.header, got, test.want)
		}
	}
}

func TestIngressListenerCapsConnections(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener := newIngressListener(inner, ingressListenerOptions{
		MaxConnections:  1,
		MaxPerSource:    1,
		MaxMessageBytes: 1024,
		Logger:          zap.NewNop(),
	})
	defer func() { _ = listener.Close() }()

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted <- conn
		}
	}()

	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	defer func() { _ = first.Close() }()
	select {
	case conn := <-accepted:
		defer func() { _ = conn.Close() }()
	case <-time.After(2 * time.Second):
		t.Fatal("expected the first connection to be accepted immediately")
	}

	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}
	defer func() { _ = second.Close() }()

	deadline := time.Now().Add(2 * time.Second)
	for listener.Rejected() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if listener.Rejected() != 1 {
		t.Fatalf("expected the over-cap connection to be dropped, dropped=%d", listener.Rejected())
	}
	// The overflowing connection is closed without ever being served.
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, readErr := second.Read(make([]byte, 1)); readErr == nil {
		t.Fatal("expected the over-cap connection to be closed")
	}
}

func TestGuardedConnReleasesSlotOnClose(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	released := 0
	conn := &guardedConn{Conn: server, release: func() { released++ }}
	_ = conn.Close()
	_ = conn.Close()
	if released != 1 {
		t.Fatalf("expected the slot to be released exactly once, got %d", released)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected the underlying pipe to be closed, got %v", err)
	}
}

// A device that loses power never closes its TCP connection: without a read deadline
// the half-open socket holds a slot until the kernel gives up, which for a socket
// nobody writes to can be never. The deadline is what makes the slot table self-heal.
func TestFramedConnDropsASilentPeer(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	conn := &framedConn{Conn: server, max: 1024, idle: 20 * time.Millisecond}
	defer func() { _ = conn.Close() }()

	done := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 16))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error for a silent peer")
		}
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("expected a timeout, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the idle deadline never fired")
	}
}

func TestFramedConnKeepsReadingWhileThePeerIsAlive(t *testing.T) {
	server, client := net.Pipe()
	conn := &framedConn{Conn: server, max: 1024, idle: 100 * time.Millisecond}
	defer func() { _ = conn.Close() }()
	go func() {
		for i := 0; i < 3; i++ {
			time.Sleep(30 * time.Millisecond)
			_, _ = client.Write([]byte("\r\n"))
		}
		_ = client.Close()
	}()

	buf := make([]byte, 16)
	for i := 0; i < 3; i++ {
		if _, err := conn.Read(buf); err != nil {
			t.Fatalf("read %d failed although the peer was alive: %v", i, err)
		}
	}
}

// One peer must not be able to take every signalling slot: devices behind the same NAT
// share a source address, so the per-source cap is a flood guard rather than a
// per-device quota, and it has to leave the global table usable for other peers.
func TestIngressListenerCapsConnectionsPerSource(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener := newIngressListener(inner, ingressListenerOptions{
		MaxConnections:  8,
		MaxPerSource:    1,
		MaxMessageBytes: 1024,
		IdleTimeout:     time.Minute,
		Logger:          zap.NewNop(),
	})
	defer func() { _ = listener.Close() }()

	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted <- conn
		}
	}()

	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	defer func() { _ = first.Close() }()
	select {
	case conn := <-accepted:
		defer func() { _ = conn.Close() }()
	case <-time.After(2 * time.Second):
		t.Fatal("expected the first connection from the source to be accepted")
	}

	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}
	defer func() { _ = second.Close() }()

	deadline := time.Now().Add(2 * time.Second)
	for listener.Rejected() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if listener.Rejected() != 1 {
		t.Fatalf("expected the second connection from the same address to be dropped, rejected=%d", listener.Rejected())
	}
	if active := listener.Active(); active != 1 {
		t.Fatalf("expected exactly one held slot, got %d", active)
	}
}
