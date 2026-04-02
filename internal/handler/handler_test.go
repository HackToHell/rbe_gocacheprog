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
	"github.com/hacktohell/rbe_gocacheprog/testutil/fakereapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type testEnv struct {
	srv    *fakereapi.Server
	client *reapi.Client
	cache  *cache.DiskCache
	h      *handler.Handler
}

func setupEnv(t *testing.T) *testEnv {
	t.Helper()

	srv, err := fakereapi.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	client := reapi.NewClientFromConn(conn, "", 10*time.Second)
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	return &testEnv{
		srv:    srv,
		client: client,
		cache:  dc,
		h:      &handler.Handler{Cache: dc, Client: client},
	}
}

func makeActionID(b byte) []byte {
	id := make([]byte, 32)
	id[0] = b
	return id
}

func makeOutputID(b byte) []byte {
	id := make([]byte, 32)
	id[0] = b
	return id
}

func TestPutLocalOnly(t *testing.T) {
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	h := &handler.Handler{Cache: dc, Client: nil}

	req := &protocol.Request{
		ID:       1,
		Command:  "put",
		ActionID: makeActionID(0x01),
		OutputID: makeOutputID(0xAA),
		Body:     []byte("local only body"),
		BodySize: 15,
	}

	resp := h.Handle(context.Background(), req)
	if resp.Err != "" {
		t.Fatalf("unexpected error: %s", resp.Err)
	}
	if resp.DiskPath == "" {
		t.Fatal("expected DiskPath")
	}

	// Verify file on disk
	data, err := os.ReadFile(resp.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "local only body" {
		t.Errorf("body = %q", data)
	}
}

func TestPutWithRemote(t *testing.T) {
	env := setupEnv(t)

	req := &protocol.Request{
		ID:       2,
		Command:  "put",
		ActionID: makeActionID(0x02),
		OutputID: makeOutputID(0xBB),
		Body:     []byte("remote body"),
		BodySize: 11,
	}

	resp := env.h.Handle(context.Background(), req)
	if resp.Err != "" {
		t.Fatalf("unexpected error: %s", resp.Err)
	}
	if resp.DiskPath == "" {
		t.Fatal("expected DiskPath")
	}

	// Wait for async remote populate
	time.Sleep(500 * time.Millisecond)

	// Verify body exists in CAS
	bodyDigest := reapi.DigestBytes([]byte("remote body"))
	got, ok := env.srv.GetBlob(bodyDigest.Hash)
	if !ok {
		t.Fatal("body not in CAS")
	}
	if string(got) != "remote body" {
		t.Errorf("CAS body = %q", got)
	}
}

func TestPutReturnsDiskPathEvenIfRemoteFails(t *testing.T) {
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	// Client is nil - simulates remote being down
	h := &handler.Handler{Cache: dc, Client: nil}

	req := &protocol.Request{
		ID:       3,
		Command:  "put",
		ActionID: makeActionID(0x03),
		OutputID: makeOutputID(0xCC),
		Body:     []byte("resilient body"),
		BodySize: 14,
	}

	resp := h.Handle(context.Background(), req)
	if resp.Err != "" {
		t.Fatalf("unexpected error: %s", resp.Err)
	}
	if resp.DiskPath == "" {
		t.Fatal("expected DiskPath even without remote")
	}
}

func TestGetMiss(t *testing.T) {
	env := setupEnv(t)

	req := &protocol.Request{
		ID:       4,
		Command:  "get",
		ActionID: makeActionID(0x04),
	}

	resp := env.h.Handle(context.Background(), req)
	if !resp.Miss {
		t.Error("expected miss")
	}
}

func TestPutThenGetLocalHit(t *testing.T) {
	env := setupEnv(t)

	actionID := makeActionID(0x05)
	outputID := makeOutputID(0xDD)

	putReq := &protocol.Request{
		ID:       5,
		Command:  "put",
		ActionID: actionID,
		OutputID: outputID,
		Body:     []byte("round trip data"),
		BodySize: 15,
	}
	putResp := env.h.Handle(context.Background(), putReq)
	if putResp.Err != "" {
		t.Fatalf("put error: %s", putResp.Err)
	}

	getReq := &protocol.Request{
		ID:       6,
		Command:  "get",
		ActionID: actionID,
	}
	getResp := env.h.Handle(context.Background(), getReq)
	if getResp.Miss {
		t.Error("expected hit")
	}
	if getResp.DiskPath == "" {
		t.Error("expected DiskPath")
	}
	if getResp.Size != 15 {
		t.Errorf("size = %d", getResp.Size)
	}

	data, err := os.ReadFile(getResp.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "round trip data" {
		t.Errorf("body = %q", data)
	}
}

func TestClose(t *testing.T) {
	env := setupEnv(t)

	resp := env.h.Handle(context.Background(), &protocol.Request{
		ID:      7,
		Command: "close",
	})
	if resp.Err != "" {
		t.Fatalf("close error: %s", resp.Err)
	}
	if resp.ID != 7 {
		t.Errorf("ID = %d", resp.ID)
	}
}

func TestUnknownCommand(t *testing.T) {
	env := setupEnv(t)

	resp := env.h.Handle(context.Background(), &protocol.Request{
		ID:      8,
		Command: "unknown",
	})
	if resp.Err == "" {
		t.Error("expected error for unknown command")
	}
}
