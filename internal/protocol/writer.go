package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Writer writes GOCACHEPROG responses to an io.Writer (typically stdout).
// It is safe for concurrent use.
type Writer struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewWriter creates a Writer.
func NewWriter(w io.Writer) *Writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Writer{enc: enc}
}

// Write writes a single response as a JSON line.
func (w *Writer) Write(resp *Response) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.enc.Encode(resp); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	return nil
}
