package handler_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hacktohell/gocache-rbe/internal/cache"
	"github.com/hacktohell/gocache-rbe/internal/handler"
	"github.com/hacktohell/gocache-rbe/internal/protocol"
	"github.com/hacktohell/gocache-rbe/internal/reapi"
	"github.com/hacktohell/gocache-rbe/testutil/fakereapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"time"
)

func benchHandler(b *testing.B, withRemote bool) (*handler.Handler, func()) {
	b.Helper()
	dir := b.TempDir()
	dc, err := cache.NewDiskCache(dir, 1024*1024*1024)
	if err != nil {
		b.Fatal(err)
	}

	if !withRemote {
		return &handler.Handler{Cache: dc}, func() {}
	}

	srv, err := fakereapi.New()
	if err != nil {
		b.Fatal(err)
	}
	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		srv.Stop()
		b.Fatal(err)
	}
	client := reapi.NewClientFromConn(conn, "", 10*time.Second)
	h := &handler.Handler{Cache: dc, Client: client}
	return h, func() {
		conn.Close()
		srv.Stop()
	}
}

// --- HandlePut local-only (the synchronous hotpath: sha256 + write + install) ---

func BenchmarkHandlePutLocalOnly(b *testing.B) {
	for _, bodySize := range []int{64, 1024, 64 * 1024} {
		b.Run(byteSizeLabel(bodySize), func(b *testing.B) {
			h, cleanup := benchHandler(b, false)
			defer cleanup()

			body := make([]byte, bodySize)
			for i := range body {
				body[i] = byte(i % 256)
			}

			ctx := context.Background()
			b.SetBytes(int64(bodySize))
			b.ResetTimer()
			for i := range b.N {
				actionID := make([]byte, 32)
				actionID[0] = byte(i >> 24)
				actionID[1] = byte(i >> 16)
				actionID[2] = byte(i >> 8)
				actionID[3] = byte(i)

				outputID := make([]byte, 32)
				outputID[0] = byte(i)

				req := &protocol.Request{
					ID:       int64(i),
					Command:  "put",
					ActionID: actionID,
					OutputID: outputID,
					Body:     append([]byte(nil), body...), // copy since handler nils it
					BodySize: int64(bodySize),
				}
				resp := h.Handle(ctx, req)
				if resp.Err != "" {
					b.Fatal(resp.Err)
				}
			}
		})
	}
}

// --- HandleGet local hit (the fast path: hex encode + ReadMetadata + os.Stat + mutex) ---

func BenchmarkHandleGetLocalHit(b *testing.B) {
	h, cleanup := benchHandler(b, false)
	defer cleanup()

	ctx := context.Background()
	actionID := make([]byte, 32)
	actionID[0] = 0xAA
	outputID := make([]byte, 32)
	outputID[0] = 0xBB

	putReq := &protocol.Request{
		ID:       1,
		Command:  "put",
		ActionID: actionID,
		OutputID: outputID,
		Body:     make([]byte, 1024),
		BodySize: 1024,
	}
	resp := h.Handle(ctx, putReq)
	if resp.Err != "" {
		b.Fatal(resp.Err)
	}

	getReq := &protocol.Request{
		ID:       2,
		Command:  "get",
		ActionID: actionID,
	}

	b.ResetTimer()
	for range b.N {
		resp := h.Handle(ctx, getReq)
		if resp.Miss {
			b.Fatal("expected hit")
		}
	}
}

// --- HandleGet local miss (no remote) ---

func BenchmarkHandleGetLocalMiss(b *testing.B) {
	h, cleanup := benchHandler(b, false)
	defer cleanup()

	ctx := context.Background()
	actionID := make([]byte, 32)
	actionID[0] = 0xFF

	req := &protocol.Request{
		ID:       1,
		Command:  "get",
		ActionID: actionID,
	}

	b.ResetTimer()
	for range b.N {
		resp := h.Handle(ctx, req)
		if !resp.Miss {
			b.Fatal("expected miss")
		}
	}
}

// --- HandleGet local miss with remote miss (includes ComputeSyntheticDigests + gRPC AC lookup) ---

func BenchmarkHandleGetRemoteMiss(b *testing.B) {
	h, cleanup := benchHandler(b, true)
	defer cleanup()

	ctx := context.Background()
	actionID := make([]byte, 32)
	actionID[0] = 0xEE

	req := &protocol.Request{
		ID:       1,
		Command:  "get",
		ActionID: actionID,
	}

	b.ResetTimer()
	for range b.N {
		resp := h.Handle(ctx, req)
		if !resp.Miss {
			b.Fatal("expected miss")
		}
	}
}

// --- Full put-then-get local round trip ---

func BenchmarkPutThenGetRoundTrip(b *testing.B) {
	h, cleanup := benchHandler(b, false)
	defer cleanup()

	ctx := context.Background()
	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i % 256)
	}

	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := range b.N {
		actionID := make([]byte, 32)
		actionID[0] = byte(i >> 24)
		actionID[1] = byte(i >> 16)
		actionID[2] = byte(i >> 8)
		actionID[3] = byte(i)

		outputID := make([]byte, 32)
		outputID[0] = byte(i)

		putReq := &protocol.Request{
			ID:       int64(i * 2),
			Command:  "put",
			ActionID: actionID,
			OutputID: outputID,
			Body:     append([]byte(nil), body...),
			BodySize: int64(len(body)),
		}
		putResp := h.Handle(ctx, putReq)
		if putResp.Err != "" {
			b.Fatal(putResp.Err)
		}

		getReq := &protocol.Request{
			ID:       int64(i*2 + 1),
			Command:  "get",
			ActionID: actionID,
		}
		getResp := h.Handle(ctx, getReq)
		if getResp.Miss {
			b.Fatal("expected hit after put")
		}
	}
}

// --- HandleGet parallel contention on same key ---

func BenchmarkHandleGetLocalHitParallel(b *testing.B) {
	h, cleanup := benchHandler(b, false)
	defer cleanup()

	ctx := context.Background()
	actionID := make([]byte, 32)
	actionID[0] = 0xDD
	outputID := make([]byte, 32)
	outputID[0] = 0xCC

	putReq := &protocol.Request{
		ID:       1,
		Command:  "put",
		ActionID: actionID,
		OutputID: outputID,
		Body:     make([]byte, 1024),
		BodySize: 1024,
	}
	resp := h.Handle(ctx, putReq)
	if resp.Err != "" {
		b.Fatal(resp.Err)
	}

	getReq := &protocol.Request{
		ID:       2,
		Command:  "get",
		ActionID: actionID,
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp := h.Handle(ctx, getReq)
			if resp.Miss {
				b.Fatal("expected hit")
			}
		}
	})
}

func byteSizeLabel(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%dMiB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%dKiB", n/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
