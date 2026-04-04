package handler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"os"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
	"golang.org/x/sync/singleflight"
)

// HandleGet processes a get request.
func HandleGet(ctx context.Context, req *protocol.Request, dc *cache.DiskCache, client *reapi.Client, sfGroup *singleflight.Group) *protocol.Response {
	actionIDHex := reapi.HexEncode(req.ActionID)

	// Step 1: Check local disk cache.
	meta, bodyPath, hit := dc.Lookup(actionIDHex)
	if hit {
		outputID, err := reapi.HexDecode(meta.OutputIDHex)
		if err != nil {
			dc.Remove(actionIDHex)
			return &protocol.Response{ID: req.ID, Miss: true}
		}
		return &protocol.Response{
			ID:       req.ID,
			OutputID: outputID,
			Size:     meta.Size,
			DiskPath: bodyPath,
			Time:     meta.Time,
		}
	}

	// Step 2: Check for metadata stub (body evicted, CAS refill possible).
	// Lookup already returned metadata when the body is missing (meta != nil, hit == false).
	// Reuse it to avoid a redundant ReadMetadata + os.Stat.
	if meta != nil && client != nil {
		resp := refillFromCAS(ctx, req, dc, client, actionIDHex, meta)
		if resp != nil {
			return resp
		}
	}

	// Step 3: Remote AC lookup with singleflight deduplication.
	if client != nil {
		type sfResult struct {
			resp *protocol.Response
		}
		v, _, _ := sfGroup.Do(actionIDHex, func() (interface{}, error) {
			return &sfResult{resp: remoteACLookup(ctx, req, dc, client, actionIDHex)}, nil
		})
		result := v.(*sfResult)
		// Each caller needs its own response with the correct request ID.
		orig := result.resp
		return &protocol.Response{
			ID:       req.ID,
			Err:      orig.Err,
			Miss:     orig.Miss,
			OutputID: orig.OutputID,
			Size:     orig.Size,
			Time:     orig.Time,
			DiskPath: orig.DiskPath,
		}
	}

	return &protocol.Response{ID: req.ID, Miss: true}
}

func refillFromCAS(ctx context.Context, req *protocol.Request, dc *cache.DiskCache, client *reapi.Client, actionIDHex string, meta *cache.Metadata) *protocol.Response {
	digest := reapi.Digest{Hash: meta.CASDigestHash, Size: meta.CASDigestSize}
	diskPath, err := downloadAndInstall(ctx, dc, client, actionIDHex, digest, meta)
	if err != nil {
		return nil // fall through to AC lookup
	}

	outputID, err := reapi.HexDecode(meta.OutputIDHex)
	if err != nil {
		return nil
	}

	return &protocol.Response{
		ID:       req.ID,
		OutputID: outputID,
		Size:     meta.Size,
		DiskPath: diskPath,
		Time:     meta.Time,
	}
}

func remoteACLookup(ctx context.Context, req *protocol.Request, dc *cache.DiskCache, client *reapi.Client, actionIDHex string) *protocol.Response {
	sd, err := reapi.ComputeSyntheticDigests(actionIDHex)
	if err != nil {
		return &protocol.Response{ID: req.ID, Miss: true}
	}

	ar, err := client.GetActionResult(ctx, sd.ActionDigest)
	if err != nil || ar == nil {
		return &protocol.Response{ID: req.ID, Miss: true}
	}

	if len(ar.GetOutputFiles()) == 0 {
		return &protocol.Response{ID: req.ID, Miss: true}
	}

	of := ar.GetOutputFiles()[0]
	bodyDigest := reapi.DigestFromProto(of.GetDigest())
	outputIDHex := of.GetPath()

	outputID, err := reapi.HexDecode(outputIDHex)
	if err != nil {
		return &protocol.Response{ID: req.ID, Miss: true}
	}

	meta := &cache.Metadata{
		OutputIDHex:   outputIDHex,
		Size:          bodyDigest.Size,
		Time:          time.Now(),
		CASDigestHash: bodyDigest.Hash,
		CASDigestSize: bodyDigest.Size,
	}

	diskPath, err := downloadAndInstall(ctx, dc, client, actionIDHex, bodyDigest, meta)
	if err != nil {
		return &protocol.Response{ID: req.ID, Miss: true}
	}

	return &protocol.Response{
		ID:       req.ID,
		OutputID: outputID,
		Size:     bodyDigest.Size,
		DiskPath: diskPath,
		Time:     meta.Time,
	}
}

func downloadAndInstall(ctx context.Context, dc *cache.DiskCache, client *reapi.Client, actionIDHex string, digest reapi.Digest, meta *cache.Metadata) (string, error) {
	tmp, err := dc.TempFile()
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()

	// Hash inline during download to avoid a second full read of the file.
	h := reapi.SHA256Pool.Get().(hash.Hash)
	defer func() { h.Reset(); reapi.SHA256Pool.Put(h) }()

	w := io.MultiWriter(tmp, h)
	if err := client.DownloadBlob(ctx, digest, w); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	var buf [sha256.Size]byte
	gotHash := reapi.HexEncodeFixed(h.Sum(buf[:0]))
	info, err := os.Stat(tmpName)
	if err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if gotHash != digest.Hash || info.Size() != digest.Size {
		os.Remove(tmpName)
		return "", fmt.Errorf("digest mismatch: got %s/%d, want %s/%d", gotHash, info.Size(), digest.Hash, digest.Size)
	}

	return dc.Install(actionIDHex, tmpName, meta)
}
