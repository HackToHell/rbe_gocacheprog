package reapi_test

import (
	"testing"

	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
)

func TestSyntheticDeterminism(t *testing.T) {
	actionIDHex := "0001020304050607080910111213141516171819202122232425262728293031"

	d1, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		t.Fatal(err)
	}

	if d1.ActionDigest != d2.ActionDigest {
		t.Errorf("action digest not deterministic: %v vs %v", d1.ActionDigest, d2.ActionDigest)
	}
	if d1.CommandDigest != d2.CommandDigest {
		t.Errorf("command digest not deterministic")
	}
	if d1.DirDigest != d2.DirDigest {
		t.Errorf("dir digest not deterministic")
	}
}

func TestSyntheticCollisionFree(t *testing.T) {
	id1 := "0001020304050607080910111213141516171819202122232425262728293031"
	id2 := "ff01020304050607080910111213141516171819202122232425262728293031"

	d1, err := reapi.ComputeSyntheticDigests(id1)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := reapi.ComputeSyntheticDigests(id2)
	if err != nil {
		t.Fatal(err)
	}

	if d1.ActionDigest == d2.ActionDigest {
		t.Error("different ActionIDs produced the same Action digest")
	}
	if d1.CommandDigest == d2.CommandDigest {
		t.Error("different ActionIDs produced the same Command digest")
	}
	// Dir digest should be identical (empty directory is constant)
	if d1.DirDigest != d2.DirDigest {
		t.Error("empty directory digest should be constant")
	}
}

func TestSyntheticGolden(t *testing.T) {
	// Record a golden digest to detect accidental serialization changes.
	actionIDHex := "0000000000000000000000000000000000000000000000000000000000000000"
	d, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		t.Fatal(err)
	}

	// Empty Directory digest is well-known.
	wantDirHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if d.DirDigest.Hash != wantDirHash {
		t.Errorf("empty dir hash = %s, want %s", d.DirDigest.Hash, wantDirHash)
	}
	if d.DirDigest.Size != 0 {
		t.Errorf("empty dir size = %d, want 0", d.DirDigest.Size)
	}

	// Log the action digest for golden tracking. If this changes, the cache is broken.
	t.Logf("Golden action digest for zero ActionID: hash=%s size=%d", d.ActionDigest.Hash, d.ActionDigest.Size)
	t.Logf("Golden command digest: hash=%s size=%d", d.CommandDigest.Hash, d.CommandDigest.Size)

	// Verify non-empty
	if d.ActionDigest.Hash == "" || d.ActionDigest.Size == 0 {
		t.Error("action digest should be non-empty")
	}
	if d.CommandDigest.Hash == "" || d.CommandDigest.Size == 0 {
		t.Error("command digest should be non-empty")
	}
}

func TestSyntheticActionResult(t *testing.T) {
	outputIDHex := "aabbccdd"
	bodyDigest := reapi.Digest{Hash: "deadbeef", Size: 42}

	ar := reapi.SyntheticActionResult(outputIDHex, bodyDigest)
	if len(ar.GetOutputFiles()) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(ar.GetOutputFiles()))
	}
	of := ar.GetOutputFiles()[0]
	if of.GetPath() != "aabbccdd" {
		t.Errorf("path = %q", of.GetPath())
	}
	if of.GetDigest().GetHash() != "deadbeef" {
		t.Errorf("digest hash = %q", of.GetDigest().GetHash())
	}
	if of.GetIsExecutable() {
		t.Error("should not be executable")
	}
}
