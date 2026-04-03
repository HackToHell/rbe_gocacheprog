package reapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
	"github.com/hacktohell/rbe_gocacheprog/testutil/fakereapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupClient(t *testing.T) (*fakereapi.Server, *reapi.Client) {
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
	return srv, client
}

func blobDigest(data []byte) reapi.Digest {
	h := sha256.Sum256(data)
	return reapi.Digest{Hash: fmt.Sprintf("%x", h), Size: int64(len(data))}
}

func TestFindMissingBlobs(t *testing.T) {
	_, client := setupClient(t)

	data := []byte("test blob")
	d := blobDigest(data)

	missing, err := client.FindMissingBlobs(context.Background(), []reapi.Digest{d})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing, got %d", len(missing))
	}
}

func TestUploadBlobSmall(t *testing.T) {
	srv, client := setupClient(t)

	data := []byte("small blob data")
	d := blobDigest(data)

	if err := client.UploadBlob(context.Background(), d, data); err != nil {
		t.Fatal(err)
	}

	// Verify exists in server
	got, ok := srv.GetBlob(d.Hash)
	if !ok {
		t.Fatal("blob not found in server")
	}
	if string(got) != string(data) {
		t.Errorf("data mismatch")
	}

	// Upload again - should be a no-op (already exists)
	if err := client.UploadBlob(context.Background(), d, data); err != nil {
		t.Fatal(err)
	}
}

func TestUploadBlobLargeByteStream(t *testing.T) {
	srv, client := setupClient(t)

	// Create a blob larger than max_batch_size (4 MiB in fakereapi)
	data := bytes.Repeat([]byte("x"), 5*1024*1024)
	d := blobDigest(data)

	if err := client.UploadBlob(context.Background(), d, data); err != nil {
		t.Fatal(err)
	}

	got, ok := srv.GetBlob(d.Hash)
	if !ok {
		t.Fatal("blob not found")
	}
	if len(got) != len(data) {
		t.Errorf("size mismatch: got %d, want %d", len(got), len(data))
	}
}

func TestDownloadBlobSmall(t *testing.T) {
	srv, client := setupClient(t)

	data := []byte("download me")
	d := blobDigest(data)
	srv.PutBlob(d.Hash, data)

	var buf bytes.Buffer
	if err := client.DownloadBlob(context.Background(), d, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "download me" {
		t.Errorf("got %q", buf.String())
	}
}

func TestDownloadBlobLargeByteStream(t *testing.T) {
	srv, client := setupClient(t)

	data := bytes.Repeat([]byte("y"), 5*1024*1024)
	d := blobDigest(data)
	srv.PutBlob(d.Hash, data)

	var buf bytes.Buffer
	if err := client.DownloadBlob(context.Background(), d, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != len(data) {
		t.Errorf("size mismatch: got %d, want %d", buf.Len(), len(data))
	}
}

func TestACRoundTripClient(t *testing.T) {
	_, client := setupClient(t)

	sd, err := reapi.ComputeSyntheticDigests("deadbeef")
	if err != nil {
		t.Fatal(err)
	}

	// Miss
	ar, err := client.GetActionResult(context.Background(), sd.ActionDigest)
	if err != nil {
		t.Fatal(err)
	}
	if ar != nil {
		t.Error("expected nil on miss")
	}

	// Store
	result := reapi.SyntheticActionResult("outputhex", reapi.Digest{Hash: "blobhash", Size: 99})
	if err := client.UpdateActionResult(context.Background(), sd.ActionDigest, result); err != nil {
		t.Fatal(err)
	}

	// Hit
	ar, err = client.GetActionResult(context.Background(), sd.ActionDigest)
	if err != nil {
		t.Fatal(err)
	}
	if ar == nil {
		t.Fatal("expected hit")
	}
	if len(ar.GetOutputFiles()) != 1 {
		t.Fatalf("expected 1 output file, got %d", len(ar.GetOutputFiles()))
	}
	if ar.GetOutputFiles()[0].GetPath() != "outputhex" {
		t.Errorf("path = %q", ar.GetOutputFiles()[0].GetPath())
	}
}

func TestACUpdateDisabled(t *testing.T) {
	srv, client := setupClient(t)
	srv.SetUpdateEnabled(false)

	result := reapi.SyntheticActionResult("out", reapi.Digest{Hash: "h", Size: 1})
	err := client.UpdateActionResult(context.Background(), reapi.Digest{Hash: "ah", Size: 10}, result)
	if err == nil {
		t.Error("expected error when updates disabled")
	}
	if !strings.Contains(err.Error(), "PermissionDenied") && !strings.Contains(err.Error(), "AC updates disabled") {
		t.Errorf("unexpected error: %v", err)
	}
}
