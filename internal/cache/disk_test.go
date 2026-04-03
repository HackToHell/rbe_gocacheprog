package cache_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
)

func newTestCache(t *testing.T, maxSize int64) *cache.DiskCache {
	t.Helper()
	dir := t.TempDir()
	dc, err := cache.NewDiskCache(dir, maxSize)
	if err != nil {
		t.Fatal(err)
	}
	return dc
}

func installEntry(t *testing.T, dc *cache.DiskCache, actionIDHex string, data []byte) string {
	t.Helper()
	tmp, err := dc.TempFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write(data); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	meta := &cache.Metadata{
		OutputIDHex:   "output_" + actionIDHex,
		Size:          int64(len(data)),
		Time:          time.Now(),
		CASDigestHash: "hash_" + actionIDHex,
		CASDigestSize: int64(len(data)),
	}
	path, err := dc.Install(actionIDHex, tmp.Name(), meta)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInstallAndLookup(t *testing.T) {
	dc := newTestCache(t, 1024*1024)
	actionIDHex := "aa01020304050607080910111213141516171819202122232425262728293031"
	data := []byte("hello cached world")

	path := installEntry(t, dc, actionIDHex, data)

	// Verify file exists
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("body mismatch")
	}

	// Lookup should hit
	meta, bodyPath, hit := dc.Lookup(actionIDHex)
	if !hit {
		t.Fatal("expected hit")
	}
	if bodyPath != path {
		t.Errorf("path = %q, want %q", bodyPath, path)
	}
	if meta.OutputIDHex != "output_"+actionIDHex {
		t.Errorf("output id = %q", meta.OutputIDHex)
	}
	if meta.Size != int64(len(data)) {
		t.Errorf("size = %d", meta.Size)
	}
}

func TestLookupMiss(t *testing.T) {
	dc := newTestCache(t, 1024*1024)
	_, _, hit := dc.Lookup("bb01020304050607080910111213141516171819202122232425262728293031")
	if hit {
		t.Error("expected miss")
	}
}

func TestMetadataStub(t *testing.T) {
	dc := newTestCache(t, 1024*1024)
	actionIDHex := "cc01020304050607080910111213141516171819202122232425262728293031"
	data := []byte("stub test")

	path := installEntry(t, dc, actionIDHex, data)

	// Remove body to simulate eviction
	os.Remove(path)

	// Lookup should miss (no body)
	_, _, hit := dc.Lookup(actionIDHex)
	if hit {
		t.Error("expected miss after body removal")
	}

	// HasMetadataStub should return true
	meta, isStub := dc.HasMetadataStub(actionIDHex)
	if !isStub {
		t.Fatal("expected stub")
	}
	if meta.CASDigestHash != "hash_"+actionIDHex {
		t.Errorf("CAS digest = %q", meta.CASDigestHash)
	}
}

func TestRemove(t *testing.T) {
	dc := newTestCache(t, 1024*1024)
	actionIDHex := "dd01020304050607080910111213141516171819202122232425262728293031"
	installEntry(t, dc, actionIDHex, []byte("remove me"))

	dc.Remove(actionIDHex)

	_, _, hit := dc.Lookup(actionIDHex)
	if hit {
		t.Error("expected miss after remove")
	}

	metaPath := cache.MetadataPath(dc.Dir(), actionIDHex)
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Error("metadata should be deleted")
	}
}

func TestCorruptMetadataCleanup(t *testing.T) {
	dc := newTestCache(t, 1024*1024)
	actionIDHex := "ee01020304050607080910111213141516171819202122232425262728293031"

	// Write corrupt metadata
	metaPath := cache.MetadataPath(dc.Dir(), actionIDHex)
	os.MkdirAll(filepath.Dir(metaPath), 0o700)
	os.WriteFile(metaPath, []byte("not json"), 0o600)

	// Lookup should miss (corrupt metadata)
	_, _, hit := dc.Lookup(actionIDHex)
	if hit {
		t.Error("expected miss with corrupt metadata")
	}
}

func TestStartupRecovery(t *testing.T) {
	dir := t.TempDir()

	// Pre-populate with a body + metadata
	actionIDHex := "ff01020304050607080910111213141516171819202122232425262728293031"
	subDir := filepath.Join(dir, actionIDHex[:2])
	os.MkdirAll(subDir, 0o700)

	bodyPath := filepath.Join(subDir, actionIDHex+"-d")
	data := []byte("recovered data")
	os.WriteFile(bodyPath, data, 0o600)

	metaPath := filepath.Join(subDir, actionIDHex+"-m")
	cache.WriteMetadata(metaPath, &cache.Metadata{
		OutputIDHex:   "recovered_output",
		Size:          int64(len(data)),
		Time:          time.Now(),
		CASDigestHash: "recovered_hash",
		CASDigestSize: int64(len(data)),
	})

	// Create new cache - should scan and recover
	dc, err := cache.NewDiskCache(dir, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	if dc.TotalSize() != int64(len(data)) {
		t.Errorf("total size = %d, want %d", dc.TotalSize(), len(data))
	}

	meta, bp, hit := dc.Lookup(actionIDHex)
	if !hit {
		t.Fatal("expected hit after recovery")
	}
	if meta.OutputIDHex != "recovered_output" {
		t.Errorf("output id = %q", meta.OutputIDHex)
	}
	if bp != bodyPath {
		t.Errorf("body path = %q, want %q", bp, bodyPath)
	}
}

func TestBodyAndMetadataPaths(t *testing.T) {
	dir := "/cache"
	hex := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	bp := cache.BodyPath(dir, hex)
	mp := cache.MetadataPath(dir, hex)

	if bp != "/cache/ab/"+hex+"-d" {
		t.Errorf("body path = %q", bp)
	}
	if mp != "/cache/ab/"+hex+"-m" {
		t.Errorf("meta path = %q", mp)
	}
}

func TestReadMetadataLargerThanPoolBuffer(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "ab")
	os.MkdirAll(subDir, 0o700)
	path := filepath.Join(subDir, "testmeta-m")

	// Create metadata with fields large enough to exceed the 512-byte pool buffer.
	longHash := strings.Repeat("ab", 256) // 512 chars
	meta := &cache.Metadata{
		OutputIDHex:   strings.Repeat("cd", 128), // 256 chars
		Size:          99999,
		Time:          time.Now(),
		CASDigestHash: longHash,
		CASDigestSize: 99999,
	}
	if err := cache.WriteMetadata(path, meta); err != nil {
		t.Fatal(err)
	}

	// Verify the file is actually larger than 512 bytes.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 512 {
		t.Fatalf("metadata file is %d bytes, expected >512 to exercise fallback", info.Size())
	}

	got, err := cache.ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata failed on large file: %v", err)
	}
	if got.OutputIDHex != meta.OutputIDHex {
		t.Errorf("OutputIDHex mismatch: got len %d, want len %d", len(got.OutputIDHex), len(meta.OutputIDHex))
	}
	if got.CASDigestHash != longHash {
		t.Errorf("CASDigestHash mismatch: got len %d, want len %d", len(got.CASDigestHash), len(longHash))
	}
	if got.Size != 99999 {
		t.Errorf("Size = %d, want 99999", got.Size)
	}
}

func TestTotalSize(t *testing.T) {
	dc := newTestCache(t, 1024*1024)
	a1 := "1101020304050607080910111213141516171819202122232425262728293031"
	a2 := "2201020304050607080910111213141516171819202122232425262728293031"

	installEntry(t, dc, a1, []byte("aaaa"))
	installEntry(t, dc, a2, []byte("bbbbbbbb"))

	if dc.TotalSize() != 12 {
		t.Errorf("total = %d, want 12", dc.TotalSize())
	}
}
