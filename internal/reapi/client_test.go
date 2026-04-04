package reapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/hacktohell/gocache-rbe/internal/reapi"
	"github.com/hacktohell/gocache-rbe/testutil/fakereapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
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

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		fallbackTLS bool
		wantAddr    string
		wantTLS     bool
		wantErr     bool
	}{
		// grpcs:// — TLS, default port 443
		{name: "grpcs_no_port", raw: "grpcs://example.com", wantAddr: "example.com:443", wantTLS: true},
		{name: "grpcs_with_port", raw: "grpcs://example.com:9092", wantAddr: "example.com:9092", wantTLS: true},
		{name: "grpcs_ignores_fallback_tls_false", raw: "grpcs://example.com", fallbackTLS: false, wantAddr: "example.com:443", wantTLS: true},
		{name: "grpcs_empty_host", raw: "grpcs://", wantErr: true},

		// grpc:// — insecure, default port 80
		{name: "grpc_no_port", raw: "grpc://localhost", wantAddr: "localhost:80", wantTLS: false},
		{name: "grpc_with_port", raw: "grpc://localhost:8080", wantAddr: "localhost:8080", wantTLS: false},
		{name: "grpc_ignores_fallback_tls_true", raw: "grpc://localhost", fallbackTLS: true, wantAddr: "localhost:80", wantTLS: false},
		{name: "grpc_empty_host", raw: "grpc://", wantErr: true},

		// bare host:port — TLS from fallback
		{name: "bare_with_port", raw: "localhost:9092", wantAddr: "localhost:9092", wantTLS: false},
		{name: "bare_with_port_tls_fallback", raw: "localhost:9092", fallbackTLS: true, wantAddr: "localhost:9092", wantTLS: true},
		{name: "bare_no_port", raw: "example.com", wantAddr: "example.com:443", wantTLS: false},
		{name: "bare_no_port_tls_fallback", raw: "example.com", fallbackTLS: true, wantAddr: "example.com:443", wantTLS: true},

		// unsupported schemes
		{name: "http_scheme", raw: "http://example.com", wantErr: true},
		{name: "https_scheme", raw: "https://example.com", wantErr: true},
		{name: "unknown_scheme", raw: "ftp://example.com", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, useTLS, err := reapi.ParseTarget(tc.raw, tc.fallbackTLS)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTarget(%q) = %q, %v, nil; want error", tc.raw, addr, useTLS)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTarget(%q) error: %v", tc.raw, err)
			}
			if addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", addr, tc.wantAddr)
			}
			if useTLS != tc.wantTLS {
				t.Errorf("useTLS = %v, want %v", useTLS, tc.wantTLS)
			}
		})
	}
}

func TestNewClientAuthHeaderSentOnRPC(t *testing.T) {
	srv, err := fakereapi.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	const wantHeader = "x-test-api-key"
	const wantToken = "secret-token-123"

	var gotToken string
	srv.OnFindMissingBlobs = func(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			if vals := md.Get(wantHeader); len(vals) > 0 {
				gotToken = vals[0]
			}
		}
		return &repb.FindMissingBlobsResponse{}, nil
	}

	client, err := reapi.NewClient(context.Background(), reapi.ClientConfig{
		Target:         "grpc://" + srv.Addr(),
		AuthHeader:     wantHeader,
		AuthToken:      wantToken,
		RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.FindMissingBlobs(context.Background(), []reapi.Digest{{Hash: "abc123", Size: 3}})
	if err != nil {
		t.Fatal(err)
	}

	if gotToken != wantToken {
		t.Errorf("auth token on RPC = %q, want %q", gotToken, wantToken)
	}
}

func TestNewClientNoAuthHeaderWhenNotConfigured(t *testing.T) {
	srv, err := fakereapi.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	var receivedKeys []string
	srv.OnFindMissingBlobs = func(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			for k := range md {
				receivedKeys = append(receivedKeys, k)
			}
		}
		return &repb.FindMissingBlobsResponse{}, nil
	}

	client, err := reapi.NewClient(context.Background(), reapi.ClientConfig{
		Target:         "grpc://" + srv.Addr(),
		RequestTimeout: 10 * time.Second,
		// No AuthHeader / AuthToken
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.FindMissingBlobs(context.Background(), []reapi.Digest{{Hash: "abc123", Size: 3}})
	if err != nil {
		t.Fatal(err)
	}

	for _, k := range receivedKeys {
		if strings.HasPrefix(k, "x-") {
			t.Errorf("unexpected metadata key %q sent without auth config", k)
		}
	}
}

func TestNewClientInvalidTarget(t *testing.T) {
	_, err := reapi.NewClient(context.Background(), reapi.ClientConfig{
		Target: "http://example.com",
	})
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
	if !strings.Contains(err.Error(), "invalid target") {
		t.Errorf("error = %q; want it to mention 'invalid target'", err.Error())
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
