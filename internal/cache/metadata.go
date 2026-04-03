// Package cache implements a bounded local disk cache for GOCACHEPROG.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// readBufPool reuses read buffers to avoid per-call allocation in ReadMetadata.
var readBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 512)
		return &b
	},
}

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
	// Fast-path: skip MkdirAll if directory already exists (common case after first install).
	if _, err := os.Stat(dir); err != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mkdir metadata dir: %w", err)
		}
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp metadata: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp metadata: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync temp metadata: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp metadata: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename metadata: %w", err)
	}
	return nil
}

// ReadMetadata reads and parses metadata from the given path.
func ReadMetadata(path string) (*Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	bufp := readBufPool.Get().(*[]byte)
	buf := (*bufp)[:cap(*bufp)]

	// Metadata files are small (<256B). Read in one syscall.
	n, readErr := f.Read(buf)
	f.Close()

	if n == 0 && readErr != nil {
		*bufp = buf[:0]
		readBufPool.Put(bufp)
		return nil, readErr
	}

	// If we filled the buffer exactly, the file may be larger than our pool
	// buffer. Fall back to os.ReadFile for correctness.
	if n == len(buf) && readErr == nil {
		*bufp = buf[:0]
		readBufPool.Put(bufp)
		return readMetadataFull(path)
	}

	data := buf[:n]
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		*bufp = buf[:0]
		readBufPool.Put(bufp)
		return nil, fmt.Errorf("unmarshal metadata %s: %w", path, err)
	}

	*bufp = buf[:0]
	readBufPool.Put(bufp)
	return &m, nil
}

// readMetadataFull is the slow fallback for files larger than the pool buffer.
func readMetadataFull(path string) (*Metadata, error) {
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
	// Avoid filepath.Join overhead - these are simple concatenations.
	// actionIDHex[:2] is the shard prefix.
	var b strings.Builder
	b.Grow(len(cacheDir) + 1 + 2 + 1 + len(actionIDHex) + 2)
	b.WriteString(cacheDir)
	b.WriteByte(os.PathSeparator)
	b.WriteString(actionIDHex[:2])
	b.WriteByte(os.PathSeparator)
	b.WriteString(actionIDHex)
	b.WriteString("-m")
	return b.String()
}

// BodyPath returns the body file path for a given actionIDHex.
func BodyPath(cacheDir, actionIDHex string) string {
	var b strings.Builder
	b.Grow(len(cacheDir) + 1 + 2 + 1 + len(actionIDHex) + 2)
	b.WriteString(cacheDir)
	b.WriteByte(os.PathSeparator)
	b.WriteString(actionIDHex[:2])
	b.WriteByte(os.PathSeparator)
	b.WriteString(actionIDHex)
	b.WriteString("-d")
	return b.String()
}
