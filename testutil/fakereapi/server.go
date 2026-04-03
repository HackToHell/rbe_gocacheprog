// Package fakereapi provides an in-process REAPI v2 server for testing.
// It implements CAS (ContentAddressableStorage), AC (ActionCache), ByteStream,
// and Capabilities services with in-memory storage and optional fault injection.
package fakereapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"sync"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is an in-process fake REAPI v2 server.
type Server struct {
	grpcServer *grpc.Server
	listener   net.Listener

	mu            sync.RWMutex
	blobs         map[string][]byte             // hash -> data
	actionCache   map[string]*repb.ActionResult // action digest hash -> result
	updateEnabled bool

	// Fault injection hooks. If set, called before normal handling.
	OnFindMissingBlobs   func(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error)
	OnGetActionResult    func(ctx context.Context, req *repb.GetActionResultRequest) (*repb.ActionResult, error)
	OnUpdateActionResult func(ctx context.Context, req *repb.UpdateActionResultRequest) (*repb.ActionResult, error)
	OnBatchReadBlobs     func(ctx context.Context, req *repb.BatchReadBlobsRequest) (*repb.BatchReadBlobsResponse, error)
	OnBatchUpdateBlobs   func(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error)
	OnByteStreamRead     func(req *bytestream.ReadRequest, stream bytestream.ByteStream_ReadServer) error
	OnByteStreamWrite    func(stream bytestream.ByteStream_WriteServer) error
}

// New creates a new fake REAPI server listening on a random port.
func New() (*Server, error) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("fakereapi: listen: %w", err)
	}

	s := &Server{
		grpcServer:    grpc.NewServer(),
		listener:      lis,
		blobs:         make(map[string][]byte),
		actionCache:   make(map[string]*repb.ActionResult),
		updateEnabled: true,
	}

	repb.RegisterContentAddressableStorageServer(s.grpcServer, &casServer{s: s})
	repb.RegisterActionCacheServer(s.grpcServer, &acServer{s: s})
	repb.RegisterCapabilitiesServer(s.grpcServer, &capServer{s: s})
	bytestream.RegisterByteStreamServer(s.grpcServer, &bsServer{s: s})

	go s.grpcServer.Serve(lis)
	return s, nil
}

// Addr returns the server's listen address (host:port).
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Stop gracefully stops the server.
func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
}

// SetUpdateEnabled controls whether UpdateActionResult is allowed.
func (s *Server) SetUpdateEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateEnabled = v
}

// PutBlob directly injects a blob into CAS (for test setup).
func (s *Server) PutBlob(hash string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[hash] = append([]byte(nil), data...)
}

// GetBlob retrieves a blob from CAS (for test assertions).
func (s *Server) GetBlob(hash string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.blobs[hash]
	return d, ok
}

// PutActionResult directly injects an AC entry (for test setup).
func (s *Server) PutActionResult(actionHash string, ar *repb.ActionResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actionCache[actionHash] = ar
}

// --- Capabilities service ---

type capServer struct {
	repb.UnimplementedCapabilitiesServer
	s *Server
}

func (c *capServer) GetCapabilities(ctx context.Context, req *repb.GetCapabilitiesRequest) (*repb.ServerCapabilities, error) {
	c.s.mu.RLock()
	updateEnabled := c.s.updateEnabled
	c.s.mu.RUnlock()

	return &repb.ServerCapabilities{
		CacheCapabilities: &repb.CacheCapabilities{
			DigestFunctions:        []repb.DigestFunction_Value{repb.DigestFunction_SHA256},
			MaxBatchTotalSizeBytes: 4 * 1024 * 1024, // 4 MiB
			ActionCacheUpdateCapabilities: &repb.ActionCacheUpdateCapabilities{
				UpdateEnabled: updateEnabled,
			},
		},
	}, nil
}

// --- ActionCache service ---

type acServer struct {
	repb.UnimplementedActionCacheServer
	s *Server
}

func (a *acServer) GetActionResult(ctx context.Context, req *repb.GetActionResultRequest) (*repb.ActionResult, error) {
	if a.s.OnGetActionResult != nil {
		return a.s.OnGetActionResult(ctx, req)
	}

	a.s.mu.RLock()
	defer a.s.mu.RUnlock()

	ar, ok := a.s.actionCache[req.GetActionDigest().GetHash()]
	if !ok {
		return nil, status.Error(codes.NotFound, "action result not found")
	}
	return ar, nil
}

func (a *acServer) UpdateActionResult(ctx context.Context, req *repb.UpdateActionResultRequest) (*repb.ActionResult, error) {
	if a.s.OnUpdateActionResult != nil {
		return a.s.OnUpdateActionResult(ctx, req)
	}

	a.s.mu.Lock()
	defer a.s.mu.Unlock()

	if !a.s.updateEnabled {
		return nil, status.Error(codes.PermissionDenied, "AC updates disabled")
	}

	a.s.actionCache[req.GetActionDigest().GetHash()] = req.GetActionResult()
	return req.GetActionResult(), nil
}

// --- CAS service ---

type casServer struct {
	repb.UnimplementedContentAddressableStorageServer
	s *Server
}

func (c *casServer) FindMissingBlobs(ctx context.Context, req *repb.FindMissingBlobsRequest) (*repb.FindMissingBlobsResponse, error) {
	if c.s.OnFindMissingBlobs != nil {
		return c.s.OnFindMissingBlobs(ctx, req)
	}

	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	var missing []*repb.Digest
	for _, d := range req.GetBlobDigests() {
		if _, ok := c.s.blobs[d.GetHash()]; !ok {
			missing = append(missing, d)
		}
	}
	return &repb.FindMissingBlobsResponse{MissingBlobDigests: missing}, nil
}

func (c *casServer) BatchUpdateBlobs(ctx context.Context, req *repb.BatchUpdateBlobsRequest) (*repb.BatchUpdateBlobsResponse, error) {
	if c.s.OnBatchUpdateBlobs != nil {
		return c.s.OnBatchUpdateBlobs(ctx, req)
	}

	c.s.mu.Lock()
	defer c.s.mu.Unlock()

	var responses []*repb.BatchUpdateBlobsResponse_Response
	for _, r := range req.GetRequests() {
		h := sha256.Sum256(r.GetData())
		hash := fmt.Sprintf("%x", h)
		if hash != r.GetDigest().GetHash() {
			responses = append(responses, &repb.BatchUpdateBlobsResponse_Response{
				Digest: r.GetDigest(),
				Status: status.New(codes.InvalidArgument, "digest mismatch").Proto(),
			})
			continue
		}
		c.s.blobs[hash] = append([]byte(nil), r.GetData()...)
		responses = append(responses, &repb.BatchUpdateBlobsResponse_Response{
			Digest: r.GetDigest(),
			Status: status.New(codes.OK, "").Proto(),
		})
	}
	return &repb.BatchUpdateBlobsResponse{Responses: responses}, nil
}

func (c *casServer) BatchReadBlobs(ctx context.Context, req *repb.BatchReadBlobsRequest) (*repb.BatchReadBlobsResponse, error) {
	if c.s.OnBatchReadBlobs != nil {
		return c.s.OnBatchReadBlobs(ctx, req)
	}

	c.s.mu.RLock()
	defer c.s.mu.RUnlock()

	var responses []*repb.BatchReadBlobsResponse_Response
	for _, d := range req.GetDigests() {
		data, ok := c.s.blobs[d.GetHash()]
		if !ok {
			responses = append(responses, &repb.BatchReadBlobsResponse_Response{
				Digest: d,
				Status: status.New(codes.NotFound, "blob not found").Proto(),
			})
			continue
		}
		responses = append(responses, &repb.BatchReadBlobsResponse_Response{
			Digest: d,
			Data:   data,
			Status: status.New(codes.OK, "").Proto(),
		})
	}
	return &repb.BatchReadBlobsResponse{Responses: responses}, nil
}

// --- ByteStream service ---

type bsServer struct {
	bytestream.UnimplementedByteStreamServer
	s *Server
}

func (b *bsServer) Read(req *bytestream.ReadRequest, stream bytestream.ByteStream_ReadServer) error {
	if b.s.OnByteStreamRead != nil {
		return b.s.OnByteStreamRead(req, stream)
	}

	// Parse resource name: {instance}/blobs/{hash}/{size}
	hash, err := parseBlobReadResource(req.GetResourceName())
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "bad resource name: %v", err)
	}

	b.s.mu.RLock()
	data, ok := b.s.blobs[hash]
	b.s.mu.RUnlock()

	if !ok {
		return status.Error(codes.NotFound, "blob not found")
	}

	offset := int(req.GetReadOffset())
	if offset > len(data) {
		offset = len(data)
	}
	data = data[offset:]

	const chunkSize = 1024 * 1024 // 1 MiB chunks
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		if err := stream.Send(&bytestream.ReadResponse{Data: data[:n]}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

func (b *bsServer) Write(stream bytestream.ByteStream_WriteServer) error {
	if b.s.OnByteStreamWrite != nil {
		return b.s.OnByteStreamWrite(stream)
	}

	var buf bytes.Buffer
	var hash string
	var resourceSet bool

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if !resourceSet && req.GetResourceName() != "" {
			var parseErr error
			hash, parseErr = parseBlobWriteResource(req.GetResourceName())
			if parseErr != nil {
				return status.Errorf(codes.InvalidArgument, "bad resource name: %v", parseErr)
			}
			resourceSet = true
		}

		buf.Write(req.GetData())

		if req.GetFinishWrite() {
			break
		}
	}

	if !resourceSet {
		return status.Error(codes.InvalidArgument, "no resource name provided")
	}

	data := buf.Bytes()
	h := sha256.Sum256(data)
	computed := fmt.Sprintf("%x", h)
	if computed != hash {
		return status.Errorf(codes.InvalidArgument, "digest mismatch: expected %s, got %s", hash, computed)
	}

	b.s.mu.Lock()
	b.s.blobs[hash] = data
	b.s.mu.Unlock()

	return stream.SendAndClose(&bytestream.WriteResponse{
		CommittedSize: int64(len(data)),
	})
}

// parseBlobReadResource extracts the hash from "{instance}/blobs/{hash}/{size}".
func parseBlobReadResource(name string) (string, error) {
	parts := splitResource(name)
	for i, p := range parts {
		if p == "blobs" && i+2 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("expected blobs/{hash}/{size} in resource name %q", name)
}

// parseBlobWriteResource extracts the hash from "{instance}/uploads/{uuid}/blobs/{hash}/{size}".
func parseBlobWriteResource(name string) (string, error) {
	parts := splitResource(name)
	// Find "blobs" segment
	for i, p := range parts {
		if p == "blobs" && i+2 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("expected blobs/{hash}/{size} in resource name %q", name)
}

func splitResource(name string) []string {
	var parts []string
	current := ""
	for _, c := range name {
		if c == '/' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
