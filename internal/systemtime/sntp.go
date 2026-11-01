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

	// The caller's deadline wins; otherwise bound the socket by the per-server
	// timeout so a black-holed server cannot stall the probe pool.
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline && timeout > 0 {
		deadline, hasDeadline = time.Now().Add(timeout), true
	}
	if hasDeadline {
		if err := conn.SetDeadline(deadline); err != nil {
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

	header, err := parseSNTPResponse(host, resp[:n])
	if err != nil {
		return nil, err
	}

	// RFC 4330 math:
	// delay = (T4 - T1) - (T3 - T2)
	// offset = ((T2 - T1) + (T3 - T4)) / 2
	rtt := max(t4.Sub(t1)-header.T3.Sub(header.T2), 0)
	offset := (header.T2.Sub(t1) + header.T3.Sub(t4)) / 2

	return &SNTPResult{
		Server:    server,
		Offset:    offset,
		RTT:       rtt,
		Stratum:   header.Stratum,
		Leap:      header.Leap,
		Precision: header.Precision,
		T1:        t1,
		T2:        header.T2,
		T3:        header.T3,
		T4:        t4,
	}, nil
}

// ntpHeader is the validated subset of an RFC 4330 reply header plus the two
// server-side timestamps the offset calculation needs.
type ntpHeader struct {
	Leap      uint8
	Version   uint8
	Mode      uint8
	Stratum   uint8
	Precision int8
	T2        time.Time // Server receive time
	T3        time.Time // Server transmit time
}

// parseSNTPResponse validates one server reply and extracts its header and
// timestamps. host is only used to make the errors actionable.
func parseSNTPResponse(host string, resp []byte) (ntpHeader, error) {
	if len(resp) < ntpPacketSize {
		return ntpHeader{}, fmt.Errorf("short ntp response from %s: %d bytes", host, len(resp))
	}

	header := ntpHeader{
		Leap:      (resp[0] >> 6) & 0x03,
		Version:   (resp[0] >> 3) & 0x07,
		Mode:      resp[0] & 0x07,
		Stratum:   resp[1],
		Precision: int8(resp[3]),
	}

	// Mode 4 = server, 5 = broadcast.
	if header.Mode != 4 && header.Mode != 5 {
		return ntpHeader{}, fmt.Errorf("invalid ntp mode %d from %s (expected 4/5)", header.Mode, host)
	}
	if header.Version < 3 || header.Version > 4 {
		return ntpHeader{}, fmt.Errorf("unsupported ntp version %d from %s", header.Version, host)
	}
	// Stratum 0 = Kiss-o'-Death, 16 = unsynchronized.
	if header.Stratum == 0 {
		return ntpHeader{}, fmt.Errorf("server %s returned kiss-o'-death (stratum 0)", host)
	}
	if header.Stratum >= 16 {
		return ntpHeader{}, fmt.Errorf("server %s is unsynchronized (stratum %d)", host, header.Stratum)
	}
	if header.Leap == 3 {
		return ntpHeader{}, fmt.Errorf("server %s clock is unsynchronized (leap indicator 3)", host)
	}

	header.T2 = ntpToTime(
		binary.BigEndian.Uint32(resp[32:36]),
		binary.BigEndian.Uint32(resp[36:40]),
	)
	header.T3 = ntpToTime(
		binary.BigEndian.Uint32(resp[40:44]),
		binary.BigEndian.Uint32(resp[44:48]),
	)
	return header, nil
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
