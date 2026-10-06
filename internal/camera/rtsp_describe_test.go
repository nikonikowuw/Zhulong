package camera

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

const sampleH264SDP = "v=0\r\n" +
	"o=- 1600000000 1 IN IP4 127.0.0.1\r\n" +
	"s=Zhulong Test Camera\r\n" +
	"t=0 0\r\n" +
	"m=video 0 RTP/AVP 96\r\n" +
	"a=rtpmap:96 H264/90000\r\n" +
	"a=control:track1\r\n"

func startMockRTSPServer(t *testing.T, handler func(conn net.Conn)) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on test port: %v", err)
	}

	stopChan := make(chan struct{})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-stopChan:
					return
				default:
					return
				}
			}
			go handler(conn)
		}
	}()

	cleanup := func() {
		close(stopChan)
		_ = listener.Close()
	}
	return listener.Addr().String(), cleanup
}

func readMockRTSPRequest(r *bufio.Reader) (method, uri, cseq, auth string, err error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", "", "", "", err
	}
	parts := strings.Split(strings.TrimSpace(line), " ")
	if len(parts) >= 2 {
		method = parts[0]
		uri = parts[1]
	}

	for {
		headerLine, err := r.ReadString('\n')
		if err != nil {
			return "", "", "", "", err
		}
		trimmed := strings.TrimSpace(headerLine)
		if trimmed == "" {
			break
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon > 0 {
			k := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
			v := strings.TrimSpace(trimmed[colon+1:])
			if k == "cseq" {
				cseq = v
			} else if k == "authorization" {
				auth = v
			}
		}
	}
	return method, uri, cseq, auth, nil
}

func TestDescribeClientSuccessNoAuth(t *testing.T) {
	addr, cleanup := startMockRTSPServer(t, func(conn net.Conn) {
		defer conn.Close()
		r := bufio.NewReader(conn)
		_, _, cseq, _, err := readMockRTSPRequest(r)
		if err != nil {
			return
		}

		resp := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
			cseq, len(sampleH264SDP), sampleH264SDP)
		_, _ = conn.Write([]byte(resp))
	})
	defer cleanup()

	client := NewDescribeClient()
	info := &StreamConnectionInfo{
		CleanURI:  fmt.Sprintf("rtsp://%s/live/main", addr),
		Transport: "tcp",
	}

	res, err := client.Describe(context.Background(), info, 2*time.Second)
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}

	if res.StatusCode != 200 || res.Codec != "h264" {
		t.Fatalf("unexpected DescribeResult: %+v", res)
	}
}

func TestDescribeClientDigestAuthWorkflow(t *testing.T) {
	addr, cleanup := startMockRTSPServer(t, func(conn net.Conn) {
		defer conn.Close()
		r := bufio.NewReader(conn)

		// Request 1: without auth -> return 401 with Challenge
		_, _, cseq1, auth1, err := readMockRTSPRequest(r)
		if err != nil || auth1 != "" {
			return
		}

		resp1 := fmt.Sprintf("RTSP/1.0 401 Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Digest realm=\"MockCam\", nonce=\"testnonce123\", algorithm=\"MD5\", qop=\"auth\"\r\nContent-Length: 0\r\n\r\n", cseq1)
		_, _ = conn.Write([]byte(resp1))

		// Request 2: with auth -> return 200 OK
		_, _, cseq2, auth2, err := readMockRTSPRequest(r)
		if err != nil {
			return
		}
		if !strings.Contains(auth2, `username="admin"`) || !strings.Contains(auth2, `nonce="testnonce123"`) {
			failResp := fmt.Sprintf("RTSP/1.0 401 Unauthorized\r\nCSeq: %s\r\nContent-Length: 0\r\n\r\n", cseq2)
			_, _ = conn.Write([]byte(failResp))
			return
		}

		resp2 := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
			cseq2, len(sampleH264SDP), sampleH264SDP)
		_, _ = conn.Write([]byte(resp2))
	})
	defer cleanup()

	client := NewDescribeClient()
	info := &StreamConnectionInfo{
		CleanURI:  fmt.Sprintf("rtsp://%s/live/main", addr),
		Username:  "admin",
		Password:  "password123",
		Transport: "tcp",
	}

	res, err := client.Describe(context.Background(), info, 2*time.Second)
	if err != nil {
		t.Fatalf("Describe with Digest failed: %v", err)
	}

	if res.StatusCode != 200 || res.Codec != "h264" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDescribeClientAuthRejectedOnWrongPassword(t *testing.T) {
	addr, cleanup := startMockRTSPServer(t, func(conn net.Conn) {
		defer conn.Close()
		r := bufio.NewReader(conn)

		// 1. Send 401
		_, _, cseq1, _, _ := readMockRTSPRequest(r)
		resp1 := fmt.Sprintf("RTSP/1.0 401 Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Digest realm=\"MockCam\", nonce=\"testnonce123\", algorithm=\"MD5\"\r\nContent-Length: 0\r\n\r\n", cseq1)
		_, _ = conn.Write([]byte(resp1))

		// 2. Client responds with auth, server rejects with 401
		_, _, cseq2, _, _ := readMockRTSPRequest(r)
		resp2 := fmt.Sprintf("RTSP/1.0 401 Unauthorized\r\nCSeq: %s\r\nWWW-Authenticate: Digest realm=\"MockCam\", nonce=\"testnonce123\", algorithm=\"MD5\"\r\nContent-Length: 0\r\n\r\n", cseq2)
		_, _ = conn.Write([]byte(resp2))
	})
	defer cleanup()

	client := NewDescribeClient()
	info := &StreamConnectionInfo{
		CleanURI:  fmt.Sprintf("rtsp://%s/live/main", addr),
		Username:  "admin",
		Password:  "wrongpass",
		Transport: "tcp",
	}

	_, err := client.Describe(context.Background(), info, 2*time.Second)
	if err != ErrAuthRejected {
		t.Fatalf("expected ErrAuthRejected, got: %v", err)
	}
}

func TestDescribeClientRejectsRedirect(t *testing.T) {
	addr, cleanup := startMockRTSPServer(t, func(conn net.Conn) {
		defer conn.Close()
		r := bufio.NewReader(conn)
		_, _, cseq, _, _ := readMockRTSPRequest(r)
		resp := fmt.Sprintf("RTSP/1.0 302 Moved Temporarily\r\nCSeq: %s\r\nLocation: rtsp://127.0.0.1:8554/other\r\nContent-Length: 0\r\n\r\n", cseq)
		_, _ = conn.Write([]byte(resp))
	})
	defer cleanup()

	client := NewDescribeClient()
	info := &StreamConnectionInfo{
		CleanURI:  fmt.Sprintf("rtsp://%s/live/main", addr),
		Transport: "tcp",
	}

	_, err := client.Describe(context.Background(), info, 2*time.Second)
	if err != ErrRedirectNotAllowed {
		t.Fatalf("expected ErrRedirectNotAllowed, got: %v", err)
	}
}

func TestDescribeClientDetectsCSeqMismatch(t *testing.T) {
	addr, cleanup := startMockRTSPServer(t, func(conn net.Conn) {
		defer conn.Close()
		r := bufio.NewReader(conn)
		_, _, cseq, _, _ := readMockRTSPRequest(r)
		badCSeq, _ := strconv.Atoi(cseq)
		resp := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: %d\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
			badCSeq+10, len(sampleH264SDP), sampleH264SDP)
		_, _ = conn.Write([]byte(resp))
	})
	defer cleanup()

	client := NewDescribeClient()
	info := &StreamConnectionInfo{
		CleanURI:  fmt.Sprintf("rtsp://%s/live/main", addr),
		Transport: "tcp",
	}

	_, err := client.Describe(context.Background(), info, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "CSeq mismatch") {
		t.Fatalf("expected CSeq mismatch error, got: %v", err)
	}
}
