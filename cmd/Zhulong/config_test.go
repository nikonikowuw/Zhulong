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
