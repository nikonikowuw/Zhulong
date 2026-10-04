package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/app"
)

func TestLoadConfigUsesTOMLAndDefaults(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n[data]\ndirectory = '/srv/zhulong'\n[logging]\ndevelopment = true\n")

	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if got.HTTPAddress != "127.0.0.1:9000" || got.DataDir != "/srv/zhulong" || !got.Development {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestLoadConfigKeepsDefaultsForOmittedValues(t *testing.T) {
	path := writeConfig(t, "[http]\naddress = '127.0.0.1:9000'\n")

	got, err := loadConfigFile(path)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	defaults := app.DefaultConfig()
	if got.HTTPAddress != "127.0.0.1:9000" || got.DataDir != defaults.DataDir || got.Development {
		t.Fatalf("unexpected config: %+v", got)
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
