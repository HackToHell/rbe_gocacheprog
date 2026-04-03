package reapi_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
	"github.com/hacktohell/rbe_gocacheprog/testutil/fakereapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func setupRetryClient(t *testing.T) (*fakereapi.Server, *reapi.Client) {
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

func TestRetryRPCTransientErrorThenSuccess(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("retry test blob")
	d := blobDigest(data)

	// Pre-seed the blob so FindMissingBlobs returns it as missing on first call
	// but BatchUpdateBlobs fails transiently then succeeds.
	var attempts atomic.Int32
	srv.OnBatchUpdateBlobs = func(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
		n := attempts.Add(1)
		if n <= 2 {
			return nil, status.Error(codes.Unavailable, "transient error")
		}
		// Let it through to default handler
		srv.OnBatchUpdateBlobs = nil
		return nil, status.Error(codes.Unimplemented, "fallthrough")
	}

	// The retry logic should handle the transient errors and succeed on the 3rd attempt.
	// However, since we nil the hook after returning the non-retryable Unimplemented
	// on the 3rd attempt via hook, we need a different approach.
	// Let's instead make the hook return success directly on the 3rd call.
	attempts.Store(0)
	srv.OnBatchUpdateBlobs = func(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
		n := attempts.Add(1)
		if n <= 2 {
			return nil, status.Error(codes.Unavailable, "transient error")
		}
		// Return success
		var responses []*repb.BatchUpdateBlobsResponse_Response
		for _, r := range req.GetRequests() {
			srv.PutBlob(r.GetDigest().GetHash(), r.GetData())
			responses = append(responses, &repb.BatchUpdateBlobsResponse_Response{
				Digest: r.GetDigest(),
				Status: status.New(codes.OK, "").Proto(),
			})
		}
		return &repb.BatchUpdateBlobsResponse{Responses: responses}, nil
	}

	if err := client.UploadBlob(context.Background(), d, data); err != nil {
		t.Fatalf("UploadBlob should succeed after retries: %v", err)
	}

	got, ok := srv.GetBlob(d.Hash)
	if !ok {
		t.Fatal("blob not in CAS after retry")
	}
	if !bytes.Equal(got, data) {
		t.Error("data mismatch")
	}

	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestRetryRPCExhaustsAllAttempts(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("always fail blob")
	d := blobDigest(data)

	var attempts atomic.Int32
	srv.OnBatchUpdateBlobs = func(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
		attempts.Add(1)
		return nil, status.Error(codes.Unavailable, "persistent transient error")
	}

	err := client.UploadBlob(context.Background(), d, data)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "Unavailable") && !strings.Contains(err.Error(), "persistent transient") {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have been called 3 times (maxAttempts=3)
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestRetryRPCNonTransientErrorNoRetry(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("non-transient blob")
	d := blobDigest(data)

	var attempts atomic.Int32
	srv.OnBatchUpdateBlobs = func(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
		attempts.Add(1)
		return nil, status.Error(codes.InvalidArgument, "bad request")
	}

	err := client.UploadBlob(context.Background(), d, data)
	if err == nil {
		t.Fatal("expected error")
	}

	// Non-transient errors should not be retried
	if attempts.Load() != 1 {
		t.Errorf("expected 1 attempt (no retry), got %d", attempts.Load())
	}
}

func TestRetryRPCWithResourceExhausted(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("resource exhausted blob")
	d := blobDigest(data)

	var attempts atomic.Int32
	srv.OnBatchUpdateBlobs = func(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
		n := attempts.Add(1)
		if n == 1 {
			return nil, status.Error(codes.ResourceExhausted, "rate limited")
		}
		// Success on second attempt
		var responses []*repb.BatchUpdateBlobsResponse_Response
		for _, r := range req.GetRequests() {
			srv.PutBlob(r.GetDigest().GetHash(), r.GetData())
			responses = append(responses, &repb.BatchUpdateBlobsResponse_Response{
				Digest: r.GetDigest(),
				Status: status.New(codes.OK, "").Proto(),
			})
		}
		return &repb.BatchUpdateBlobsResponse{Responses: responses}, nil
	}

	if err := client.UploadBlob(context.Background(), d, data); err != nil {
		t.Fatalf("should succeed after ResourceExhausted retry: %v", err)
	}

	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestRetryRPCFindMissingBlobsTransient(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("find missing retry")
	d := blobDigest(data)

	var attempts atomic.Int32
	srv.OnFindMissingBlobs = func(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
		n := attempts.Add(1)
		if n == 1 {
			return nil, status.Error(codes.Unavailable, "server restarting")
		}
		// Return all as missing on second try
		return &repb.FindMissingBlobsResponse{MissingBlobDigests: req.GetBlobDigests()}, nil
	}

	missing, err := client.FindMissingBlobs(context.Background(), []reapi.Digest{d})
	if err != nil {
		t.Fatalf("FindMissingBlobs should retry: %v", err)
	}
	if len(missing) != 1 {
		t.Errorf("expected 1 missing, got %d", len(missing))
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestClientCircuitBreakerIntegration(t *testing.T) {
	_, client := setupRetryClient(t)

	// CheckCircuit should pass initially
	if err := client.CheckCircuit(); err != nil {
		t.Fatalf("circuit should be closed initially: %v", err)
	}

	// Record success - should be a no-op
	client.RecordSuccess()
	if err := client.CheckCircuit(); err != nil {
		t.Fatalf("circuit should still be closed: %v", err)
	}

	// Record 5 failures (threshold) to trip
	for range 5 {
		client.RecordFailure()
	}

	err := client.CheckCircuit()
	if err == nil {
		t.Fatal("circuit should be open after 5 failures")
	}
	if !strings.Contains(err.Error(), "circuit breaker open") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUploadFileSmall(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("file upload test content")
	d := blobDigest(data)

	// Write to a temp file
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := client.UploadFile(context.Background(), path, d); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}

	got, ok := srv.GetBlob(d.Hash)
	if !ok {
		t.Fatal("blob not in CAS after UploadFile")
	}
	if !bytes.Equal(got, data) {
		t.Error("data mismatch")
	}
}

func TestUploadFileLargeByteStream(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := bytes.Repeat([]byte("z"), 5*1024*1024)
	d := blobDigest(data)

	dir := t.TempDir()
	path := filepath.Join(dir, "largefile")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := client.UploadFile(context.Background(), path, d); err != nil {
		t.Fatalf("UploadFile large: %v", err)
	}

	got, ok := srv.GetBlob(d.Hash)
	if !ok {
		t.Fatal("large blob not in CAS")
	}
	if len(got) != len(data) {
		t.Errorf("size mismatch: got %d, want %d", len(got), len(data))
	}
}

func TestUploadFileAlreadyExists(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("pre-existing file content")
	d := blobDigest(data)

	// Pre-seed CAS
	srv.PutBlob(d.Hash, data)

	dir := t.TempDir()
	path := filepath.Join(dir, "existing")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	// Should be a no-op (already exists)
	if err := client.UploadFile(context.Background(), path, d); err != nil {
		t.Fatalf("UploadFile should succeed for existing blob: %v", err)
	}
}

func TestUploadFileNotFound(t *testing.T) {
	_, client := setupRetryClient(t)

	d := reapi.Digest{Hash: "deadbeef", Size: 10}
	err := client.UploadFile(context.Background(), "/nonexistent/path/to/file", d)
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestRetryRPCCancelledContextNoRetry(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("cancelled context blob")
	d := blobDigest(data)

	var attempts atomic.Int32
	srv.OnFindMissingBlobs = func(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
		attempts.Add(1)
		return nil, status.Error(codes.Unavailable, "transient")
	}

	// Cancel the context before making the call
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.FindMissingBlobs(ctx, []reapi.Digest{d})
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}

	// With a cancelled context, should bail out quickly - at most 1 attempt
	if attempts.Load() > 1 {
		t.Errorf("expected at most 1 attempt with cancelled context, got %d", attempts.Load())
	}
}

func TestRetryRPCContextCancelledMidRetry(t *testing.T) {
	srv, client := setupRetryClient(t)

	data := []byte("mid-cancel blob")
	d := blobDigest(data)

	ctx, cancel := context.WithCancel(context.Background())

	var attempts atomic.Int32
	srv.OnFindMissingBlobs = func(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
		n := attempts.Add(1)
		if n == 1 {
			// Cancel the context after first attempt
			cancel()
		}
		return nil, status.Error(codes.Unavailable, "transient")
	}

	_, err := client.FindMissingBlobs(ctx, []reapi.Digest{d})
	if err == nil {
		t.Fatal("expected error")
	}

	// Should have stopped retrying after context was cancelled (1 real attempt + context check)
	if attempts.Load() > 2 {
		t.Errorf("expected at most 2 attempts, got %d", attempts.Load())
	}
}
