package reapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// uploadBufPool reuses 1 MiB buffers for ByteStream uploads.
var uploadBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 1024*1024)
		return &b
	},
}

// FindMissingBlobs checks which digests are absent from CAS.
func (c *Client) FindMissingBlobs(ctx context.Context, digests []Digest) ([]Digest, error) {
	return retryRPCWithCtx(ctx, c.cb, func() ([]Digest, error) {
		rCtx, cancel := c.rpcCtx(ctx)
		defer cancel()

		casClient := c.casClient

		// Single backing array for both the pointer slice and the proto structs.
		protoBacking := make([]repb.Digest, len(digests))
		protoDigests := make([]*repb.Digest, len(digests))
		for i, d := range digests {
			d.FillProto(&protoBacking[i])
			protoDigests[i] = &protoBacking[i]
		}

		resp, err := casClient.FindMissingBlobs(rCtx, &repb.FindMissingBlobsRequest{
			InstanceName: c.instanceName,
			BlobDigests:  protoDigests,
		})
		if err != nil {
			return nil, fmt.Errorf("FindMissingBlobs: %w", err)
		}

		missing := make([]Digest, len(resp.GetMissingBlobDigests()))
		for i, d := range resp.GetMissingBlobDigests() {
			missing[i] = DigestFromProto(d)
		}
		return missing, nil
	})
}

// UploadBlob uploads data to CAS if missing. Uses batch for small blobs, ByteStream for large.
func (c *Client) UploadBlob(ctx context.Context, digest Digest, data []byte) error {
	missing, err := c.FindMissingBlobs(ctx, []Digest{digest})
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil // already exists
	}

	if digest.Size <= c.maxBatchSize {
		return c.batchUpload(ctx, digest, data)
	}
	return c.byteStreamUpload(ctx, digest, bytes.NewReader(data))
}

// UploadFile uploads a file to CAS if missing.
func (c *Client) UploadFile(ctx context.Context, path string, digest Digest) error {
	missing, err := c.FindMissingBlobs(ctx, []Digest{digest})
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if digest.Size <= c.maxBatchSize {
		// Read into a right-sized buffer instead of os.ReadFile which may
		// over-allocate when stat size and actual size diverge.
		data := make([]byte, digest.Size)
		if _, err := io.ReadFull(f, data); err != nil {
			return fmt.Errorf("read file for batch upload: %w", err)
		}
		return c.batchUpload(ctx, digest, data)
	}

	return c.byteStreamUpload(ctx, digest, f)
}

func (c *Client) batchUpload(ctx context.Context, digest Digest, data []byte) error {
	return retryRPCNoResultWithCtx(ctx, c.cb, func() error {
		rCtx, cancel := c.rpcCtx(ctx)
		defer cancel()

		casClient := c.casClient
		resp, err := casClient.BatchUpdateBlobs(rCtx, &repb.BatchUpdateBlobsRequest{
			InstanceName: c.instanceName,
			Requests: []*repb.BatchUpdateBlobsRequest_Request{
				{Digest: digest.ToProto(), Data: data},
			},
		})
		if err != nil {
			return fmt.Errorf("BatchUpdateBlobs: %w", err)
		}
		for _, r := range resp.GetResponses() {
			if r.GetStatus().GetCode() != int32(codes.OK) {
				return fmt.Errorf("BatchUpdateBlobs %s: %s", r.GetDigest().GetHash(), r.GetStatus().GetMessage())
			}
		}
		return nil
	})
}

func (c *Client) byteStreamUpload(ctx context.Context, digest Digest, r io.Reader) error {
	rCtx, cancel := c.rpcCtx(ctx)
	defer cancel()

	bsClient := c.bsClient
	stream, err := bsClient.Write(rCtx)
	if err != nil {
		return fmt.Errorf("ByteStream.Write: %w", err)
	}

	resourceName := fmt.Sprintf("%s/uploads/%s/blobs/%s/%d", c.instanceName, uuid.New().String(), digest.Hash, digest.Size)

	bufp := uploadBufPool.Get().(*[]byte)
	buf := *bufp
	defer uploadBufPool.Put(bufp)

	first := true
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			req := &bytestream.WriteRequest{
				Data:        buf[:n],
				FinishWrite: readErr == io.EOF,
			}
			if first {
				req.ResourceName = resourceName
				first = false
			}
			if err := stream.Send(req); err != nil {
				return fmt.Errorf("ByteStream.Write send: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read data: %w", readErr)
		}
	}

	_, err = stream.CloseAndRecv()
	if err != nil {
		return fmt.Errorf("ByteStream.Write close: %w", err)
	}
	return nil
}

// DownloadBlob downloads a blob from CAS to a writer. Uses batch for small, ByteStream for large.
func (c *Client) DownloadBlob(ctx context.Context, digest Digest, w io.Writer) error {
	if digest.Size <= c.maxBatchSize {
		return c.batchDownload(ctx, digest, w)
	}
	return c.byteStreamDownload(ctx, digest, w)
}

func (c *Client) batchDownload(ctx context.Context, digest Digest, w io.Writer) error {
	// No retry: w is a stateful io.Writer (typically a file). Retrying would
	// append duplicate data, corrupting the output. The caller's digest
	// verification catches transient failures.
	rCtx, cancel := c.rpcCtx(ctx)
	defer cancel()

	casClient := c.casClient
	resp, err := casClient.BatchReadBlobs(rCtx, &repb.BatchReadBlobsRequest{
		InstanceName: c.instanceName,
		Digests:      []*repb.Digest{digest.ToProto()},
	})
	if err != nil {
		return fmt.Errorf("BatchReadBlobs: %w", err)
	}
	for _, r := range resp.GetResponses() {
		s := status.FromProto(r.GetStatus())
		if s.Code() != codes.OK {
			return fmt.Errorf("BatchReadBlobs %s: %s", r.GetDigest().GetHash(), s.Message())
		}
		if _, err := w.Write(r.GetData()); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) byteStreamDownload(ctx context.Context, digest Digest, w io.Writer) error {
	rCtx, cancel := c.rpcCtx(ctx)
	defer cancel()

	bsClient := c.bsClient
	resourceName := fmt.Sprintf("%s/blobs/%s/%d", c.instanceName, digest.Hash, digest.Size)

	stream, err := bsClient.Read(rCtx, &bytestream.ReadRequest{
		ResourceName: resourceName,
	})
	if err != nil {
		return fmt.Errorf("ByteStream.Read: %w", err)
	}

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ByteStream.Read recv: %w", err)
		}
		if _, err := w.Write(resp.GetData()); err != nil {
			return err
		}
	}
}
