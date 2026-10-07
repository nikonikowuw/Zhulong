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

var rttRegex = regexp.MustCompile(`(?:rtt|round-trip)[^=]*=\s*[\d.]+/([\d.]+)/`)

// PingProbe performs a lightweight reachability test to the specified target.
func PingProbe(ctx context.Context, target string) (*PingResponse, error) {
	cleanTarget := strings.TrimSpace(target)
	if cleanTarget == "" {
		return nil, apperr.New(apperr.KindInvalid, "INVALID_TARGET", "Target host or IP address is required", nil)
	}

	// Validate target as IP or hostname (prevent shell injection)
	if net.ParseIP(cleanTarget) == nil {
		// Verify basic hostname format (alphanumeric, dashes, dots)
		matched, _ := regexp.MatchString(`^[a-zA-Z0-9.-]+$`, cleanTarget)
		if !matched {
			return nil, apperr.New(apperr.KindInvalid, "INVALID_TARGET", "Invalid target format", nil)
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var args []string
	if runtime.GOOS == "darwin" {
		args = []string{"-c", "2", "-t", "1", cleanTarget}
	} else {
		args = []string{"-c", "2", "-W", "1", cleanTarget}
	}

	start := time.Now()
	var stdout bytes.Buffer
	cmd := exec.CommandContext(probeCtx, "ping", args...)
	cmd.Stdout = &stdout

	if err := cmd.Run(); err == nil {
		elapsed := time.Since(start)
		rtt := float64(elapsed.Milliseconds()) / 2.0
		// Attempt to parse avg rtt from stdout
		matches := rttRegex.FindStringSubmatch(stdout.String())
		if len(matches) > 1 {
			if parsed, err := strconv.ParseFloat(matches[1], 64); err == nil {
				rtt = parsed
			}
		}
		return &PingResponse{
			Reachable: true,
			RTTMs:     rtt,
		}, nil
	}

	return &PingResponse{
		Reachable: false,
		RTTMs:     0,
	}, nil
}
