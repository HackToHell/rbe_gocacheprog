// Package reapi implements REAPI v2 client operations and proto construction.
package reapi

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"sync"
	"unsafe"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
)

const hexDigits = "0123456789abcdef"

// SHA256Pool reuses sha256 hash state to avoid per-call allocation.
var SHA256Pool = sync.Pool{
	New: func() any { return sha256.New() },
}

// copyBufPool reuses 32 KiB buffers for io.CopyBuffer to avoid per-call allocation.
var copyBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

// Digest wraps an REAPI digest with hash and size.
type Digest struct {
	Hash string
	Size int64
}

// ToProto converts to the protobuf Digest type.
func (d Digest) ToProto() *repb.Digest {
	return &repb.Digest{
		Hash:      d.Hash,
		SizeBytes: d.Size,
	}
}

// FillProto fills an existing protobuf Digest, avoiding allocation.
func (d Digest) FillProto(pd *repb.Digest) {
	pd.Hash = d.Hash
	pd.SizeBytes = d.Size
}

// DigestFromProto converts from the protobuf Digest type.
func DigestFromProto(pd *repb.Digest) Digest {
	return Digest{Hash: pd.GetHash(), Size: pd.GetSizeBytes()}
}

// hexEncodeFixed encodes src into a hex string with zero copy.
// The returned string directly backs the allocated buffer.
func hexEncodeFixed(src []byte) string {
	dst := make([]byte, len(src)*2)
	for i, v := range src {
		dst[i*2] = hexDigits[v>>4]
		dst[i*2+1] = hexDigits[v&0x0f]
	}
	return unsafe.String(unsafe.SliceData(dst), len(dst))
}

// DigestBytes computes the SHA-256 digest of a byte slice.
func DigestBytes(data []byte) Digest {
	h := sha256.Sum256(data)
	return Digest{
		Hash: hexEncodeFixed(h[:]),
		Size: int64(len(data)),
	}
}

// DigestFile computes the SHA-256 digest of a file.
func DigestFile(path string) (Digest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Digest{}, err
	}
	defer f.Close()

	h := SHA256Pool.Get().(hash.Hash)
	defer func() { h.Reset(); SHA256Pool.Put(h) }()

	bufp := copyBufPool.Get().(*[]byte)
	defer copyBufPool.Put(bufp)
	size, err := io.CopyBuffer(h, f, *bufp)
	if err != nil {
		return Digest{}, err
	}
	var buf [sha256.Size]byte
	return Digest{
		Hash: hexEncodeFixed(h.Sum(buf[:0])),
		Size: size,
	}, nil
}

// HexEncode encodes raw bytes to lowercase hex string.
func HexEncode(b []byte) string {
	return hexEncodeFixed(b)
}

// HexDecode decodes a hex string to raw bytes.
func HexDecode(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
