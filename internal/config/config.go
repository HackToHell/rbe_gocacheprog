// Package config loads gocacheprog configuration from environment variables and config file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Duration is a time.Duration that can be unmarshalled from JSON as either
// a string ("10s", "5m") or a number (nanoseconds).
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch val := v.(type) {
	case string:
		dur, err := time.ParseDuration(val)
		if err != nil {
			return err
		}
		d.Duration = dur
	case float64:
		d.Duration = time.Duration(int64(val))
	default:
		return fmt.Errorf("invalid duration: %v", v)
	}
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// Config holds all gocacheprog configuration.
type Config struct {
	Target            string   `json:"target"`
	InstanceName      string   `json:"instance_name"`
	CacheDir          string   `json:"cache_dir"`
	CacheSizeMB       int64    `json:"cache_size_mb"`
	TLSCert           string   `json:"tls_cert"`
	TLSKey            string   `json:"tls_key"`
	TLSCA             string   `json:"tls_ca"`
	Workers           int      `json:"workers"`
	ConnectTimeout    Duration `json:"connect_timeout"`
	RequestTimeout    Duration `json:"request_timeout"`
	LogLevel          string   `json:"log_level"`
	MaxArtifactSizeMB int64    `json:"max_artifact_size_mb"`
	HealthAddr        string   `json:"health_addr"`
}

// Load returns configuration with precedence: env vars > config file > defaults.
func Load() (*Config, error) {
	cfg := defaults()

	if err := loadFile(cfg); err != nil {
		// Config file is optional; ignore file-not-found.
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("config file: %w", err)
		}
	}

	applyEnv(cfg)

	if cfg.Target == "" {
		return nil, fmt.Errorf("GOCACHEPROG_TARGET is required")
	}

	return cfg, nil
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		InstanceName:      "",
		CacheDir:          filepath.Join(home, ".cache", "gocacheprog"),
		CacheSizeMB:       10240,
		Workers:           runtime.GOMAXPROCS(0) * 2,
		ConnectTimeout:    Duration{10 * time.Second},
		RequestTimeout:    Duration{60 * time.Second},
		LogLevel:          "info",
		MaxArtifactSizeMB: 512, // 512 MiB default max artifact size
	}
}

func configFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gocacheprog", "config.json")
}

func loadFile(cfg *Config) error {
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("GOCACHEPROG_TARGET"); v != "" {
		cfg.Target = v
	}
	if v := os.Getenv("GOCACHEPROG_INSTANCE"); v != "" {
		cfg.InstanceName = v
	}
	if v := os.Getenv("GOCACHEPROG_CACHE_DIR"); v != "" {
		cfg.CacheDir = v
	}
	if v := os.Getenv("GOCACHEPROG_CACHE_SIZE_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.CacheSizeMB = n
		}
	}
	if v := os.Getenv("GOCACHEPROG_TLS_CERT"); v != "" {
		cfg.TLSCert = v
	}
	if v := os.Getenv("GOCACHEPROG_TLS_KEY"); v != "" {
		cfg.TLSKey = v
	}
	if v := os.Getenv("GOCACHEPROG_TLS_CA"); v != "" {
		cfg.TLSCA = v
	}
	if v := os.Getenv("GOCACHEPROG_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Workers = n
		}
	}
	if v := os.Getenv("GOCACHEPROG_CONNECT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ConnectTimeout = Duration{d}
		}
	}
	if v := os.Getenv("GOCACHEPROG_REQUEST_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.RequestTimeout = Duration{d}
		}
	}
	if v := os.Getenv("GOCACHEPROG_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("GOCACHEPROG_MAX_ARTIFACT_SIZE_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			cfg.MaxArtifactSizeMB = n
		}
	}
	if v := os.Getenv("GOCACHEPROG_HEALTH_ADDR"); v != "" {
		cfg.HealthAddr = v
	}
}

// CacheSizeBytes returns the cache size target in bytes.
func (c *Config) CacheSizeBytes() int64 {
	return c.CacheSizeMB * 1024 * 1024
}

// MaxArtifactSizeBytes returns the max artifact size in bytes.
func (c *Config) MaxArtifactSizeBytes() int64 {
	return c.MaxArtifactSizeMB * 1024 * 1024
}
