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
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[data]\ndirectory = '/srv/zhulong'\n[person]\nfaces_directory = '/srv/custom_faces'\n[algorithm]\ntarget = 'rk3588'\n[logging]\nlevel = 'warn'\n")

	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.HTTPAddress != "127.0.0.1:9000" || got.DataDir != "/srv/zhulong" || got.FacesDir != "/srv/custom_faces" || got.AlgorithmTarget != "rk3588" || got.Logging.Level != "warn" {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestLoadConfigInheritsLoggingDefaultsAndResolvesConfigPath(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n")
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
		t.Fatalf("config did not inherit logging defaults: %+v", got.Logging)
	}
	if got.Logging.Sampling.Enabled || got.Logging.Rotation.Enabled {
		t.Fatalf("logging options unexpectedly enabled: %+v", got.Logging)
	}
}

func TestLoadConfigParsesLoggingPolicyAndExample(t *testing.T) {
	path := writeConfig(t, "[logging]\nlevel = 'debug'\nformat = 'console'\noutput = 'stdout'\n[logging.sampling]\nenabled = true\ninitial = 2\nthereafter = 5\n[logging.rotation]\nenabled = true\nmax_size_mb = 8\nmax_backups = 3\nmax_age_days = 7\ncompress = true\nmax_total_size_mb = 24\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.Logging.Level != "debug" || got.Logging.Format != "console" || got.Logging.Output != "stdout" {
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

// TestLoadConfigRejectsRemovedLoggingDevelopmentKey pins the removal. The switch
// only ever selected a default format, and an unknown key is a boot error here; a
// stale config must say so instead of appearing to still configure something.
func TestLoadConfigRejectsRemovedLoggingDevelopmentKey(t *testing.T) {
	path := writeConfig(t, "[logging]\ndevelopment = true\n")
	_, err := loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "has invalid keys: development") {
		t.Fatalf("expected the removed key to be rejected by name, got %v", err)
	}
}

func TestLoadConfigRejectsUnknownLoggingKeys(t *testing.T) {
	path := writeConfig(t, "[logging]\nlevel = 'debug'\nlevele = 'debug'\n")
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
	if got.HTTPAddress != "127.0.0.1:9000" || got.DataDir != defaults.DataDir || got.FacesDir != expectedFacesDir || got.AlgorithmTarget != defaults.AlgorithmTarget || got.SessionTTL != defaults.SessionTTL {
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

func TestLoadConfigParsesTLSConfig(t *testing.T) {
	// 1. Defaults to off
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.TLSMode != "off" || got.TLSCertFile != "" || got.TLSKeyFile != "" {
		t.Fatalf("unexpected TLS defaults: %+v", got)
	}

	// 2. Auto mode
	path = writeConfig(t, "[http]\naddress = '0.0.0.0:8000'\ntls_mode = 'auto'\n")
	got, err = loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.TLSMode != "auto" {
		t.Fatalf("expected tls_mode 'auto', got %q", got.TLSMode)
	}

	// 3. Manual mode valid
	path = writeConfig(t, "[http]\naddress = '0.0.0.0:8000'\ntls_mode = 'manual'\ncert_file = '/path/to/cert.pem'\nkey_file = '/path/to/key.pem'\n")
	got, err = loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.TLSMode != "manual" || got.TLSCertFile != "/path/to/cert.pem" || got.TLSKeyFile != "/path/to/key.pem" {
		t.Fatalf("unexpected manual TLS config: %+v", got)
	}

	// 4. Manual mode missing cert_file
	path = writeConfig(t, "[http]\naddress = '0.0.0.0:8000'\ntls_mode = 'manual'\n")
	_, err = loadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for manual mode without cert_file")
	}

	// 5. Invalid mode
	path = writeConfig(t, "[http]\naddress = '0.0.0.0:8000'\ntls_mode = 'invalid_mode'\n")
	_, err = loadConfigFile(path)
	if err == nil {
		t.Fatal("expected error for invalid tls_mode")
	}
}

func TestLoadConfigDecodeSection(t *testing.T) {
	// 1. 默认值
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.Decode.Backend != "auto" || !got.Decode.AllowSoftwareFallback || got.Decode.MaxSessions != 8 {
		t.Fatalf("unexpected default decode config: %+v", got.Decode)
	}

	// 2. 自定义有效配置
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[decode]\nbackend = 'mpp'\nallow_software_fallback = false\nmax_sessions = 4\n")
	got, err = loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.Decode.Backend != "mpp" || got.Decode.AllowSoftwareFallback || got.Decode.MaxSessions != 4 {
		t.Fatalf("unexpected custom decode config: %+v", got.Decode)
	}

	// 3. 非法 backend
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[decode]\nbackend = 'cuda'\n")
	_, err = loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("expected invalid backend error, got %v", err)
	}

	// 4. 负数 max_sessions
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[decode]\nmax_sessions = -1\n")
	_, err = loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "max_sessions") {
		t.Fatalf("expected negative max_sessions error, got %v", err)
	}
}

func TestLoadConfigAlertPush(t *testing.T) {
	// 1. Defaults
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n")
	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.AlertPushPublicBaseURL != "" || got.AlertPushSignedURLTTL != 24*time.Hour {
		t.Fatalf("unexpected alert push defaults: url=%q, ttl=%v", got.AlertPushPublicBaseURL, got.AlertPushSignedURLTTL)
	}

	// 2. Custom valid config
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[alertpush]\npublic_base_url = 'http://192.168.1.50:8000/'\nsigned_url_ttl = '12h'\n")
	got, err = loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.AlertPushPublicBaseURL != "http://192.168.1.50:8000" || got.AlertPushSignedURLTTL != 12*time.Hour {
		t.Fatalf("unexpected custom alertpush config: url=%q, ttl=%v", got.AlertPushPublicBaseURL, got.AlertPushSignedURLTTL)
	}

	// 3. Invalid scheme
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[alertpush]\npublic_base_url = 'ftp://192.168.1.50:8000'\n")
	_, err = loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("expected scheme error, got %v", err)
	}

	// 4. TTL too short
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[alertpush]\nsigned_url_ttl = '10s'\n")
	_, err = loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("expected out of bounds error for short TTL, got %v", err)
	}

	// 5. TTL too long
	path = writeConfig(t, "[http]\naddress = '127.0.0.1:8000'\n[alertpush]\nsigned_url_ttl = '200h'\n")
	_, err = loadConfigFile(path)
	if err == nil || !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("expected out of bounds error for long TTL, got %v", err)
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
