package protocol_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/hacktohell/gocache-rbe/internal/protocol"
)

func TestHandshake(t *testing.T) {
	resp := protocol.Handshake()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ID":0,"KnownCommands":["get","put","close"]}`
	if string(data) != want {
		t.Errorf("handshake:\n  got  %s\n  want %s", data, want)
	}
}

func TestReaderGet(t *testing.T) {
	input := `{"ID":1,"Command":"get","ActionID":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}` + "\n"
	r := protocol.NewReader(strings.NewReader(input))
	req, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if req.ID != 1 {
		t.Errorf("ID = %d, want 1", req.ID)
	}
	if req.Command != "get" {
		t.Errorf("Command = %q, want get", req.Command)
	}
	if len(req.ActionID) != 32 {
		t.Errorf("ActionID len = %d, want 32", len(req.ActionID))
	}
}

func TestReaderPutWithBody(t *testing.T) {
	body := []byte("hello world")
	bodyB64, _ := json.Marshal(body) // JSON string with base64
	input := `{"ID":2,"Command":"put","ActionID":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","OutputID":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","BodySize":11}` + "\n" + string(bodyB64) + "\n"

	r := protocol.NewReader(strings.NewReader(input))
	req, err := r.Read()
	if err != nil {
		t.Fatal(err)
	}
	if req.Command != "put" {
		t.Errorf("Command = %q, want put", req.Command)
	}
	if string(req.Body) != "hello world" {
		t.Errorf("Body = %q, want %q", req.Body, "hello world")
	}
	if req.BodySize != 11 {
		t.Errorf("BodySize = %d, want 11", req.BodySize)
	}
}

func TestReaderEOF(t *testing.T) {
	r := protocol.NewReader(strings.NewReader(""))
	_, err := r.Read()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReaderInvalidJSON(t *testing.T) {
	r := protocol.NewReader(strings.NewReader("not json\n"))
	_, err := r.Read()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestReaderInvalidBodyLine(t *testing.T) {
	input := `{"ID":3,"Command":"put","ActionID":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","OutputID":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","BodySize":5}` + "\n" + "not-valid-json-string\n"
	r := protocol.NewReader(strings.NewReader(input))
	_, err := r.Read()
	if err == nil {
		t.Error("expected error for invalid body line")
	}
}

func TestReaderPutMissingBodyLine(t *testing.T) {
	input := `{"ID":4,"Command":"put","ActionID":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","OutputID":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","BodySize":5}` + "\n"
	r := protocol.NewReader(strings.NewReader(input))
	_, err := r.Read()
	if err == nil {
		t.Error("expected error for missing body line")
	}
}

func TestWriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := protocol.NewWriter(&buf)

	resp := &protocol.Response{
		ID:       42,
		DiskPath: "/tmp/test",
		Size:     100,
	}
	if err := w.Write(resp); err != nil {
		t.Fatal(err)
	}

	var got protocol.Response
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != 42 {
		t.Errorf("ID = %d, want 42", got.ID)
	}
	if got.DiskPath != "/tmp/test" {
		t.Errorf("DiskPath = %q, want /tmp/test", got.DiskPath)
	}
}
