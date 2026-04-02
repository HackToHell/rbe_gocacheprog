// Package protocol implements the GOCACHEPROG JSON wire protocol.
package protocol

import "time"

// Request is a cache request from cmd/go via stdin.
type Request struct {
	ID       int64  `json:"ID"`
	Command  string `json:"Command"`
	ActionID []byte `json:"ActionID,omitempty"`
	OutputID []byte `json:"OutputID,omitempty"`
	// Body is populated by the reader after decoding the base64 body line.
	Body     []byte `json:"-"`
	BodySize int64  `json:"BodySize,omitempty"`
}

// Response is a cache response to cmd/go via stdout.
type Response struct {
	ID            int64     `json:"ID"`
	Err           string    `json:"Err,omitempty"`
	Miss          bool      `json:"Miss,omitempty"`
	OutputID      []byte    `json:"OutputID,omitempty"`
	Size          int64     `json:"Size,omitempty"`
	Time          time.Time `json:"Time,omitzero"`
	DiskPath      string    `json:"DiskPath,omitempty"`
	KnownCommands []string  `json:"KnownCommands,omitempty"`
}

// Handshake returns the initial startup response.
func Handshake() *Response {
	return &Response{
		KnownCommands: []string{"get", "put", "close"},
	}
}
