package systemtime

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	// ntpEpochOffset is the difference in seconds between 1900-01-01 (NTP) and 1970-01-01 (Unix).
	ntpEpochOffset = 2208988800
	defaultNTPPort = 123
	ntpPacketSize  = 48
	// ntpFractionScale converts the 32-bit fractional part of an NTP timestamp.
	ntpFractionScale = 1 << 32
	nanosPerSecond   = 1_000_000_000
)

// SNTPResult holds the calculation results from an SNTP query.
type SNTPResult struct {
	Server  string
	Offset  time.Duration
	RTT     time.Duration
	Stratum uint8
	Leap    uint8
	// Precision is the server's clock precision exponent: the real precision is
	// 2^Precision seconds.
	Precision int8
	T1        time.Time // Local send time
	T2        time.Time // Server receive time
	T3        time.Time // Server transmit time
	T4        time.Time // Local receive time
}

// SNTPClient defines the contract for querying NTP servers.
type SNTPClient interface {
	Query(ctx context.Context, server string, timeout time.Duration) (*SNTPResult, error)
	QueryPool(ctx context.Context, servers []string, timeout time.Duration) (*SNTPResult, error)
}

type sntpClientImpl struct{}

// NewSNTPClient creates a standard RFC 4330 SNTP client.
func NewSNTPClient() SNTPClient {
	return &sntpClientImpl{}
}

// timeToNTP converts a time.Time to an NTP 64-bit timestamp (32-bit seconds + 32-bit fraction).
func timeToNTP(t time.Time) (uint32, uint32) {
	sec := uint32(t.Unix() + ntpEpochOffset)
	frac := uint32((uint64(t.Nanosecond()) * ntpFractionScale) / nanosPerSecond)
	return sec, frac
}

// ntpToTime converts NTP 64-bit timestamp (32-bit seconds + 32-bit fraction) to time.Time in UTC.
func ntpToTime(sec uint32, frac uint32) time.Time {
	if sec == 0 && frac == 0 {
		return time.Time{}
	}
	unixSec := int64(sec) - ntpEpochOffset
	nsec := (int64(frac) * nanosPerSecond) / ntpFractionScale
	return time.Unix(unixSec, nsec).UTC()
}

// Query performs a single RFC 4330 SNTP query to the given server.
func (c *sntpClientImpl) Query(ctx context.Context, server string, timeout time.Duration) (*SNTPResult, error) {
	host := server
	if !strings.Contains(server, ":") {
		host = fmt.Sprintf("%s:%d", server, defaultNTPPort)
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "udp", host)
	if err != nil {
		return nil, fmt.Errorf("dial ntp %s: %w", host, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set ntp deadline for %s: %w", host, err)
		}
	} else if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, fmt.Errorf("set ntp deadline for %s: %w", host, err)
		}
	}

	// 48-byte request packet: Mode=3 (Client), Version=4, LI=0
	req := make([]byte, ntpPacketSize)
	req[0] = 0x23 // LI 0, VN 4, Mode 3

	t1 := time.Now().UTC()
	sec, frac := timeToNTP(t1)
	binary.BigEndian.PutUint32(req[40:44], sec)
	binary.BigEndian.PutUint32(req[44:48], frac)

	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("write ntp %s: %w", host, err)
	}

	resp := make([]byte, ntpPacketSize)
	n, err := conn.Read(resp)
	t4 := time.Now().UTC()
	if err != nil {
		return nil, fmt.Errorf("read ntp %s: %w", host, err)
	}
	if n < ntpPacketSize {
		return nil, fmt.Errorf("short ntp response from %s: %d bytes", host, n)
	}

	li := (resp[0] >> 6) & 0x03
	version := (resp[0] >> 3) & 0x07
	mode := resp[0] & 0x07
	stratum := resp[1]
	precision := int8(resp[3])

	// Validate Mode (4 = server, 5 = broadcast)
	if mode != 4 && mode != 5 {
		return nil, fmt.Errorf("invalid ntp mode %d from %s (expected 4/5)", mode, host)
	}
	if version < 3 || version > 4 {
		return nil, fmt.Errorf("unsupported ntp version %d from %s", version, host)
	}
	// Stratum 0 = Kiss-o'-Death, 16 = unsynchronized
	if stratum == 0 {
		return nil, fmt.Errorf("server %s returned kiss-o'-death (stratum 0)", host)
	}
	if stratum >= 16 {
		return nil, fmt.Errorf("server %s is unsynchronized (stratum %d)", host, stratum)
	}
	if li == 3 {
		return nil, fmt.Errorf("server %s clock is unsynchronized (leap indicator 3)", host)
	}

	// T2: Server Receive Timestamp (bytes 32-39)
	t2 := ntpToTime(
		binary.BigEndian.Uint32(resp[32:36]),
		binary.BigEndian.Uint32(resp[36:40]),
	)

	// T3: Server Transmit Timestamp (bytes 40-47)
	t3 := ntpToTime(
		binary.BigEndian.Uint32(resp[40:44]),
		binary.BigEndian.Uint32(resp[44:48]),
	)

	// RFC 4330 math:
	// delay = (T4 - T1) - (T3 - T2)
	// offset = ((T2 - T1) + (T3 - T4)) / 2
	rtt := max(t4.Sub(t1)-t3.Sub(t2), 0)
	offset := (t2.Sub(t1) + t3.Sub(t4)) / 2

	return &SNTPResult{
		Server:    server,
		Offset:    offset,
		RTT:       rtt,
		Stratum:   stratum,
		Leap:      li,
		Precision: precision,
		T1:        t1,
		T2:        t2,
		T3:        t3,
		T4:        t4,
	}, nil
}

// QueryPool tries servers sequentially with fallback until one succeeds or all fail.
func (c *sntpClientImpl) QueryPool(ctx context.Context, servers []string, timeout time.Duration) (*SNTPResult, error) {
	if len(servers) == 0 {
		return nil, errors.New("no ntp servers provided")
	}

	var lastErr error
	for _, server := range servers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}

		res, err := c.Query(ctx, server, timeout)
		if err == nil {
			return res, nil
		}
		lastErr = err

		// The caller's budget is gone; the remaining servers would fail the same way.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("ntp query pool aborted: %w", ctxErr)
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all ntp servers failed, last error: %w", lastErr)
	}
	return nil, errors.New("no valid ntp servers configured")
}
