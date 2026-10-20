package app

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// LoggingConfig contains runtime logging policy. Empty Format follows Development mode.
type LoggingConfig struct {
	Development bool           `mapstructure:"development"`
	Level       string         `mapstructure:"level"`
	Format      string         `mapstructure:"format"`
	Output      string         `mapstructure:"output"`
	Sampling    SamplingConfig `mapstructure:"sampling"`
	Rotation    RotationConfig `mapstructure:"rotation"`
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

// Config contains host-level settings supplied before Fx constructs the application.
type Config struct {
	HTTPAddress     string
	DataDir         string
	FacesDir        string
	AlgorithmTarget string
	Development     bool
	Logging         LoggingConfig
	ConfigPath      string
	CustomScript    string
	SessionTTL      time.Duration
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
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return errors.New("port must be a number between 0 and 65535")
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
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
		HTTPAddress:     "127.0.0.1:8080",
		DataDir:         "./data",
		FacesDir:        "",
		AlgorithmTarget: "cpu",
		Logging: LoggingConfig{
			Level:  "info",
			Output: "stderr",
		},
		SessionTTL: 24 * time.Hour,
	}
}
