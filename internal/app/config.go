package app

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Config contains host-level settings supplied before Fx constructs the application.
type Config struct {
	HTTPAddress  string
	DataDir      string
	Development  bool
	CustomScript string
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

// DefaultConfig returns local-only defaults suitable for development and smoke tests.
func DefaultConfig() Config {
	return Config{
		HTTPAddress: "127.0.0.1:8080",
		DataDir:     "./data",
	}
}
