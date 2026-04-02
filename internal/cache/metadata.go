// Package cache implements a bounded local disk cache for GOCACHEPROG.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Metadata is the per-entry metadata stored as a JSON file (-m suffix).
type Metadata struct {
	OutputIDHex   string    `json:"output_id_hex"`
	Size          int64     `json:"size"`
	Time          time.Time `json:"time"`
	CASDigestHash string    `json:"cas_digest_hash"`
	CASDigestSize int64     `json:"cas_digest_size"`
}

// WriteMetadata atomically writes metadata to the given path.
func WriteMetadata(path string, m *Metadata) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir metadata dir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp metadata: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename metadata: %w", err)
	}
	return nil
}

// ReadMetadata reads and parses metadata from the given path.
func ReadMetadata(path string) (*Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal metadata %s: %w", path, err)
	}
	return &m, nil
}

// MetadataPath returns the metadata file path for a given actionIDHex.
func MetadataPath(cacheDir, actionIDHex string) string {
	return filepath.Join(cacheDir, actionIDHex[:2], actionIDHex+"-m")
}

// BodyPath returns the body file path for a given actionIDHex.
func BodyPath(cacheDir, actionIDHex string) string {
	return filepath.Join(cacheDir, actionIDHex[:2], actionIDHex+"-d")
}
