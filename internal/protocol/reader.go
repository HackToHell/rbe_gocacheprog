package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Reader reads GOCACHEPROG requests from an io.Reader (typically stdin).
type Reader struct {
	scanner *bufio.Scanner
}

// NewReader creates a Reader that reads JSON requests line by line.
func NewReader(r io.Reader) *Reader {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 64*1024*1024) // up to 64 MiB lines for large bodies
	return &Reader{scanner: s}
}

// scanNonEmpty advances the scanner past blank lines and returns the next
// non-empty line. Returns false at EOF or on scanner error.
func (r *Reader) scanNonEmpty() bool {
	for r.scanner.Scan() {
		if len(r.scanner.Bytes()) > 0 {
			return true
		}
	}
	return false
}

// Read reads the next request. For put requests with BodySize > 0,
// it reads the following line as a base64-encoded JSON string literal
// and decodes it into req.Body.
func (r *Reader) Read() (*Request, error) {
	if !r.scanNonEmpty() {
		if err := r.scanner.Err(); err != nil {
			return nil, fmt.Errorf("read request: %w", err)
		}
		return nil, io.EOF
	}

	var req Request
	if err := json.Unmarshal(r.scanner.Bytes(), &req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}

	if req.Command == "put" && req.BodySize > 0 {
		if !r.scanNonEmpty() {
			if err := r.scanner.Err(); err != nil {
				return nil, fmt.Errorf("read body line: %w", err)
			}
			return nil, fmt.Errorf("read body line: unexpected EOF")
		}

		// The body line is a JSON string literal containing base64-encoded bytes.
		var bodyBytes []byte
		if err := json.Unmarshal(r.scanner.Bytes(), &bodyBytes); err != nil {
			return nil, fmt.Errorf("decode body: %w", err)
		}
		if int64(len(bodyBytes)) != req.BodySize {
			return nil, fmt.Errorf("body size mismatch: got %d, declared %d", len(bodyBytes), req.BodySize)
		}
		req.Body = bodyBytes
	}

	return &req, nil
}
