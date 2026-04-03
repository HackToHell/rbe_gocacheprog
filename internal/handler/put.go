package handler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HandlePut processes a put request.
func HandlePut(ctx context.Context, req *protocol.Request, dc *cache.DiskCache, client *reapi.Client, remoteWg *sync.WaitGroup, remoteSem *semaphore.Weighted, maxArtifactSize int64) *protocol.Response {
	if maxArtifactSize > 0 && req.BodySize > maxArtifactSize {
		return &protocol.Response{ID: req.ID, Err: fmt.Sprintf("artifact too large: %d bytes exceeds limit %d", req.BodySize, maxArtifactSize)}
	}

	actionIDHex := reapi.HexEncode(req.ActionID)
	outputIDHex := reapi.HexEncode(req.OutputID)

	bodyDigest, tempPath, err := writeBodyToTemp(dc, req.Body)
	req.Body = nil // allow GC to reclaim body memory
	if err != nil {
		return &protocol.Response{ID: req.ID, Err: fmt.Sprintf("write body: %v", err)}
	}

	meta := &cache.Metadata{
		OutputIDHex:   outputIDHex,
		Size:          req.BodySize,
		Time:          time.Now(),
		CASDigestHash: bodyDigest.Hash,
		CASDigestSize: bodyDigest.Size,
	}

	diskPath, err := dc.Install(actionIDHex, tempPath, meta)
	if err != nil {
		return &protocol.Response{ID: req.ID, Err: fmt.Sprintf("local install: %v", err)}
	}

	if client != nil {
		remoteWg.Add(1)
		go func() {
			defer remoteWg.Done()
			if remoteSem != nil {
				if err := remoteSem.Acquire(context.Background(), 1); err != nil {
					slog.Warn("remote semaphore acquire failed", "error", err)
					return
				}
				defer remoteSem.Release(1)
			}
			bgCtx := context.WithoutCancel(ctx)
			if err := remotePopulate(bgCtx, client, actionIDHex, outputIDHex, diskPath, bodyDigest); err != nil {
				slog.Warn("remote populate failed", "action_id", actionIDHex, "error", err)
			}
		}()
	}

	return &protocol.Response{ID: req.ID, DiskPath: diskPath}
}

func writeBodyToTemp(dc *cache.DiskCache, body []byte) (reapi.Digest, string, error) {
	tmp, err := dc.TempFile()
	if err != nil {
		return reapi.Digest{}, "", err
	}
	tmpName := tmp.Name()

	h := reapi.SHA256Pool.Get().(hash.Hash)
	// Hash and write simultaneously to avoid traversing data twice.
	// Write directly to both instead of io.MultiWriter to avoid its allocation.
	h.Write(body)
	if _, err := tmp.Write(body); err != nil {
		h.Reset()
		reapi.SHA256Pool.Put(h)
		tmp.Close()
		os.Remove(tmpName)
		return reapi.Digest{}, "", err
	}

	if err := tmp.Close(); err != nil {
		h.Reset()
		reapi.SHA256Pool.Put(h)
		os.Remove(tmpName)
		return reapi.Digest{}, "", err
	}

	var buf [sha256.Size]byte
	digest := reapi.Digest{
		Hash: reapi.HexEncode(h.Sum(buf[:0])),
		Size: int64(len(body)),
	}
	h.Reset()
	reapi.SHA256Pool.Put(h)
	return digest, tmpName, nil
}

func remotePopulate(ctx context.Context, client *reapi.Client, actionIDHex, outputIDHex, bodyPath string, bodyDigest reapi.Digest) error {
	sd, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		return fmt.Errorf("synthetic digests: %w", err)
	}

	if err := client.UploadFile(ctx, bodyPath, bodyDigest); err != nil {
		return fmt.Errorf("upload body: %w", err)
	}

	for _, blob := range []struct {
		data   []byte
		digest reapi.Digest
	}{
		{sd.CommandData, sd.CommandDigest},
		{sd.DirData, sd.DirDigest},
		{sd.ActionData, sd.ActionDigest},
	} {
		if err := client.UploadBlob(ctx, blob.digest, blob.data); err != nil {
			return fmt.Errorf("upload proto blob: %w", err)
		}
	}

	if client.UpdateEnabled() {
		ar := reapi.SyntheticActionResult(outputIDHex, bodyDigest)
		if err := client.UpdateActionResult(ctx, sd.ActionDigest, ar); err != nil {
			if status.Code(err) == codes.FailedPrecondition {
				// Server GC'd blobs between upload and AC update. Re-upload and retry once.
				if err := client.UploadFile(ctx, bodyPath, bodyDigest); err != nil {
					return fmt.Errorf("re-upload body after FAILED_PRECONDITION: %w", err)
				}
				for _, blob := range []struct {
					data   []byte
					digest reapi.Digest
				}{
					{sd.CommandData, sd.CommandDigest},
					{sd.DirData, sd.DirDigest},
					{sd.ActionData, sd.ActionDigest},
				} {
					if err := client.UploadBlob(ctx, blob.digest, blob.data); err != nil {
						return fmt.Errorf("re-upload proto blob after FAILED_PRECONDITION: %w", err)
					}
				}
				if err := client.UpdateActionResult(ctx, sd.ActionDigest, ar); err != nil {
					return fmt.Errorf("retry update action result: %w", err)
				}
				return nil
			}
			return fmt.Errorf("update action result: %w", err)
		}
	}

	return nil
}
