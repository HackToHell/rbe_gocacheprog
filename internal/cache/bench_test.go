package cache_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
)

// --- Path computation (called every get/put) ---

func BenchmarkMetadataPath(b *testing.B) {
	dir := "/cache"
	hex := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	b.ResetTimer()
	for range b.N {
		_ = cache.MetadataPath(dir, hex)
	}
}

func BenchmarkBodyPath(b *testing.B) {
	dir := "/cache"
	hex := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	b.ResetTimer()
	for range b.N {
		_ = cache.BodyPath(dir, hex)
	}
}

// --- WriteMetadata (json.Marshal + write + sync + rename) ---

func BenchmarkWriteMetadata(b *testing.B) {
	dir := b.TempDir()
	subDir := filepath.Join(dir, "ab")
	os.MkdirAll(subDir, 0o700)
	path := filepath.Join(subDir, "testmeta-m")

	meta := &cache.Metadata{
		OutputIDHex:   "aabbccdd0123456789aabbccdd0123456789aabbccdd0123456789aabbccdd01",
		Size:          65536,
		Time:          time.Now(),
		CASDigestHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		CASDigestSize: 65536,
	}

	b.ResetTimer()
	for range b.N {
		if err := cache.WriteMetadata(path, meta); err != nil {
			b.Fatal(err)
		}
	}
}

// --- ReadMetadata (os.ReadFile + json.Unmarshal) ---

func BenchmarkReadMetadata(b *testing.B) {
	dir := b.TempDir()
	subDir := filepath.Join(dir, "ab")
	os.MkdirAll(subDir, 0o700)
	path := filepath.Join(subDir, "testmeta-m")

	meta := &cache.Metadata{
		OutputIDHex:   "aabbccdd0123456789aabbccdd0123456789aabbccdd0123456789aabbccdd01",
		Size:          65536,
		Time:          time.Now(),
		CASDigestHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		CASDigestSize: 65536,
	}
	if err := cache.WriteMetadata(path, meta); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for range b.N {
		_, err := cache.ReadMetadata(path)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- DiskCache.Install (mkdir + rename + WriteMetadata + mutex) ---

func BenchmarkDiskCacheInstall(b *testing.B) {
	for _, bodySize := range []int{64, 1024, 64 * 1024} {
		b.Run(byteSizeLabel(bodySize), func(b *testing.B) {
			dir := b.TempDir()
			dc, err := cache.NewDiskCache(dir, 1024*1024*1024)
			if err != nil {
				b.Fatal(err)
			}

			body := make([]byte, bodySize)
			for i := range body {
				body[i] = byte(i % 256)
			}

			b.ResetTimer()
			for i := range b.N {
				actionIDHex := fmt.Sprintf("%064x", i)
				tmp, err := dc.TempFile()
				if err != nil {
					b.Fatal(err)
				}
				if _, err := tmp.Write(body); err != nil {
					b.Fatal(err)
				}
				tmp.Close()

				meta := &cache.Metadata{
					OutputIDHex:   fmt.Sprintf("out_%064x", i),
					Size:          int64(bodySize),
					Time:          time.Now(),
					CASDigestHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					CASDigestSize: int64(bodySize),
				}
				if _, err := dc.Install(actionIDHex, tmp.Name(), meta); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- DiskCache.Lookup hit path (ReadMetadata + os.Stat + mutex) ---

func BenchmarkDiskCacheLookupHit(b *testing.B) {
	dir := b.TempDir()
	dc, err := cache.NewDiskCache(dir, 1024*1024*1024)
	if err != nil {
		b.Fatal(err)
	}

	actionIDHex := "aa01020304050607080910111213141516171819202122232425262728293031"
	body := make([]byte, 1024)
	tmp, err := dc.TempFile()
	if err != nil {
		b.Fatal(err)
	}
	tmp.Write(body)
	tmp.Close()

	meta := &cache.Metadata{
		OutputIDHex:   "output_" + actionIDHex,
		Size:          int64(len(body)),
		Time:          time.Now(),
		CASDigestHash: "hash_test",
		CASDigestSize: int64(len(body)),
	}
	dc.Install(actionIDHex, tmp.Name(), meta)

	b.ResetTimer()
	for range b.N {
		_, _, hit := dc.Lookup(actionIDHex)
		if !hit {
			b.Fatal("expected hit")
		}
	}
}

// --- DiskCache.Lookup miss path ---

func BenchmarkDiskCacheLookupMiss(b *testing.B) {
	dir := b.TempDir()
	dc, err := cache.NewDiskCache(dir, 1024*1024*1024)
	if err != nil {
		b.Fatal(err)
	}

	actionIDHex := "bb01020304050607080910111213141516171819202122232425262728293031"
	b.ResetTimer()
	for range b.N {
		_, _, _ = dc.Lookup(actionIDHex)
	}
}

// --- DiskCache.Lookup parallel contention ---

func BenchmarkDiskCacheLookupHitParallel(b *testing.B) {
	dir := b.TempDir()
	dc, err := cache.NewDiskCache(dir, 1024*1024*1024)
	if err != nil {
		b.Fatal(err)
	}

	actionIDHex := "cc01020304050607080910111213141516171819202122232425262728293031"
	body := make([]byte, 1024)
	tmp, err := dc.TempFile()
	if err != nil {
		b.Fatal(err)
	}
	tmp.Write(body)
	tmp.Close()

	meta := &cache.Metadata{
		OutputIDHex:   "output_" + actionIDHex,
		Size:          int64(len(body)),
		Time:          time.Now(),
		CASDigestHash: "hash_test",
		CASDigestSize: int64(len(body)),
	}
	dc.Install(actionIDHex, tmp.Name(), meta)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, hit := dc.Lookup(actionIDHex)
			if !hit {
				b.Fatal("expected hit")
			}
		}
	})
}

// --- DiskCache.HasMetadataStub (ReadMetadata + os.Stat for body absence) ---

func BenchmarkDiskCacheHasMetadataStub(b *testing.B) {
	dir := b.TempDir()
	dc, err := cache.NewDiskCache(dir, 1024*1024*1024)
	if err != nil {
		b.Fatal(err)
	}

	actionIDHex := "dd01020304050607080910111213141516171819202122232425262728293031"
	body := []byte("stub body")
	tmp, err := dc.TempFile()
	if err != nil {
		b.Fatal(err)
	}
	tmp.Write(body)
	tmp.Close()

	meta := &cache.Metadata{
		OutputIDHex:   "output_" + actionIDHex,
		Size:          int64(len(body)),
		Time:          time.Now(),
		CASDigestHash: "hash_test",
		CASDigestSize: int64(len(body)),
	}
	bodyPath, err := dc.Install(actionIDHex, tmp.Name(), meta)
	if err != nil {
		b.Fatal(err)
	}
	// Remove body to make it a stub
	os.Remove(bodyPath)

	b.ResetTimer()
	for range b.N {
		_, isStub := dc.HasMetadataStub(actionIDHex)
		if !isStub {
			b.Fatal("expected stub")
		}
	}
}

func byteSizeLabel(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%dMiB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%dKiB", n/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
