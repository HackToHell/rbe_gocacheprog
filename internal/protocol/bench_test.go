package protocol_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hacktohell/gocache-rbe/internal/protocol"
)

// --- Reader benchmarks ---

func BenchmarkReaderReadGet(b *testing.B) {
	actionID := make([]byte, 32)
	for i := range actionID {
		actionID[i] = byte(i)
	}
	req := protocol.Request{
		ID:       1,
		Command:  "get",
		ActionID: actionID,
	}
	line, _ := json.Marshal(req)
	singleLine := string(line) + "\n"
	input := strings.Repeat(singleLine, b.N)

	r := protocol.NewReader(strings.NewReader(input))
	b.ResetTimer()
	b.SetBytes(int64(len(line)))
	for range b.N {
		_, err := r.Read()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReaderReadPut(b *testing.B) {
	actionID := make([]byte, 32)
	outputID := make([]byte, 32)
	for i := range actionID {
		actionID[i] = byte(i)
		outputID[i] = byte(i + 128)
	}

	for _, bodySize := range []int{64, 1024, 64 * 1024} {
		b.Run(byteSizeLabel(bodySize), func(b *testing.B) {
			body := make([]byte, bodySize)
			for i := range body {
				body[i] = byte(i % 256)
			}

			req := protocol.Request{
				ID:       2,
				Command:  "put",
				ActionID: actionID,
				OutputID: outputID,
				BodySize: int64(bodySize),
			}
			headerLine, _ := json.Marshal(req)
			bodyLine, _ := json.Marshal(body) // base64-encoded JSON string
			singleEntry := string(headerLine) + "\n" + string(bodyLine) + "\n"
			input := strings.Repeat(singleEntry, b.N)

			r := protocol.NewReader(strings.NewReader(input))
			b.ResetTimer()
			b.SetBytes(int64(len(headerLine) + len(bodyLine)))
			for range b.N {
				_, err := r.Read()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// --- Writer benchmarks ---

func BenchmarkWriterWriteHit(b *testing.B) {
	var buf bytes.Buffer
	w := protocol.NewWriter(&buf)

	resp := &protocol.Response{
		ID:       42,
		OutputID: make([]byte, 32),
		Size:     65536,
		DiskPath: "/cache/ab/abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789-d",
		Time:     time.Now(),
	}

	// Warm up to measure size
	w.Write(resp)
	lineSize := buf.Len()
	buf.Reset()

	b.ResetTimer()
	b.SetBytes(int64(lineSize))
	for range b.N {
		buf.Reset()
		if err := w.Write(resp); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriterWriteMiss(b *testing.B) {
	var buf bytes.Buffer
	w := protocol.NewWriter(&buf)

	resp := &protocol.Response{
		ID:   42,
		Miss: true,
	}

	b.ResetTimer()
	for range b.N {
		buf.Reset()
		if err := w.Write(resp); err != nil {
			b.Fatal(err)
		}
	}
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
