package fakereapi_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"testing"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/hacktohell/gocache-rbe/testutil/fakereapi"
	"google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func setup(t *testing.T) (*fakereapi.Server, *grpc.ClientConn) {
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

	return srv, conn
}

func TestGetCapabilities(t *testing.T) {
	_, conn := setup(t)
	client := repb.NewCapabilitiesClient(conn)

	resp, err := client.GetCapabilities(context.Background(), &repb.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	cc := resp.GetCacheCapabilities()
	if cc == nil {
		t.Fatal("expected cache capabilities")
	}

	if !cc.GetActionCacheUpdateCapabilities().GetUpdateEnabled() {
		t.Error("expected update_enabled=true")
	}

	found := false
	for _, df := range cc.GetDigestFunctions() {
		if df == repb.DigestFunction_SHA256 {
			found = true
		}
	}
	if !found {
		t.Error("expected SHA256 in digest functions")
	}
}

func TestCASBatchRoundTrip(t *testing.T) {
	_, conn := setup(t)
	client := repb.NewContentAddressableStorageClient(conn)

	data := []byte("hello world")
	h := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", h)
	digest := &repb.Digest{Hash: hash, SizeBytes: int64(len(data))}

	// FindMissingBlobs - should be missing
	fmResp, err := client.FindMissingBlobs(context.Background(), &repb.FindMissingBlobsRequest{
		BlobDigests: []*repb.Digest{digest},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fmResp.GetMissingBlobDigests()) != 1 {
		t.Fatalf("expected 1 missing blob, got %d", len(fmResp.GetMissingBlobDigests()))
	}

	// BatchUpdateBlobs
	_, err = client.BatchUpdateBlobs(context.Background(), &repb.BatchUpdateBlobsRequest{
		Requests: []*repb.BatchUpdateBlobsRequest_Request{
			{Digest: digest, Data: data},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// FindMissingBlobs - should exist now
	fmResp, err = client.FindMissingBlobs(context.Background(), &repb.FindMissingBlobsRequest{
		BlobDigests: []*repb.Digest{digest},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fmResp.GetMissingBlobDigests()) != 0 {
		t.Fatalf("expected 0 missing blobs, got %d", len(fmResp.GetMissingBlobDigests()))
	}

	// BatchReadBlobs
	brResp, err := client.BatchReadBlobs(context.Background(), &repb.BatchReadBlobsRequest{
		Digests: []*repb.Digest{digest},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(brResp.GetResponses()) != 1 {
		t.Fatalf("expected 1 response, got %d", len(brResp.GetResponses()))
	}
	if string(brResp.GetResponses()[0].GetData()) != "hello world" {
		t.Errorf("data mismatch: %q", brResp.GetResponses()[0].GetData())
	}
}

func TestACRoundTrip(t *testing.T) {
	_, conn := setup(t)
	acClient := repb.NewActionCacheClient(conn)

	actionDigest := &repb.Digest{Hash: "abc123", SizeBytes: 10}

	// GetActionResult - should be NOT_FOUND
	_, err := acClient.GetActionResult(context.Background(), &repb.GetActionResultRequest{
		ActionDigest: actionDigest,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", status.Code(err))
	}

	// UpdateActionResult
	ar := &repb.ActionResult{
		OutputFiles: []*repb.OutputFile{
			{Path: "out", Digest: &repb.Digest{Hash: "deadbeef", SizeBytes: 5}},
		},
	}
	_, err = acClient.UpdateActionResult(context.Background(), &repb.UpdateActionResultRequest{
		ActionDigest: actionDigest,
		ActionResult: ar,
	})
	if err != nil {
		t.Fatal(err)
	}

	// GetActionResult - should succeed
	got, err := acClient.GetActionResult(context.Background(), &repb.GetActionResultRequest{
		ActionDigest: actionDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.GetOutputFiles()) != 1 || got.GetOutputFiles()[0].GetPath() != "out" {
		t.Errorf("unexpected action result: %v", got)
	}
}

func TestByteStreamRoundTrip(t *testing.T) {
	_, conn := setup(t)
	bsClient := bytestream.NewByteStreamClient(conn)

	data := []byte("streaming test data that is larger than usual")
	h := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", h)

	// Write
	wStream, err := bsClient.Write(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = wStream.Send(&bytestream.WriteRequest{
		ResourceName: fmt.Sprintf("test/uploads/uuid123/blobs/%s/%d", hash, len(data)),
		Data:         data,
		FinishWrite:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wResp, err := wStream.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}
	if wResp.GetCommittedSize() != int64(len(data)) {
		t.Errorf("committed size: got %d, want %d", wResp.GetCommittedSize(), len(data))
	}

	// Read
	rStream, err := bsClient.Read(context.Background(), &bytestream.ReadRequest{
		ResourceName: fmt.Sprintf("test/blobs/%s/%d", hash, len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	var result []byte
	for {
		resp, err := rStream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, resp.GetData()...)
	}
	if string(result) != string(data) {
		t.Errorf("data mismatch")
	}
}
