package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/app"
)

func TestLoadConfigUsesTOMLAndDefaults(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[data]\ndirectory = '/srv/zhulong'\n[person]\nfaces_directory = '/srv/custom_faces'\n[algorithm]\ntarget = 'rk3588'\n[logging]\ndevelopment = true\n")

	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.HTTPAddress != "127.0.0.1:9000" || got.DataDir != "/srv/zhulong" || got.FacesDir != "/srv/custom_faces" || got.AlgorithmTarget != "rk3588" || !got.Development {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestLoadConfigKeepsLegacyLoggingFieldsAndResolvesConfigPath(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[logging]\ndevelopment = false\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if got.ConfigPath != filepath.Clean(absPath) {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, filepath.Clean(absPath))
	}
	if got.Logging.Level != "info" || got.Logging.Output != "stderr" || got.Logging.Format != "" {
		t.Fatalf("legacy config did not inherit logging defaults: %+v", got.Logging)
	}
	if got.Logging.Sampling.Enabled || got.Logging.Rotation.Enabled || got.Development {
		t.Fatalf("legacy logging options unexpectedly enabled: %+v", got.Logging)
	}
}

func TestLoadConfigParsesLoggingPolicyAndExample(t *testing.T) {
	path := writeConfig(t, "[logging]\ndevelopment = true\nlevel = 'debug'\nformat = 'console'\noutput = 'stdout'\n[logging.sampling]\nenabled = true\ninitial = 2\nthereafter = 5\n[logging.rotation]\nenabled = true\nmax_size_mb = 8\nmax_backups = 3\nmax_age_days = 7\ncompress = true\nmax_total_size_mb = 24\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if !got.Development || !got.Logging.Development || got.Logging.Level != "debug" || got.Logging.Format != "console" || got.Logging.Output != "stdout" {
		t.Fatalf("unexpected logging policy: %+v", got.Logging)
	}
	if !got.Logging.Sampling.Enabled || got.Logging.Sampling.Initial != 2 || got.Logging.Sampling.Thereafter != 5 {
		t.Fatalf("unexpected sampling policy: %+v", got.Logging.Sampling)
	}
	if !got.Logging.Rotation.Enabled || got.Logging.Rotation.MaxSizeMB != 8 || got.Logging.Rotation.MaxBackups != 3 || got.Logging.Rotation.MaxAgeDays != 7 || !got.Logging.Rotation.Compress || got.Logging.Rotation.MaxTotalSizeMB != 24 {
		t.Fatalf("unexpected rotation policy: %+v", got.Logging.Rotation)
	}

	examplePath := filepath.Join("..", "..", "config.toml.example")
	if _, err := loadConfigFile(examplePath); err != nil {
		t.Fatalf("example config does not match strict schema: %v", err)
	}
}

func TestLoadConfigRejectsUnknownLoggingKeys(t *testing.T) {
	path := writeConfig(t, "[logging]\ndevelopment = false\nlevele = 'debug'\n")
	_, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "levele") {
		t.Fatalf("expected unknown logging key error, got %v", err)
	}
}

func TestLoadConfigInheritsFacesDirFromDataDir(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[data]\ndirectory = '/custom/data'\n")

	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.FacesDir != "/custom/data/faces" {
		t.Fatalf("expected FacesDir to be /custom/data/faces, got %s", got.FacesDir)
	}
}

func TestLoadConfigKeepsDefaultsForOmittedValues(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n")

	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	defaults := app.DefaultConfig()
	expectedFacesDir := filepath.Join(defaults.DataDir, "faces")
	if got.HTTPAddress != "127.0.0.1:9000" || got.DataDir != defaults.DataDir || got.FacesDir != expectedFacesDir || got.AlgorithmTarget != defaults.AlgorithmTarget || got.Development || got.SessionTTL != defaults.SessionTTL {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestLoadConfigParsesAuthSessionTTL(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[auth]\nsession_ttl = '12h'\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.SessionTTL != 12*time.Hour {
		t.Fatalf("expected SessionTTL 12h, got %v", got.SessionTTL)
	}

	// Test day format
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[auth]\nsession_ttl = '7d'\n")
	got, err = loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.SessionTTL != 7*24*time.Hour {
		t.Fatalf("expected SessionTTL 7d, got %v", got.SessionTTL)
	}

	// Test invalid format rejected
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[auth]\nsession_ttl = 'bad-ttl'\n")
	_, err = loadConfigFile(path)
	if err == nil {
		t.Fatal("expected invalid session_ttl to fail")
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	path := writeConfig(t, "[http]\nadress = '127.0.0.1:9000'\n")

	_, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "adress") {
		t.Fatalf("expected unknown TOML key error, got %v", err)
	}
}

func TestLoadConfigRejectsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.toml")
	_, err := loadConfigFile(path)
	if err == nil {
		t.Fatal("expected missing configuration file to fail")
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
