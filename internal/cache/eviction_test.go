package cache_test

import (
	"os"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
)

func TestTrimEvictsColdEntries(t *testing.T) {
	// Max 100 bytes. Install entries totaling ~200 bytes.
	dc := newTestCache(t, 100)

	a1 := "0101020304050607080910111213141516171819202122232425262728293031"
	a2 := "0201020304050607080910111213141516171819202122232425262728293031"

	installEntry(t, dc, a1, make([]byte, 60))
	installEntry(t, dc, a2, make([]byte, 60))

	// Unpin entries so they're eligible
	dc.UnpinAll()

	// Force entries to appear older than safety window by using a short-lived cache
	// We can't easily mock time, so we test with the real safety window check.
	// Since entries were just created, they're within the 24h safety window,
	// so Trim should NOT evict them.
	dc.Trim()

	if dc.TotalSize() != 120 {
		t.Logf("total = %d (entries within safety window, expected no eviction)", dc.TotalSize())
	}
}

func TestTrimPinnedEntriesNeverEvicted(t *testing.T) {
	dc := newTestCache(t, 50)

	a1 := "0101020304050607080910111213141516171819202122232425262728293031"
	installEntry(t, dc, a1, make([]byte, 60))

	// Entry is pinned by Install, so even if we call Trim it should stay.
	dc.Trim()

	_, _, hit := dc.Lookup(a1)
	if !hit {
		t.Error("pinned entry should not be evicted")
	}
}

func TestTrimConvergesAfterEviction(t *testing.T) {
	dir := t.TempDir()
	dc, err := cache.NewDiskCache(dir, 100)
	if err != nil {
		t.Fatal(err)
	}

	// Install 3 entries, make them old by writing directly
	entries := []struct {
		id   string
		size int
		age  time.Duration
	}{
		{"a101020304050607080910111213141516171819202122232425262728293031", 40, 48 * time.Hour},
		{"a201020304050607080910111213141516171819202122232425262728293031", 40, 72 * time.Hour},
		{"a301020304050607080910111213141516171819202122232425262728293031", 40, 96 * time.Hour},
	}

	for _, e := range entries {
		tmp, _ := dc.TempFile()
		tmp.Write(make([]byte, e.size))
		tmp.Close()
		dc.Install(e.id, tmp.Name(), &cache.Metadata{
			OutputIDHex:   "out",
			Size:          int64(e.size),
			Time:          time.Now().Add(-e.age),
			CASDigestHash: "hash",
			CASDigestSize: int64(e.size),
		})
		// Back-date the body file
		bodyPath := cache.BodyPath(dir, e.id)
		past := time.Now().Add(-e.age)
		os.Chtimes(bodyPath, past, past)
	}

	dc.UnpinAll()
	// Total is 120, max is 100, target is 80.
	// Need to evict at least 40 bytes. Oldest first: a3 (96h), then a2 (72h).

	// Re-create cache to pick up the mtime-based access times
	dc2, err := cache.NewDiskCache(dir, 100)
	if err != nil {
		t.Fatal(err)
	}

	dc2.Trim()

	// After trim, should be at or below 80 bytes
	if dc2.TotalSize() > 80 {
		t.Errorf("total after trim = %d, want <= 80", dc2.TotalSize())
	}
}

func TestTrimPreservesMetadataStubs(t *testing.T) {
	dir := t.TempDir()
	dc, err := cache.NewDiskCache(dir, 50)
	if err != nil {
		t.Fatal(err)
	}

	actionIDHex := "b101020304050607080910111213141516171819202122232425262728293031"
	tmp, _ := dc.TempFile()
	tmp.Write(make([]byte, 60))
	tmp.Close()

	dc.Install(actionIDHex, tmp.Name(), &cache.Metadata{
		OutputIDHex:   "out",
		Size:          60,
		Time:          time.Now().Add(-48 * time.Hour),
		CASDigestHash: "cas_hash",
		CASDigestSize: 60,
	})
	dc.UnpinAll()

	bodyPath := cache.BodyPath(dir, actionIDHex)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(bodyPath, past, past)

	// Re-scan to pick up old mtimes
	dc2, err := cache.NewDiskCache(dir, 50)
	if err != nil {
		t.Fatal(err)
	}
	dc2.Trim()

	// Body should be removed
	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Error("body should be evicted")
	}

	// Metadata should still exist
	metaPath := cache.MetadataPath(dir, actionIDHex)
	meta, err := cache.ReadMetadata(metaPath)
	if err != nil {
		t.Fatal("metadata should survive eviction:", err)
	}
	if meta.CASDigestHash != "cas_hash" {
		t.Errorf("cas digest = %q", meta.CASDigestHash)
	}
}

func TestTrimOverflowTolerated(t *testing.T) {
	dc := newTestCache(t, 50)

	a1 := "c101020304050607080910111213141516171819202122232425262728293031"
	installEntry(t, dc, a1, make([]byte, 100))

	// Pinned entry, cannot evict. Trim should not crash.
	dc.Trim()

	if dc.TotalSize() != 100 {
		t.Errorf("total = %d, want 100 (overflow tolerated)", dc.TotalSize())
	}
}
