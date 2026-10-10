package systemtime

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestTimeToNTPAndBack(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 30, 45, 123456789, time.UTC)
	sec, frac := timeToNTP(now)
	recovered := ntpToTime(sec, frac)

	diff := recovered.Sub(now)
	if diff < -time.Microsecond || diff > time.Microsecond {
		t.Fatalf("recovered time %v differs from %v by %v", recovered, now, diff)
	}
}

func TestNTPToTimeZero(t *testing.T) {
	recovered := ntpToTime(0, 0)
	if !recovered.IsZero() {
		t.Fatalf("expected zero time, got %v", recovered)
	}
}

func startMockNTPServer(t *testing.T, offset time.Duration) (string, func()) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock udp listener: %v", err)
	}

	stopCh := make(chan struct{})

	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-stopCh:
				return
			default:
			}

			_ = pc.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				continue
			}
			if n < 48 {
				continue
			}

			// Prepare response
			resp := make([]byte, 48)
			// LI=0, VN=4, Mode=4 (Server) -> 0x24
			resp[0] = 0x24
			resp[1] = 2    // Stratum 2
			resp[2] = 4    // Poll
			resp[3] = 0xec // precision -20

			// Copy client transmit timestamp to originate timestamp
			copy(resp[24:32], buf[40:48])

			// Server receive (T2) and transmit (T3)
			serverTime := time.Now().UTC().Add(offset)
			t2Sec, t2Frac := timeToNTP(serverTime)
			binary.BigEndian.PutUint32(resp[32:36], t2Sec)
			binary.BigEndian.PutUint32(resp[36:40], t2Frac)

			t3Sec, t3Frac := timeToNTP(serverTime.Add(time.Millisecond))
			binary.BigEndian.PutUint32(resp[40:44], t3Sec)
			binary.BigEndian.PutUint32(resp[44:48], t3Frac)

			_, _ = pc.WriteTo(resp, addr)
		}
	}()

	addrStr := pc.LocalAddr().String()
	cleanup := func() {
		close(stopCh)
		_ = pc.Close()
	}
	return addrStr, cleanup
}

func TestSNTPClientQuerySuccess(t *testing.T) {
	expectedOffset := 2 * time.Second
	addr, cleanup := startMockNTPServer(t, expectedOffset)
	defer cleanup()

	client := NewSNTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := client.Query(ctx, addr, time.Second)
	if err != nil {
		t.Fatalf("unexpected error querying mock ntp: %v", err)
	}

	if res.Stratum != 2 {
		t.Errorf("expected stratum 2, got %d", res.Stratum)
	}

	diff := res.Offset - expectedOffset
	if diff < -500*time.Millisecond || diff > 500*time.Millisecond {
		t.Errorf("expected offset ~%v, got %v (diff: %v)", expectedOffset, res.Offset, diff)
	}
}

func TestSNTPClientQueryPoolFallback(t *testing.T) {
	addr, cleanup := startMockNTPServer(t, 500*time.Millisecond)
	defer cleanup()

	client := NewSNTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// First server is invalid port/host, second is valid
	servers := []string{"127.0.0.1:65530", addr}
	res, err := client.QueryPool(ctx, servers, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}

	if res == nil || res.Stratum != 2 {
		t.Fatalf("expected valid result from fallback server, got %v", res)
	}
}

func TestSNTPClientQueryPoolAllFailed(t *testing.T) {
	client := NewSNTPClient()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	servers := []string{"127.0.0.1:65531", "127.0.0.1:65532"}
	_, err := client.QueryPool(ctx, servers, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when all servers fail, got nil")
	}
}
