package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hacktohell/rbe_gocacheprog/internal/config"
)

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("GOCACHEPROG_TARGET", "localhost:9092")
	t.Setenv("GOCACHEPROG_INSTANCE", "test-instance")
	t.Setenv("GOCACHEPROG_CACHE_SIZE_MB", "512")
	t.Setenv("GOCACHEPROG_LOG_LEVEL", "debug")
	t.Setenv("GOCACHEPROG_CACHE_DIR", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Target != "localhost:9092" {
		t.Errorf("Target = %q", cfg.Target)
	}
	if cfg.InstanceName != "test-instance" {
		t.Errorf("InstanceName = %q", cfg.InstanceName)
	}
	if cfg.CacheSizeMB != 512 {
		t.Errorf("CacheSizeMB = %d", cfg.CacheSizeMB)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
}

func TestLoadMissingTarget(t *testing.T) {
	t.Setenv("GOCACHEPROG_TARGET", "")
	_, err := config.Load()
	if err == nil {
		t.Error("expected error when target is missing")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	// Create a temp config file
	dir := t.TempDir()
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "gocacheprog")
	os.MkdirAll(configDir, 0o700)

	fileCfg := map[string]any{
		"target":        "file-target:443",
		"instance_name": "from-file",
		"cache_dir":     dir,
	}
	data, _ := json.Marshal(fileCfg)
	os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600)

	// Env should override
	t.Setenv("HOME", home)
	t.Setenv("GOCACHEPROG_TARGET", "env-target:443")
	t.Setenv("GOCACHEPROG_CACHE_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Target != "env-target:443" {
		t.Errorf("Target = %q, want env-target:443", cfg.Target)
	}
	if cfg.InstanceName != "from-file" {
		t.Errorf("InstanceName = %q, want from-file", cfg.InstanceName)
	}
}

func TestCacheSizeBytes(t *testing.T) {
	cfg := &config.Config{CacheSizeMB: 100}
	got := cfg.CacheSizeBytes()
	want := int64(100 * 1024 * 1024)
	if got != want {
		t.Errorf("CacheSizeBytes() = %d, want %d", got, want)
	}
}

func TestDefaults(t *testing.T) {
	t.Setenv("GOCACHEPROG_TARGET", "localhost:9092")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InstanceName != "juls" {
		t.Errorf("default InstanceName = %q, want juls", cfg.InstanceName)
	}
	if cfg.CacheSizeMB != 10240 {
		t.Errorf("default CacheSizeMB = %d, want 10240", cfg.CacheSizeMB)
	}
	if cfg.Workers < 1 {
		t.Errorf("default Workers = %d", cfg.Workers)
	}
}
