package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].ID != "ac" || cfg.Devices[0].MaxTemp != 30 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestLoadRejectsBadPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
[[devices]]
id = "ac"
[[devices.presets]]
name = "Too cold"
temp = 10
`), 0o644)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "Too cold") {
		t.Fatalf("err = %v", err)
	}
}

func TestCredentialsMustBePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.toml")
	os.WriteFile(path, []byte(`access_id = "x"`), 0o644)
	if _, err := LoadCredentials(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
	os.Chmod(path, 0o600)
	c, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Region != "us" {
		t.Fatalf("region = %q, want us default", c.Region)
	}
	if missing := c.Missing("ac"); len(missing) != 3 {
		t.Fatalf("missing = %v", missing)
	}
}
