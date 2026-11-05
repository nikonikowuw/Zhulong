package app

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// LoggingConfig contains runtime logging policy. Empty Format defaults to json;
// a development console is requested explicitly through Format.
type LoggingConfig struct {
	Level    string         `mapstructure:"level"`
	Format   string         `mapstructure:"format"`
	Output   string         `mapstructure:"output"`
	Sampling SamplingConfig `mapstructure:"sampling"`
	Rotation RotationConfig `mapstructure:"rotation"`
}

// SamplingConfig controls opt-in zap log sampling.
type SamplingConfig struct {
	Enabled    bool `mapstructure:"enabled"`
	Initial    int  `mapstructure:"initial"`
	Thereafter int  `mapstructure:"thereafter"`
}

// RotationConfig bounds file log storage. Values are deployment-profile specific.
type RotationConfig struct {
	Enabled        bool  `mapstructure:"enabled"`
	MaxSizeMB      int64 `mapstructure:"max_size_mb"`
	MaxBackups     int   `mapstructure:"max_backups"`
	MaxAgeDays     int   `mapstructure:"max_age_days"`
	Compress       bool  `mapstructure:"compress"`
	MaxTotalSizeMB int64 `mapstructure:"max_total_size_mb"`
}

// DecodeConfig contains video decoding policy and admission limits.
type DecodeConfig struct {
	Backend               string `mapstructure:"backend"`
	AllowSoftwareFallback bool   `mapstructure:"allow_software_fallback"`
	MaxSessions           int    `mapstructure:"max_sessions"`
}

// Config contains host-level settings supplied before Fx constructs the application.
type Config struct {
	HTTPAddress     string
	DataDir         string
	FacesDir        string
	AlgorithmTarget string
	Logging         LoggingConfig
	Decode          DecodeConfig
	ConfigPath      string
	CustomScript    string
	SessionTTL      time.Duration
	// RequirePersonIDForEnrollment refuses face enrollment for a person without an
	// external business key. It is deployment policy, not a schema constraint: an
	// offline host still creates persons without a key, but a host that reports
	// recognitions to a business system refuses to build a gallery it cannot attribute.
	RequirePersonIDForEnrollment bool
	// ExtractorAlgorithm names the algorithm package that provides face feature
	// extraction for gallery enrollment. It is an explicit id, not a path: the
	// package's active version and entry are resolved at call time, so upgrading or
	// reinstalling the package needs no configuration change.
	ExtractorAlgorithm string
	// TLSMode controls whether the HTTP listener is wrapped with TLS.
	// Valid values are "off" (plain HTTP), "auto" (self-signed cert with local/LAN SANs), and "manual".
	TLSMode     string
	TLSCertFile string
	TLSKeyFile  string

	// AlertPushPublicBaseURL is the externally reachable base URL of the host used for signed image URLs.
	AlertPushPublicBaseURL string
	// AlertPushSignedURLTTL is the validity duration for signed media URLs (1m..168h).
	AlertPushSignedURLTTL time.Duration
}

// ValidateAlertPushConfig validates public_base_url and signed_url_ttl.
func ValidateAlertPushConfig(rawBaseURL, rawTTL string) (string, time.Duration, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(rawBaseURL), "/")
	if baseURL != "" {
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			return "", 0, fmt.Errorf("alertpush.public_base_url %q must have http:// or https:// scheme", rawBaseURL)
		}
	}

	ttlStr := strings.TrimSpace(rawTTL)
	if ttlStr == "" {
		ttlStr = "24h"
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid alertpush.signed_url_ttl %q: %w", rawTTL, err)
	}
	if ttl < 1*time.Minute || ttl > 168*time.Hour {
		return "", 0, fmt.Errorf("alertpush.signed_url_ttl %v out of bounds (must be 1m..168h)", ttl)
	}
	return baseURL, ttl, nil
}

// ValidateTLSConfig ensures the requested TLS mode and certificate paths are valid.
func ValidateTLSConfig(mode, certFile, keyFile string) error {
	switch mode {
	case "", "off", "auto":
		return nil
	case "manual":
		if strings.TrimSpace(certFile) == "" {
			return errors.New("cert_file must not be empty when tls_mode is manual")
		}
		if strings.TrimSpace(keyFile) == "" {
			return errors.New("key_file must not be empty when tls_mode is manual")
		}
		return nil
	default:
		return fmt.Errorf("invalid tls_mode %q: must be off, auto, or manual", mode)
	}
}

// ValidateDecodeConfig validates the decode configuration section.
func ValidateDecodeConfig(cfg DecodeConfig) error {
	switch cfg.Backend {
	case "", "auto", "mpp", "ffmpeg":
		// valid
	default:
		return fmt.Errorf("invalid decode.backend %q: must be auto, mpp, or ffmpeg", cfg.Backend)
	}
	if cfg.MaxSessions < 0 {
		return fmt.Errorf("invalid decode.max_sessions %d: must not be negative", cfg.MaxSessions)
	}
	return nil
}

func validateHTTPAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("address must not be empty")
	}
	if address != strings.TrimSpace(address) {
		return errors.New("address must not contain surrounding whitespace")
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("address must use host:port format: %w", err)
	}
	if host == "" {
		return errors.New("host must be explicit; use 0.0.0.0 to bind all interfaces")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("port must be a number between 0 and 65535")
	}
	return nil
}

// ParseSessionTTL parses a duration string supporting standard Go durations and day suffix "d".
func ParseSessionTTL(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("session_ttl must not be empty")
	}

	if strings.HasSuffix(raw, "d") {
		daysStr := strings.TrimSuffix(raw, "d")
		days, err := strconv.ParseFloat(daysStr, 64)
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid session_ttl %q: must be a positive duration", raw)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid session_ttl %q: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("session_ttl must be positive: %v", d)
	}
	return d, nil
}

// DefaultConfig returns local-only defaults suitable for development and smoke tests.
func DefaultConfig() Config {
	return Config{
		HTTPAddress:     "127.0.0.1:8000",
		DataDir:         "./data",
		FacesDir:        "",
		AlgorithmTarget: "onnx",
		Logging: LoggingConfig{
			Level:  "info",
			Output: "stderr",
		},
		Decode: DecodeConfig{
			Backend:               "auto",
			AllowSoftwareFallback: true,
			MaxSessions:           8,
		},
		SessionTTL:             24 * time.Hour,
		ExtractorAlgorithm:     "face_recognition",
		TLSMode:                "off",
		AlertPushPublicBaseURL: "",
		AlertPushSignedURLTTL:  24 * time.Hour,
	}
}
