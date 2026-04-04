// Package handler implements the GOCACHEPROG request routing and handling.
package handler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/hacktohell/gocache-rbe/internal/cache"
	"github.com/hacktohell/gocache-rbe/internal/protocol"
	"github.com/hacktohell/gocache-rbe/internal/reapi"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

// Handler routes GOCACHEPROG requests to the appropriate handler.
type Handler struct {
	Cache           *cache.DiskCache
	Client          *reapi.Client       // may be nil for local-only mode
	RemoteWg        sync.WaitGroup      // tracks in-flight remote populate goroutines
	sfGroup         singleflight.Group  // deduplicates concurrent GETs
	RemoteSem       *semaphore.Weighted // limits concurrent remote populates
	MaxArtifactSize int64               // max artifact size in bytes (0 = no limit)
}

// Handle processes a single request and returns a response.
func (h *Handler) Handle(ctx context.Context, req *protocol.Request) *protocol.Response {
	switch req.Command {
	case "get":
		return HandleGet(ctx, req, h.Cache, h.Client, &h.sfGroup)
	case "put":
		return HandlePut(ctx, req, h.Cache, h.Client, &h.RemoteWg, h.RemoteSem, h.MaxArtifactSize)
	case "close":
		return h.handleClose(req)
	default:
		return &protocol.Response{ID: req.ID, Err: "unknown command: " + req.Command}
	}
}

func (h *Handler) handleClose(req *protocol.Request) *protocol.Response {
	h.RemoteWg.Wait() // wait for in-flight remote populates before closing client
	h.Cache.UnpinAll()
	h.Cache.Trim()
	if h.Client != nil {
		h.Client.Close()
	}
	return &protocol.Response{ID: req.ID}
}

// Run starts the main request processing loop with concurrent workers.
func (h *Handler) Run(ctx context.Context, reader *protocol.Reader, writer *protocol.Writer, numWorkers int) {
	if err := writer.Write(protocol.Handshake()); err != nil {
		slog.Error("handshake failed", "error", err)
		return
	}

	requests := make(chan *protocol.Request, numWorkers*2)

	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for req := range requests {
				resp := h.Handle(ctx, req)
				if err := writer.Write(resp); err != nil {
					slog.Error("write response failed", "error", err)
				}
			}
		}()
	}

	for {
		req, err := reader.Read()
		if err != nil {
			break
		}
		if req.Command == "close" {
			close(requests)
			wg.Wait()
			resp := h.Handle(ctx, req)
			if err := writer.Write(resp); err != nil {
				slog.Error("write close response failed", "error", err)
			}
			return
		}
		requests <- req
	}

	close(requests)
	wg.Wait()
}
