package handler_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/handler"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
)

func TestGetRemoteACHit(t *testing.T) {
	env := setupEnv(t)

	actionID := makeActionID(0x10)
	outputID := makeOutputID(0xEE)
	actionIDHex := reapi.HexEncode(actionID)
	outputIDHex := reapi.HexEncode(outputID)
	bodyData := []byte("remote cached content")
	bodyDigest := reapi.DigestBytes(bodyData)

	// Pre-seed CAS with body
	env.srv.PutBlob(bodyDigest.Hash, bodyData)

	// Pre-seed AC with synthetic action result
	sd, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		t.Fatal(err)
	}
	ar := reapi.SyntheticActionResult(outputIDHex, bodyDigest)
	env.srv.PutActionResult(sd.ActionDigest.Hash, ar)

	// GET should find it via remote AC
	getReq := &protocol.Request{
		ID:       10,
		Command:  "get",
		ActionID: actionID,
	}
	resp := env.h.Handle(context.Background(), getReq)
	if resp.Miss {
		t.Fatal("expected hit from remote AC")
	}
	if resp.DiskPath == "" {
		t.Fatal("expected DiskPath")
	}
	if resp.Size != int64(len(bodyData)) {
		t.Errorf("size = %d, want %d", resp.Size, len(bodyData))
	}

	// Verify the body was downloaded correctly
	data, err := os.ReadFile(resp.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(bodyData) {
		t.Errorf("body = %q", data)
	}
}

func TestGetMetadataStubRefill(t *testing.T) {
	env := setupEnv(t)

	actionID := makeActionID(0x11)
	outputID := makeOutputID(0xFF)
	actionIDHex := reapi.HexEncode(actionID)
	bodyData := []byte("refill from stub")
	bodyDigest := reapi.DigestBytes(bodyData)

	// Pre-seed CAS
	env.srv.PutBlob(bodyDigest.Hash, bodyData)

	// Create metadata stub (no body file)
	metaPath := cache.MetadataPath(env.cache.Dir(), actionIDHex)
	os.MkdirAll(metaPath[:len(metaPath)-len(actionIDHex)-2-1], 0o700) // just the dir part, let WriteMetadata handle it
	cache.WriteMetadata(metaPath, &cache.Metadata{
		OutputIDHex:   reapi.HexEncode(outputID),
		Size:          int64(len(bodyData)),
		Time:          time.Now(),
		CASDigestHash: bodyDigest.Hash,
		CASDigestSize: bodyDigest.Size,
	})

	getReq := &protocol.Request{
		ID:       11,
		Command:  "get",
		ActionID: actionID,
	}
	resp := env.h.Handle(context.Background(), getReq)
	if resp.Miss {
		t.Fatal("expected hit from CAS refill")
	}
	if resp.DiskPath == "" {
		t.Fatal("expected DiskPath")
	}

	data, err := os.ReadFile(resp.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(bodyData) {
		t.Errorf("body = %q", data)
	}
}

func TestGetCorruptLocalEntryFallsThrough(t *testing.T) {
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	h := &handler.Handler{Cache: dc, Client: nil}

	actionID := makeActionID(0x12)
	actionIDHex := reapi.HexEncode(actionID)

	// Write corrupt metadata
	metaPath := cache.MetadataPath(dc.Dir(), actionIDHex)
	os.MkdirAll(metaPath[:len(metaPath)-len(actionIDHex)-2-1], 0o700)
	os.WriteFile(metaPath, []byte("{invalid"), 0o600)

	// Should be a miss
	resp := h.Handle(context.Background(), &protocol.Request{
		ID:       12,
		Command:  "get",
		ActionID: actionID,
	})
	if !resp.Miss {
		t.Error("expected miss with corrupt metadata")
	}
}

func TestGetACHitMissingCASBlob(t *testing.T) {
	env := setupEnv(t)

	actionID := makeActionID(0x13)
	outputID := makeOutputID(0xAB)
	actionIDHex := reapi.HexEncode(actionID)
	outputIDHex := reapi.HexEncode(outputID)

	// Seed AC but NOT CAS - simulating GC'd CAS blob
	sd, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		t.Fatal(err)
	}
	ar := reapi.SyntheticActionResult(outputIDHex, reapi.Digest{Hash: "nonexistent", Size: 10})
	env.srv.PutActionResult(sd.ActionDigest.Hash, ar)

	resp := env.h.Handle(context.Background(), &protocol.Request{
		ID:       13,
		Command:  "get",
		ActionID: actionID,
	})
	if !resp.Miss {
		t.Error("expected miss when CAS blob is missing")
	}
}

func TestGetNoRemote(t *testing.T) {
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	h := &handler.Handler{Cache: dc, Client: nil}

	resp := h.Handle(context.Background(), &protocol.Request{
		ID:       14,
		Command:  "get",
		ActionID: makeActionID(0x14),
	})
	if !resp.Miss {
		t.Error("expected miss without remote")
	}
}
