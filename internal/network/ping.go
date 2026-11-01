package network

import (
	"bytes"
	"context"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

const pingProbeTimeout = 2 * time.Second

var (
	rttRegex      = regexp.MustCompile(`(?:rtt|round-trip)[^=]*=\s*[\d.]+/([\d.]+)/`)
	hostnameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)
)

// PingProbe performs a lightweight reachability test to the specified target.
func PingProbe(ctx context.Context, target string) (*PingResponse, error) {
	cleanTarget := strings.TrimSpace(target)
	if cleanTarget == "" {
		return nil, apperr.New(apperr.KindInvalid, "INVALID_TARGET", "Target host or IP address is required", nil)
	}

	// Reject flags or leading/trailing dots/hyphens before command invocation.
	if strings.HasPrefix(cleanTarget, "-") {
		return nil, apperr.New(apperr.KindInvalid, "INVALID_TARGET", "Invalid target format", nil)
	}

	// Accept an IP literal or a plain hostname; anything else could reach the shell as an argument.
	isIP := net.ParseIP(cleanTarget) != nil
	isHostname := hostnameRegex.MatchString(cleanTarget)
	if !isIP && !isHostname {
		return nil, apperr.New(apperr.KindInvalid, "INVALID_TARGET", "Invalid target format", nil)
	}

	probeCtx, cancel := context.WithTimeout(ctx, pingProbeTimeout)
	defer cancel()

	args := []string{"-c", "2", "-W", "1", cleanTarget}
	if runtime.GOOS == "darwin" {
		args = []string{"-c", "2", "-t", "1", cleanTarget}
	}

	start := time.Now()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(probeCtx, "ping", args...)
	cmd.Stdout = &stdout

	// A non-zero exit code means "unreachable", which is a valid probe result rather than a failure.
	if err := cmd.Run(); err != nil {
		return &PingResponse{Reachable: false, RTTMs: 0}, nil
	}

	// Fall back to half the wall clock for two probes when the output format is not recognized.
	rtt := float64(time.Since(start).Milliseconds()) / 2.0
	if average, ok := parseAverageRTT(stdout.String()); ok {
		rtt = average
	}

	return &PingResponse{Reachable: true, RTTMs: rtt}, nil
}

// parseAverageRTT extracts the average round-trip time in milliseconds from ping output.
func parseAverageRTT(output string) (float64, bool) {
	matches := rttRegex.FindStringSubmatch(output)
	if len(matches) < 2 {
		return 0, false
	}

	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
