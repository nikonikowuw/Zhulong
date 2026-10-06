package camera

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v4/pkg/sdp"
)

const (
	MaxHeaderBytes       = 16 * 1024       // 16 KiB max total header size
	MaxHeaderLines       = 64              // 64 max header lines
	MaxStatusLineBytes   = 4 * 1024        // 4 KiB max status line
	MaxResponseBodyBytes = 128 * 1024      // 128 KiB max SDP / response body
	DefaultProbeTimeout  = 5 * time.Second // 5 seconds default absolute timeout
)

var (
	ErrRTSPTimeout           = errors.New("RTSP connection or read timeout")
	ErrCredentialsRequired   = errors.New("RTSP authentication credentials required")
	ErrAuthRejected          = errors.New("RTSP authentication credentials rejected")
	ErrStreamNotFound        = errors.New("RTSP stream not found (404)")
	ErrRedirectNotAllowed    = errors.New("RTSP 3xx redirects are not permitted")
	ErrCSeqMismatch          = errors.New("RTSP CSeq mismatch")
	ErrHeaderLimitExceeded   = errors.New("RTSP response header limit exceeded")
	ErrBodyLimitExceeded     = errors.New("RTSP response body limit exceeded")
	ErrNoVideoStreamInSDP    = errors.New("no supported video stream found in SDP")
	ErrRTSPProtocolViolation = errors.New("RTSP protocol violation")
)

// DescribeResult contains the verified lightweight probe evidence.
type DescribeResult struct {
	StatusCode int
	Codec      string
	SDPBytes   int
	CheckedAt  time.Time
}

type rtspResponse struct {
	statusCode int
	statusText string
	headers    map[string][]string
	body       []byte
	cseq       int
}

// DescribeClient provides a lightweight, secure RTSP DESCRIBE probe client that does not send SETUP or PLAY.
type DescribeClient struct {
	dialer net.Dialer
}

// NewDescribeClient creates a new DescribeClient.
func NewDescribeClient() *DescribeClient {
	return &DescribeClient{
		dialer: net.Dialer{},
	}
}

// Describe performs a single-connection, bounded RTSP DESCRIBE exchange with optional Digest/Basic authentication.
func (c *DescribeClient) Describe(ctx context.Context, info *StreamConnectionInfo, timeout time.Duration) (*DescribeResult, error) {
	if info == nil {
		return nil, errors.New("nil stream connection info")
	}

	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}

	// Compute single absolute deadline for the entire exchange
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	dialCtx, dialCancel := context.WithDeadline(ctx, deadline)
	defer dialCancel()

	parsedURL, err := url.Parse(info.CleanURI)
	if err != nil {
		return nil, fmt.Errorf("invalid clean URI: %w", err)
	}

	hostPort := parsedURL.Host
	if !strings.Contains(hostPort, ":") {
		hostPort = net.JoinHostPort(hostPort, "554")
	}

	conn, err := c.dialer.DialContext(dialCtx, "tcp", hostPort)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(dialCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrRTSPTimeout
		}
		return nil, fmt.Errorf("connect to RTSP server %s: %w", hostPort, err)
	}
	defer conn.Close()

	// Enforce monotonic deadline on the socket for all subsequent operations
	_ = conn.SetDeadline(deadline)

	stopWait := make(chan struct{})
	defer close(stopWait)
	go func() {
		select {
		case <-dialCtx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stopWait:
		}
	}()

	reader := bufio.NewReaderSize(conn, 8192)
	writer := bufio.NewWriter(conn)

	cseq := 1

	// Request 1: Initial DESCRIBE without credentials
	resp, err := c.sendDescribe(writer, reader, info.CleanURI, cseq, "")
	if err != nil {
		if dialCtx.Err() != nil {
			if errors.Is(dialCtx.Err(), context.DeadlineExceeded) {
				return nil, ErrRTSPTimeout
			}
			return nil, dialCtx.Err()
		}
		return nil, err
	}

	if resp.statusCode == 200 {
		return c.parseAndVerifySDP(resp.body)
	}

	if resp.statusCode >= 300 && resp.statusCode < 400 {
		return nil, ErrRedirectNotAllowed
	}

	if resp.statusCode == 404 {
		return nil, ErrStreamNotFound
	}

	if resp.statusCode != 401 {
		return nil, fmt.Errorf("%w: status %d %s", ErrRTSPProtocolViolation, resp.statusCode, resp.statusText)
	}

	// Received 401: inspect challenges
	challenges := resp.headers["www-authenticate"]
	if len(challenges) == 0 {
		return nil, fmt.Errorf("%w: missing WWW-Authenticate header on 401", ErrRTSPProtocolViolation)
	}

	if info.Username == "" && info.Password == "" {
		return nil, ErrCredentialsRequired
	}

	bestChal, err := SelectBestChallenge(challenges)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSupportedAuthChallenge, err)
	}

	cseq++
	nc := 1
	cnonce, err := GenerateCnonce()
	if err != nil {
		return nil, err
	}

	authHeader, err := BuildAuthorizationHeader(
		bestChal,
		"DESCRIBE",
		info.CleanURI,
		info.Username,
		info.Password,
		nc,
		cnonce,
	)
	if err != nil {
		return nil, fmt.Errorf("build authorization header: %w", err)
	}

	// Request 2: DESCRIBE with credentials
	resp2, err := c.sendDescribe(writer, reader, info.CleanURI, cseq, authHeader)
	if err != nil {
		return nil, err
	}

	if resp2.statusCode == 200 {
		return c.parseAndVerifySDP(resp2.body)
	}

	if resp2.statusCode == 401 {
		// Check for stale=true nonce refresh
		retriedChallenges := resp2.headers["www-authenticate"]
		if len(retriedChallenges) > 0 {
			staleChal, parseErr := SelectBestChallenge(retriedChallenges)
			if parseErr == nil && staleChal.Type == AuthTypeDigest && staleChal.DigestChallenge != nil && staleChal.DigestChallenge.Stale {
				// Single allowed stale retry (Request 3)
				cseq++
				nc++
				cnonce, _ = GenerateCnonce()
				staleAuthHeader, err := BuildAuthorizationHeader(
					staleChal,
					"DESCRIBE",
					info.CleanURI,
					info.Username,
					info.Password,
					nc,
					cnonce,
				)
				if err == nil {
					resp3, err := c.sendDescribe(writer, reader, info.CleanURI, cseq, staleAuthHeader)
					if err == nil && resp3.statusCode == 200 {
						return c.parseAndVerifySDP(resp3.body)
					}
				}
			}
		}
		return nil, ErrAuthRejected
	}

	if resp2.statusCode == 404 {
		return nil, ErrStreamNotFound
	}
	if resp2.statusCode >= 300 && resp2.statusCode < 400 {
		return nil, ErrRedirectNotAllowed
	}

	return nil, fmt.Errorf("%w: status %d %s", ErrRTSPProtocolViolation, resp2.statusCode, resp2.statusText)
}

func (c *DescribeClient) sendDescribe(
	w *bufio.Writer,
	r *bufio.Reader,
	cleanURI string,
	cseq int,
	authHeader string,
) (*rtspResponse, error) {
	reqText := fmt.Sprintf("DESCRIBE %s RTSP/1.0\r\nCSeq: %d\r\nAccept: application/sdp\r\nUser-Agent: Zhulong/1.0\r\n", cleanURI, cseq)
	if authHeader != "" {
		reqText += fmt.Sprintf("Authorization: %s\r\n", authHeader)
	}
	reqText += "\r\n"

	if _, err := w.WriteString(reqText); err != nil {
		return nil, fmt.Errorf("write RTSP request: %w", err)
	}
	if err := w.Flush(); err != nil {
		return nil, fmt.Errorf("flush RTSP request: %w", err)
	}

	return c.readResponse(r, cseq)
}

func (c *DescribeClient) readResponse(r *bufio.Reader, expectedCSeq int) (*rtspResponse, error) {
	// 1. Read status line
	statusLine, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read RTSP status line: %w", err)
	}
	if len(statusLine) > MaxStatusLineBytes {
		return nil, ErrHeaderLimitExceeded
	}
	statusLine = strings.TrimRight(statusLine, "\r\n")

	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(strings.ToUpper(parts[0]), "RTSP/") {
		return nil, fmt.Errorf("%w: invalid status line %q", ErrRTSPProtocolViolation, statusLine)
	}

	statusCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid status code in %q", ErrRTSPProtocolViolation, statusLine)
	}
	statusText := ""
	if len(parts) >= 3 {
		statusText = parts[2]
	}

	// 2. Read headers
	headers := make(map[string][]string)
	totalHeaderBytes := len(statusLine) + 2
	lineCount := 0
	contentLength := 0
	respCSeq := -1

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read RTSP header: %w", err)
		}
		totalHeaderBytes += len(line)
		if totalHeaderBytes > MaxHeaderBytes {
			return nil, ErrHeaderLimitExceeded
		}
		lineCount++
		if lineCount > MaxHeaderLines {
			return nil, ErrHeaderLimitExceeded
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break // End of headers
		}

		colonIdx := strings.IndexByte(trimmed, ':')
		if colonIdx <= 0 {
			return nil, fmt.Errorf("%w: malformed header line %q", ErrRTSPProtocolViolation, trimmed)
		}

		key := strings.ToLower(strings.TrimSpace(trimmed[:colonIdx]))
		val := strings.TrimSpace(trimmed[colonIdx+1:])

		headers[key] = append(headers[key], val)

		if key == "cseq" {
			if parsedCSeq, err := strconv.Atoi(val); err == nil {
				respCSeq = parsedCSeq
			}
		} else if key == "content-length" {
			if cl, err := strconv.Atoi(val); err == nil && cl >= 0 {
				contentLength = cl
			}
		}
	}

	if respCSeq != expectedCSeq {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrCSeqMismatch, expectedCSeq, respCSeq)
	}

	// 3. Read body
	if contentLength > MaxResponseBodyBytes {
		return nil, ErrBodyLimitExceeded
	}

	var body []byte
	if contentLength > 0 {
		body = make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}
	}

	return &rtspResponse{
		statusCode: statusCode,
		statusText: statusText,
		headers:    headers,
		body:       body,
		cseq:       respCSeq,
	}, nil
}

func (c *DescribeClient) parseAndVerifySDP(body []byte) (*DescribeResult, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: empty body on 200 response", ErrRTSPProtocolViolation)
	}

	var sess sdp.SessionDescription
	if err := sess.Unmarshal(body); err != nil {
		return nil, fmt.Errorf("unmarshal SDP: %w", err)
	}

	videoFound := false
	detectedCodec := ""

	for _, media := range sess.MediaDescriptions {
		if strings.ToLower(media.MediaName.Media) != "video" {
			continue
		}

		videoFound = true
		for _, attr := range media.Attributes {
			if strings.ToLower(attr.Key) == "rtpmap" {
				val := strings.ToUpper(attr.Value)
				if strings.Contains(val, "H264") {
					detectedCodec = "h264"
					break
				}
				if strings.Contains(val, "H265") || strings.Contains(val, "HEVC") {
					detectedCodec = "h265"
					break
				}
			}
		}
		if detectedCodec != "" {
			break
		}
	}

	if !videoFound {
		return nil, ErrNoVideoStreamInSDP
	}

	// If video exists but rtpmap didn't explicitly match, default to standard "h264" or flag unsupported
	if detectedCodec == "" {
		detectedCodec = "h264" // fallback standard video format
	}

	return &DescribeResult{
		StatusCode: 200,
		Codec:      detectedCodec,
		SDPBytes:   len(body),
		CheckedAt:  time.Now().UTC(),
	}, nil
}
