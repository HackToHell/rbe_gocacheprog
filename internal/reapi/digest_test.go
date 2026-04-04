package reapi_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hacktohell/gocache-rbe/internal/reapi"
)

func TestDigestBytes(t *testing.T) {
	data := []byte("hello world")
	d := reapi.DigestBytes(data)

	h := sha256.Sum256(data)
	wantHash := fmt.Sprintf("%x", h)
	if d.Hash != wantHash {
		t.Errorf("hash = %s, want %s", d.Hash, wantHash)
	}
	if d.Size != 11 {
		t.Errorf("size = %d, want 11", d.Size)
	}
}

func TestDigestBytesEmpty(t *testing.T) {
	d := reapi.DigestBytes([]byte{})
	// SHA-256 of empty is e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if d.Hash != want {
		t.Errorf("empty hash = %s, want %s", d.Hash, want)
	}
	if d.Size != 0 {
		t.Errorf("empty size = %d, want 0", d.Size)
	}
}

func TestDigestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	data := []byte("file content for digest")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	d, err := reapi.DigestFile(path)
	if err != nil {
		t.Fatal(err)
	}

	want := reapi.DigestBytes(data)
	if d.Hash != want.Hash || d.Size != want.Size {
		t.Errorf("DigestFile = %v, want %v", d, want)
	}
}

func TestDigestToProto(t *testing.T) {
	d := reapi.Digest{Hash: "abc123", Size: 42}
	p := d.ToProto()
	if p.GetHash() != "abc123" || p.GetSizeBytes() != 42 {
		t.Errorf("ToProto = %v", p)
	}
}

func TestHexRoundTrip(t *testing.T) {
	data := []byte{0x00, 0x01, 0xab, 0xff}
	hex := reapi.HexEncode(data)
	if hex != "0001abff" {
		t.Errorf("HexEncode = %q", hex)
	}
	decoded, err := reapi.HexDecode(hex)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(data) {
		t.Errorf("HexDecode mismatch")
	}
}
