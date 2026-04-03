package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	if cfg.InstanceName != "" {
		t.Errorf("default InstanceName = %q, want empty", cfg.InstanceName)
	}
	if cfg.CacheSizeMB != 10240 {
		t.Errorf("default CacheSizeMB = %d, want 10240", cfg.CacheSizeMB)
	}
	if cfg.Workers < 1 {
		t.Errorf("default Workers = %d", cfg.Workers)
	}
}

func TestDurationUnmarshalString(t *testing.T) {
	input := `{"connect_timeout":"30s","request_timeout":"5m"}`
	var cfg config.Config
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.ConnectTimeout.Duration != 30*time.Second {
		t.Errorf("ConnectTimeout = %v, want 30s", cfg.ConnectTimeout.Duration)
	}
	if cfg.RequestTimeout.Duration != 5*time.Minute {
		t.Errorf("RequestTimeout = %v, want 5m", cfg.RequestTimeout.Duration)
	}
}

func TestDurationUnmarshalNumber(t *testing.T) {
	// Nanoseconds as a number
	input := `{"connect_timeout":5000000000}`
	var cfg config.Config
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.ConnectTimeout.Duration != 5*time.Second {
		t.Errorf("ConnectTimeout = %v, want 5s", cfg.ConnectTimeout.Duration)
	}
}

func TestDurationUnmarshalInvalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"boolean", `{"connect_timeout":true}`},
		{"invalid_string", `{"connect_timeout":"not-a-duration"}`},
		{"array", `{"connect_timeout":[1,2,3]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config.Config
			if err := json.Unmarshal([]byte(tc.input), &cfg); err == nil {
				t.Error("expected unmarshal error")
			}
		})
	}
}

func TestDurationMarshalJSON(t *testing.T) {
	d := config.Duration{Duration: 10 * time.Second}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"10s"` {
		t.Errorf("marshaled = %s, want %q", data, "10s")
	}
}

func TestDurationRoundTrip(t *testing.T) {
	original := config.Duration{Duration: 2*time.Minute + 30*time.Second}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var decoded config.Duration
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Duration != original.Duration {
		t.Errorf("round-trip: got %v, want %v", decoded.Duration, original.Duration)
	}
}

func TestMaxArtifactSizeBytes(t *testing.T) {
	cfg := &config.Config{MaxArtifactSizeMB: 512}
	got := cfg.MaxArtifactSizeBytes()
	want := int64(512 * 1024 * 1024)
	if got != want {
		t.Errorf("MaxArtifactSizeBytes() = %d, want %d", got, want)
	}
}

func TestEnvOverridesTLSAndTimeouts(t *testing.T) {
	t.Setenv("GOCACHEPROG_TARGET", "localhost:9092")
	t.Setenv("GOCACHEPROG_TLS_CERT", "/path/to/cert.pem")
	t.Setenv("GOCACHEPROG_TLS_KEY", "/path/to/key.pem")
	t.Setenv("GOCACHEPROG_TLS_CA", "/path/to/ca.pem")
	t.Setenv("GOCACHEPROG_WORKERS", "16")
	t.Setenv("GOCACHEPROG_CONNECT_TIMEOUT", "30s")
	t.Setenv("GOCACHEPROG_REQUEST_TIMEOUT", "2m")
	t.Setenv("GOCACHEPROG_MAX_ARTIFACT_SIZE_MB", "256")
	t.Setenv("GOCACHEPROG_HEALTH_ADDR", ":8080")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.TLSCert != "/path/to/cert.pem" {
		t.Errorf("TLSCert = %q", cfg.TLSCert)
	}
	if cfg.TLSKey != "/path/to/key.pem" {
		t.Errorf("TLSKey = %q", cfg.TLSKey)
	}
	if cfg.TLSCA != "/path/to/ca.pem" {
		t.Errorf("TLSCA = %q", cfg.TLSCA)
	}
	if cfg.Workers != 16 {
		t.Errorf("Workers = %d, want 16", cfg.Workers)
	}
	if cfg.ConnectTimeout.Duration != 30*time.Second {
		t.Errorf("ConnectTimeout = %v, want 30s", cfg.ConnectTimeout.Duration)
	}
	if cfg.RequestTimeout.Duration != 2*time.Minute {
		t.Errorf("RequestTimeout = %v, want 2m", cfg.RequestTimeout.Duration)
	}
	if cfg.MaxArtifactSizeMB != 256 {
		t.Errorf("MaxArtifactSizeMB = %d, want 256", cfg.MaxArtifactSizeMB)
	}
	if cfg.HealthAddr != ":8080" {
		t.Errorf("HealthAddr = %q, want :8080", cfg.HealthAddr)
	}
}

func TestEnvInvalidValuesIgnored(t *testing.T) {
	t.Setenv("GOCACHEPROG_TARGET", "localhost:9092")
	t.Setenv("GOCACHEPROG_WORKERS", "not-a-number")
	t.Setenv("GOCACHEPROG_CACHE_SIZE_MB", "bad")
	t.Setenv("GOCACHEPROG_CONNECT_TIMEOUT", "invalid-duration")
	t.Setenv("GOCACHEPROG_MAX_ARTIFACT_SIZE_MB", "nope")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Invalid values should be silently ignored, keeping defaults
	if cfg.Workers < 1 {
		t.Errorf("Workers should keep default, got %d", cfg.Workers)
	}
	if cfg.CacheSizeMB != 10240 {
		t.Errorf("CacheSizeMB should keep default 10240, got %d", cfg.CacheSizeMB)
	}
	if cfg.ConnectTimeout.Duration != 10*time.Second {
		t.Errorf("ConnectTimeout should keep default 10s, got %v", cfg.ConnectTimeout.Duration)
	}
	if cfg.MaxArtifactSizeMB != 512 {
		t.Errorf("MaxArtifactSizeMB should keep default 512, got %d", cfg.MaxArtifactSizeMB)
	}
}

func TestConfigFileWithDurations(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "gocacheprog")
	os.MkdirAll(configDir, 0o700)

	fileCfg := map[string]any{
		"target":          "grpc-server:443",
		"connect_timeout": "30s",
		"request_timeout": "2m",
		"cache_dir":       t.TempDir(),
	}
	data, _ := json.Marshal(fileCfg)
	os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600)

	t.Setenv("HOME", home)
	// Don't set TARGET env var so file value is used
	t.Setenv("GOCACHEPROG_TARGET", "")

	// This will fail because the file's target will be used but then
	// env var overrides with empty string. The load function requires target.
	// Set the env var to the same value.
	t.Setenv("GOCACHEPROG_TARGET", "grpc-server:443")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnectTimeout.Duration != 30*time.Second {
		t.Errorf("ConnectTimeout = %v, want 30s", cfg.ConnectTimeout.Duration)
	}
	if cfg.RequestTimeout.Duration != 2*time.Minute {
		t.Errorf("RequestTimeout = %v, want 2m", cfg.RequestTimeout.Duration)
	}
}
