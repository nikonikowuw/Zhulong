package gb28181

import (
	"bytes"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// sipHeaderTerminator ends a SIP header block; bodies and the next message start
// right after it.
var sipHeaderTerminator = []byte("\r\n\r\n")

// errMessageTooLarge is returned when a streamed SIP message exceeds the configured
// cap. It is not a protocol error: the connection is dropped instead of answering,
// because a peer that ignores framing limits is not worth a response.
var errMessageTooLarge = errors.New("gb28181 sip: inbound message exceeds the size limit")

// ingressListener enforces the TCP-side protections that a read filter cannot:
//
//   - a hard cap on concurrently accepted signalling connections, so a connection
//     flood cannot exhaust file descriptors (UDP has no such resource per peer);
//   - a per-message size cap. The transport hands the parser arbitrary read chunks,
//     so a cap applied per chunk would let a 10 MiB message through in 8 KiB pieces;
//     framedConn tracks SIP framing across reads instead.
type ingressListener struct {
	net.Listener

	logger   *zap.Logger
	slots    chan struct{}
	maxBytes int
	dropped  atomic.Uint64
}

func newIngressListener(inner net.Listener, maxConnections, maxMessageBytes int, logger *zap.Logger) *ingressListener {
	if maxConnections <= 0 {
		maxConnections = defaultMaxSignallingConns
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ingressListener{
		Listener: inner,
		logger:   logger,
		slots:    make(chan struct{}, maxConnections),
		maxBytes: maxMessageBytes,
	}
}

// Accept drops connections beyond the cap instead of failing: returning an error
// would stop the serve loop for everyone.
func (l *ingressListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &guardedConn{
				Conn: &framedConn{Conn: conn, max: l.maxBytes},
				release: func() {
					<-l.slots
				},
			}, nil
		default:
			l.dropped.Add(1)
			closeQuietly(l.logger, conn, "rejected gb28181 signalling connection")
		}
	}
}

// Dropped counts connections refused because the cap was reached.
func (l *ingressListener) Dropped() uint64 { return l.dropped.Load() }

// guardedConn releases its slot exactly once, whatever closes it first.
type guardedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *guardedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// framedConn tracks SIP message boundaries so the size cap applies to whole messages.
type framedConn struct {
	net.Conn
	max int

	header    []byte
	bodyBytes int
}

// Read passes bytes through unchanged and aborts the connection when one message
// exceeds the cap.
func (c *framedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		if observeErr := c.observe(p[:n]); observeErr != nil {
			return 0, errors.Join(observeErr, c.Conn.Close())
		}
	}
	return n, err
}

// observe advances the framing state over one read chunk.
//
// The parser downstream still does the real parsing; this only needs enough framing
// to know where a message starts, how long its body is, and when it ends.
func (c *framedConn) observe(chunk []byte) error {
	// A single step settles the pending body: either the chunk ends or the body does.
	if c.bodyBytes > 0 {
		consume := min(c.bodyBytes, len(chunk))
		c.bodyBytes -= consume
		chunk = chunk[consume:]
	}
	if len(chunk) == 0 {
		return nil
	}
	c.header = append(c.header, chunk...)
	return c.advanceOverMessages()
}

// advanceOverMessages walks whole messages out of c.header until it holds the start
// of a message whose body has not arrived yet.
func (c *framedConn) advanceOverMessages() error {
	for {
		index := bytes.Index(c.header, sipHeaderTerminator)
		if index < 0 {
			// No header terminator yet: the header block itself is bounded.
			if len(c.header) > c.max {
				return errMessageTooLarge
			}
			return nil
		}
		headerEnd := index + len(sipHeaderTerminator)
		contentLength := contentLengthOf(c.header[:index])
		if headerEnd+contentLength > c.max {
			return errMessageTooLarge
		}
		c.header = c.header[headerEnd:]
		c.bodyBytes = contentLength

		if c.bodyBytes == 0 {
			// Header-only message: the next one may already be in this read.
			continue
		}
		if len(c.header) > 0 {
			consume := min(c.bodyBytes, len(c.header))
			c.bodyBytes -= consume
			c.header = c.header[consume:]
		}
		if c.bodyBytes > 0 {
			// The rest of this body needs another read.
			return nil
		}
	}
}

// contentLengthHeader is the header name matched case-insensitively by the framing
// state machine. It is a package value so the per-message path allocates nothing.
var contentLengthHeader = []byte("Content-Length")

// contentLengthOf extracts Content-Length from a raw header block. A missing or
// unparsable value is treated as zero: the downstream parser decides whether the
// message is acceptable, this only sizes the read window.
func contentLengthOf(header []byte) int {
	for _, line := range bytes.Split(header, []byte("\r\n")) {
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		if !bytes.EqualFold(bytes.TrimSpace(line[:colon]), contentLengthHeader) {
			continue
		}
		value, err := strconv.Atoi(string(bytes.TrimSpace(line[colon+1:])))
		if err != nil || value < 0 {
			return 0
		}
		return value
	}
	return 0
}
