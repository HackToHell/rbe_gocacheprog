package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DiskCache manages local disk storage with LRU tracking.
type DiskCache struct {
	dir       string
	maxSize   int64
	safetyDur time.Duration // entries touched within this window are never evicted

	mu      sync.Mutex
	entries map[string]*entry // actionIDHex -> entry
	pinned  map[string]bool   // actionIDHex -> pinned for current process
	total   int64             // total size of body files
}

type entry struct {
	actionIDHex string
	size        int64
	accessTime  time.Time
	hasBody     bool
}

// NewDiskCache creates a new DiskCache with the given directory and size target.
func NewDiskCache(dir string, maxSize int64) (*DiskCache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	dc := &DiskCache{
		dir:       dir,
		maxSize:   maxSize,
		safetyDur: 24 * time.Hour,
		entries:   make(map[string]*entry),
		pinned:    make(map[string]bool),
	}

	dc.scan()
	return dc, nil
}

// Dir returns the cache directory.
func (dc *DiskCache) Dir() string { return dc.dir }

// Install atomically installs a body file and metadata for the given actionIDHex.
// The tempBodyPath must be on the same filesystem.
func (dc *DiskCache) Install(actionIDHex string, tempBodyPath string, meta *Metadata) (string, error) {
	bodyPath := BodyPath(dc.dir, actionIDHex)
	metaPath := MetadataPath(dc.dir, actionIDHex)

	dir := filepath.Dir(bodyPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	if err := os.Rename(tempBodyPath, bodyPath); err != nil {
		return "", fmt.Errorf("rename body: %w", err)
	}

	if err := WriteMetadata(metaPath, meta); err != nil {
		// Body is installed but metadata failed. Clean up body.
		os.Remove(bodyPath)
		return "", fmt.Errorf("write metadata: %w", err)
	}

	dc.mu.Lock()
	if old, ok := dc.entries[actionIDHex]; ok && old.hasBody {
		dc.total -= old.size
	}
	dc.entries[actionIDHex] = &entry{
		actionIDHex: actionIDHex,
		size:        meta.Size,
		accessTime:  time.Now(),
		hasBody:     true,
	}
	dc.pinned[actionIDHex] = true
	dc.total += meta.Size
	dc.mu.Unlock()

	return bodyPath, nil
}

// Lookup checks for a local cache hit. Returns metadata, body path, and whether it's a hit.
func (dc *DiskCache) Lookup(actionIDHex string) (*Metadata, string, bool) {
	metaPath := MetadataPath(dc.dir, actionIDHex)
	bodyPath := BodyPath(dc.dir, actionIDHex)

	meta, err := ReadMetadata(metaPath)
	if err != nil {
		return nil, "", false
	}

	if _, err := os.Stat(bodyPath); err != nil {
		// Metadata exists but body is gone (evicted stub).
		return meta, "", false
	}

	dc.mu.Lock()
	if e, ok := dc.entries[actionIDHex]; ok {
		e.accessTime = time.Now()
	}
	dc.pinned[actionIDHex] = true
	dc.mu.Unlock()

	return meta, bodyPath, true
}

// HasMetadataStub returns true if metadata exists but the body is missing.
func (dc *DiskCache) HasMetadataStub(actionIDHex string) (*Metadata, bool) {
	metaPath := MetadataPath(dc.dir, actionIDHex)
	bodyPath := BodyPath(dc.dir, actionIDHex)

	meta, err := ReadMetadata(metaPath)
	if err != nil {
		return nil, false
	}

	if _, err := os.Stat(bodyPath); err == nil {
		return nil, false // body exists, not a stub
	}

	return meta, true
}

// Pin marks an entry as pinned (will not be evicted).
func (dc *DiskCache) Pin(actionIDHex string) {
	dc.mu.Lock()
	dc.pinned[actionIDHex] = true
	dc.mu.Unlock()
}

// Remove deletes both body and metadata for an entry.
func (dc *DiskCache) Remove(actionIDHex string) {
	bodyPath := BodyPath(dc.dir, actionIDHex)
	metaPath := MetadataPath(dc.dir, actionIDHex)

	os.Remove(bodyPath)
	os.Remove(metaPath)

	dc.mu.Lock()
	if e, ok := dc.entries[actionIDHex]; ok {
		if e.hasBody {
			dc.total -= e.size
		}
		delete(dc.entries, actionIDHex)
	}
	delete(dc.pinned, actionIDHex)
	dc.mu.Unlock()
}

// TotalSize returns the current total size of cached body files.
func (dc *DiskCache) TotalSize() int64 {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.total
}

// TempFile creates a temp file in the cache directory for atomic installs.
func (dc *DiskCache) TempFile() (*os.File, error) {
	return os.CreateTemp(dc.dir, "tmp-*")
}

// UnpinAll unpins all entries (called on close).
func (dc *DiskCache) UnpinAll() {
	dc.mu.Lock()
	dc.pinned = make(map[string]bool)
	dc.mu.Unlock()
}

// scan rebuilds the in-memory index from disk on startup.
func (dc *DiskCache) scan() {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	prefixes, _ := os.ReadDir(dc.dir)
	for _, prefix := range prefixes {
		if !prefix.IsDir() || len(prefix.Name()) != 2 {
			continue
		}
		subDir := filepath.Join(dc.dir, prefix.Name())
		files, _ := os.ReadDir(subDir)
		for _, f := range files {
			name := f.Name()
			if len(name) < 3 {
				continue
			}
			suffix := name[len(name)-2:]
			actionIDHex := name[:len(name)-2]

			if suffix != "-d" {
				continue
			}

			info, err := f.Info()
			if err != nil {
				continue
			}

			dc.entries[actionIDHex] = &entry{
				actionIDHex: actionIDHex,
				size:        info.Size(),
				accessTime:  info.ModTime(),
				hasBody:     true,
			}
			dc.total += info.Size()
		}
	}
}

// Trim evicts cold body files until total size is at 80% of maxSize.
// It preserves pinned entries and entries touched within the safety window.
func (dc *DiskCache) Trim() {
	dc.mu.Lock()

	target := int64(float64(dc.maxSize) * 0.8)
	if dc.total <= target {
		dc.mu.Unlock()
		return
	}

	now := time.Now()
	var candidates []*entry
	for _, e := range dc.entries {
		if !e.hasBody {
			continue
		}
		if dc.pinned[e.actionIDHex] {
			continue
		}
		if now.Sub(e.accessTime) < dc.safetyDur {
			continue
		}
		candidates = append(candidates, e)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].accessTime.Before(candidates[j].accessTime)
	})

	var toEvict []string
	currentTotal := dc.total
	for _, e := range candidates {
		if currentTotal <= target {
			break
		}
		toEvict = append(toEvict, e.actionIDHex)
		currentTotal -= e.size
	}
	dc.mu.Unlock()

	// Evict outside the lock. Convert body to metadata-only stub.
	for _, actionIDHex := range toEvict {
		bodyPath := BodyPath(dc.dir, actionIDHex)
		os.Remove(bodyPath)

		dc.mu.Lock()
		if e, ok := dc.entries[actionIDHex]; ok && e.hasBody {
			dc.total -= e.size
			e.hasBody = false
		}
		dc.mu.Unlock()
	}
}
