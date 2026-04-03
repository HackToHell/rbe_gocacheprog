// Package reapi implements REAPI v2 client operations and proto construction.
package reapi

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
)

const hexDigits = "0123456789abcdef"

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

// DigestFromProto converts from the protobuf Digest type.
func DigestFromProto(pd *repb.Digest) Digest {
	return Digest{Hash: pd.GetHash(), Size: pd.GetSizeBytes()}
}

// hexEncodeFixed encodes src into a hex string with a single allocation
// by writing directly into a string-sized byte slice.
func hexEncodeFixed(src []byte) string {
	dst := make([]byte, len(src)*2)
	for i, v := range src {
		dst[i*2] = hexDigits[v>>4]
		dst[i*2+1] = hexDigits[v&0x0f]
	}
	return string(dst)
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

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return Digest{}, err
	}
	return Digest{
		Hash: hexEncodeFixed(h.Sum(nil)),
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
