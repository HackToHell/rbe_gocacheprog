package reapi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
)

// --- HexEncode / HexDecode ---

func BenchmarkHexEncode32(b *testing.B) {
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(i)
	}
	b.SetBytes(32)
	b.ResetTimer()
	for range b.N {
		_ = reapi.HexEncode(data)
	}
}

func BenchmarkHexDecode64(b *testing.B) {
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(i)
	}
	hex := reapi.HexEncode(data)
	b.SetBytes(64)
	b.ResetTimer()
	for range b.N {
		_, err := reapi.HexDecode(hex)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- DigestBytes ---

func BenchmarkDigestBytes(b *testing.B) {
	for _, size := range []int{64, 1024, 64 * 1024, 1024 * 1024} {
		b.Run(byteSizeLabel(size), func(b *testing.B) {
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i % 256)
			}
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				_ = reapi.DigestBytes(data)
			}
		})
	}
}

// --- DigestFile ---

func BenchmarkDigestFile(b *testing.B) {
	for _, size := range []int{1024, 64 * 1024, 1024 * 1024} {
		b.Run(byteSizeLabel(size), func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "bench.bin")
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i % 256)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(size))
			b.ResetTimer()
			for range b.N {
				_, err := reapi.DigestFile(path)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- ComputeSyntheticDigests (3x proto.Marshal + 3x SHA-256) ---

func BenchmarkComputeSyntheticDigests(b *testing.B) {
	actionIDHex := "0001020304050607080910111213141516171819202122232425262728293031"
	b.ResetTimer()
	for range b.N {
		_, err := reapi.ComputeSyntheticDigests(actionIDHex)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- MarshalDeterministic ---

func BenchmarkMarshalDeterministic(b *testing.B) {
	actionIDHex := "0001020304050607080910111213141516171819202122232425262728293031"
	cmd := reapi.SyntheticCommand(actionIDHex)
	b.ResetTimer()
	for range b.N {
		_, _, err := reapi.MarshalDeterministic(cmd)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- Digest.ToProto ---

func BenchmarkDigestToProto(b *testing.B) {
	d := reapi.Digest{Hash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Size: 42}
	b.ResetTimer()
	for range b.N {
		_ = d.ToProto()
	}
}

// --- CircuitBreaker.Allow (closed path) ---

func BenchmarkCircuitBreakerAllowClosed(b *testing.B) {
	cb := reapi.NewCircuitBreaker(10, 30*time.Second)
	b.ResetTimer()
	for range b.N {
		cb.Allow()
	}
}

// --- CircuitBreaker.Allow (open path, probing) ---

func BenchmarkCircuitBreakerAllowOpen(b *testing.B) {
	cb := reapi.NewCircuitBreaker(1, 1*time.Nanosecond)
	cb.RecordFailure() // trip it
	b.ResetTimer()
	for range b.N {
		cb.Allow()
	}
}

// --- CircuitBreaker contention: parallel Allow calls ---

func BenchmarkCircuitBreakerAllowParallel(b *testing.B) {
	cb := reapi.NewCircuitBreaker(10, 30*time.Second)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cb.Allow()
		}
	})
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
