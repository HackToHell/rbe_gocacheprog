package handler

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
)

// HandleGet processes a get request.
func HandleGet(ctx context.Context, req *protocol.Request, dc *cache.DiskCache, client *reapi.Client) *protocol.Response {
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
	if stubMeta, isStub := dc.HasMetadataStub(actionIDHex); isStub && client != nil {
		resp := refillFromCAS(ctx, req, dc, client, actionIDHex, stubMeta)
		if resp != nil {
			return resp
		}
	}

	// Step 3: Remote AC lookup.
	if client != nil {
		return remoteACLookup(ctx, req, dc, client, actionIDHex)
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

	if err := client.DownloadBlob(ctx, digest, tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}

	gotDigest, err := reapi.DigestFile(tmpName)
	if err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if gotDigest.Hash != digest.Hash || gotDigest.Size != digest.Size {
		os.Remove(tmpName)
		return "", fmt.Errorf("digest mismatch: got %s/%d, want %s/%d", gotDigest.Hash, gotDigest.Size, digest.Hash, digest.Size)
	}

	return dc.Install(actionIDHex, tmpName, meta)
}
