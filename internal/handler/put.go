package handler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
)

// HandlePut processes a put request.
func HandlePut(ctx context.Context, req *protocol.Request, dc *cache.DiskCache, client *reapi.Client, remoteWg *sync.WaitGroup) *protocol.Response {
	actionIDHex := reapi.HexEncode(req.ActionID)
	outputIDHex := reapi.HexEncode(req.OutputID)

	bodyDigest, tempPath, err := writeBodyToTemp(dc, req.Body)
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

	h := sha256.New()
	h.Write(body)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return reapi.Digest{}, "", err
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return reapi.Digest{}, "", err
	}

	digest := reapi.Digest{
		Hash: fmt.Sprintf("%x", h.Sum(nil)),
		Size: int64(len(body)),
	}
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
			return fmt.Errorf("update action result: %w", err)
		}
	}

	return nil
}
