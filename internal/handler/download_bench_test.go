package handler_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/hacktohell/rbe_gocacheprog/internal/cache"
	"github.com/hacktohell/rbe_gocacheprog/internal/handler"
	"github.com/hacktohell/rbe_gocacheprog/internal/protocol"
	"github.com/hacktohell/rbe_gocacheprog/internal/reapi"
	"github.com/hacktohell/rbe_gocacheprog/testutil/fakereapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// BenchmarkHandleGetRemoteHit benchmarks the warm-remote cold-local path:
// local miss → remote AC hit → CAS download → install to disk.
func BenchmarkHandleGetRemoteHit(b *testing.B) {
	for _, bodySize := range []int{64, 1024, 64 * 1024, 256 * 1024} {
		b.Run(byteSizeLabel(bodySize), func(b *testing.B) {
			srv, err := fakereapi.New()
			if err != nil {
				b.Fatal(err)
			}
			defer srv.Stop()

			conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()

			client := reapi.NewClientFromConn(conn, "", 10*time.Second)

			// Pre-seed N distinct entries in the fake remote so each iteration
			// downloads a unique blob (avoids local cache hits on repeat).
			type seeded struct {
				actionID []byte
			}
			seeds := make([]seeded, b.N)
			for i := range b.N {
				body := make([]byte, bodySize)
				rand.Read(body)
				bodyDigest := reapi.DigestBytes(body)

				actionID := make([]byte, 32)
				actionID[0] = byte(i >> 24)
				actionID[1] = byte(i >> 16)
				actionID[2] = byte(i >> 8)
				actionID[3] = byte(i)
				actionIDHex := reapi.HexEncode(actionID)

				outputID := make([]byte, 32)
				outputID[0] = byte(i)
				outputIDHex := reapi.HexEncode(outputID)

				srv.PutBlob(bodyDigest.Hash, body)

				sd, err := reapi.ComputeSyntheticDigests(actionIDHex)
				if err != nil {
					b.Fatal(err)
				}
				ar := reapi.SyntheticActionResult(outputIDHex, bodyDigest)
				srv.PutActionResult(sd.ActionDigest.Hash, ar)

				seeds[i] = seeded{actionID: actionID}
			}

			dc, err := cache.NewDiskCache(b.TempDir(), 1024*1024*1024)
			if err != nil {
				b.Fatal(err)
			}
			h := &handler.Handler{Cache: dc, Client: client}

			ctx := context.Background()
			b.SetBytes(int64(bodySize))
			b.ResetTimer()
			for i := range b.N {
				req := &protocol.Request{
					ID:       int64(i),
					Command:  "get",
					ActionID: seeds[i].actionID,
				}
				resp := h.Handle(ctx, req)
				if resp.Miss {
					b.Fatalf("iter %d: expected hit", i)
				}
				if resp.DiskPath == "" {
					b.Fatalf("iter %d: expected DiskPath", i)
				}
			}
		})
	}
}

// BenchmarkDownloadAndInstallOnly isolates just the download+verify+install
// cost by pre-seeding the remote and using a fresh cache each time via sub-benchmarks.
func BenchmarkDownloadAndInstallOnly(b *testing.B) {
	for _, bodySize := range []int{1024, 64 * 1024, 512 * 1024} {
		b.Run(fmt.Sprintf("%s", byteSizeLabel(bodySize)), func(b *testing.B) {
			srv, err := fakereapi.New()
			if err != nil {
				b.Fatal(err)
			}
			defer srv.Stop()

			conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()

			client := reapi.NewClientFromConn(conn, "", 10*time.Second)

			// Pre-seed blobs
			type seeded struct {
				actionID []byte
				body     []byte
			}
			seeds := make([]seeded, b.N)
			for i := range b.N {
				body := make([]byte, bodySize)
				rand.Read(body)
				bodyDigest := reapi.DigestBytes(body)

				actionID := make([]byte, 32)
				actionID[0] = byte(i >> 24)
				actionID[1] = byte(i >> 16)
				actionID[2] = byte(i >> 8)
				actionID[3] = byte(i)
				actionIDHex := reapi.HexEncode(actionID)

				outputID := make([]byte, 32)
				outputID[0] = byte(i)
				outputIDHex := reapi.HexEncode(outputID)

				srv.PutBlob(bodyDigest.Hash, body)

				sd, err := reapi.ComputeSyntheticDigests(actionIDHex)
				if err != nil {
					b.Fatal(err)
				}
				ar := reapi.SyntheticActionResult(outputIDHex, bodyDigest)
				srv.PutActionResult(sd.ActionDigest.Hash, ar)

				seeds[i] = seeded{actionID: actionID, body: body}
			}

			dc, err := cache.NewDiskCache(b.TempDir(), 1024*1024*1024)
			if err != nil {
				b.Fatal(err)
			}
			h := &handler.Handler{Cache: dc, Client: client}

			ctx := context.Background()
			b.SetBytes(int64(bodySize))
			b.ResetTimer()
			for i := range b.N {
				req := &protocol.Request{
					ID:       int64(i),
					Command:  "get",
					ActionID: seeds[i].actionID,
				}
				resp := h.Handle(ctx, req)
				if resp.Miss {
					b.Fatalf("iter %d: expected hit", i)
				}
			}
		})
	}
}
