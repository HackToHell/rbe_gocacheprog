package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
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

// TestE2EProtocolTranscript tests the full stdin/stdout protocol flow.
func TestE2EProtocolTranscript(t *testing.T) {
	srv, err := fakereapi.New()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	conn, err := grpc.NewClient(srv.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	client := reapi.NewClientFromConn(conn, "", 10*time.Second)
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	h := &handler.Handler{Cache: dc, Client: client}

	actionID := makeActionID(0x20)
	outputID := makeOutputID(0xAA)

	// Build stdin: put then get then close
	var stdin bytes.Buffer
	putBody := []byte("e2e test artifact")
	bodyJSON, _ := json.Marshal(putBody)

	// PUT request
	putReq := protocol.Request{
		ID:       1,
		Command:  "put",
		ActionID: actionID,
		OutputID: outputID,
		BodySize: int64(len(putBody)),
	}
	putJSON, _ := json.Marshal(putReq)
	stdin.Write(putJSON)
	stdin.WriteByte('\n')
	stdin.Write(bodyJSON)
	stdin.WriteByte('\n')

	// GET request
	getReq := protocol.Request{
		ID:       2,
		Command:  "get",
		ActionID: actionID,
	}
	getJSON, _ := json.Marshal(getReq)
	stdin.Write(getJSON)
	stdin.WriteByte('\n')

	// CLOSE request
	closeReq := protocol.Request{
		ID:      3,
		Command: "close",
	}
	closeJSON, _ := json.Marshal(closeReq)
	stdin.Write(closeJSON)
	stdin.WriteByte('\n')

	reader := protocol.NewReader(&stdin)
	var stdout bytes.Buffer
	writer := protocol.NewWriter(&stdout)

	h.Run(context.Background(), reader, writer, 1)

	// Parse responses
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 response lines, got %d: %s", len(lines), stdout.String())
	}

	// First line: handshake
	var handshake protocol.Response
	if err := json.Unmarshal([]byte(lines[0]), &handshake); err != nil {
		t.Fatalf("parse handshake: %v", err)
	}
	if handshake.ID != 0 {
		t.Errorf("handshake ID = %d, want 0", handshake.ID)
	}
	if len(handshake.KnownCommands) != 3 {
		t.Errorf("handshake commands: %v", handshake.KnownCommands)
	}

	// Remaining lines: responses (serial with 1 worker)
	responses := make(map[int64]*protocol.Response)
	for _, line := range lines[1:] {
		var resp protocol.Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("parse response: %v: %s", err, line)
		}
		responses[resp.ID] = &resp
	}

	// PUT response
	putResp, ok := responses[1]
	if !ok {
		t.Fatal("missing PUT response")
	}
	if putResp.Err != "" {
		t.Errorf("PUT error: %s", putResp.Err)
	}
	if putResp.DiskPath == "" {
		t.Error("PUT missing DiskPath")
	}

	// GET response
	getResp, ok := responses[2]
	if !ok {
		t.Fatal("missing GET response")
	}
	if getResp.Miss {
		t.Error("GET should be a hit")
	}
	if getResp.DiskPath == "" {
		t.Error("GET missing DiskPath")
	}
	if getResp.Size != int64(len(putBody)) {
		t.Errorf("GET size = %d, want %d", getResp.Size, len(putBody))
	}

	// CLOSE response
	closeResp, ok := responses[3]
	if !ok {
		t.Fatal("missing CLOSE response")
	}
	if closeResp.Err != "" {
		t.Errorf("CLOSE error: %s", closeResp.Err)
	}
}

// TestE2ELocalOnlyMode tests operation when remote is unavailable.
func TestE2ELocalOnlyMode(t *testing.T) {
	dc, err := cache.NewDiskCache(t.TempDir(), 100*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	h := &handler.Handler{Cache: dc, Client: nil}

	actionID := makeActionID(0x30)
	outputID := makeOutputID(0xBB)
	putBody := []byte("local only e2e")

	var stdin bytes.Buffer
	bodyJSON, _ := json.Marshal(putBody)

	// PUT
	putReq := protocol.Request{ID: 1, Command: "put", ActionID: actionID, OutputID: outputID, BodySize: int64(len(putBody))}
	putJSON, _ := json.Marshal(putReq)
	stdin.Write(putJSON)
	stdin.WriteByte('\n')
	stdin.Write(bodyJSON)
	stdin.WriteByte('\n')

	// GET for same action
	getReq := protocol.Request{ID: 2, Command: "get", ActionID: actionID}
	getJSON, _ := json.Marshal(getReq)
	stdin.Write(getJSON)
	stdin.WriteByte('\n')

	// GET for unknown action
	getReq2 := protocol.Request{ID: 3, Command: "get", ActionID: makeActionID(0x99)}
	getJSON2, _ := json.Marshal(getReq2)
	stdin.Write(getJSON2)
	stdin.WriteByte('\n')

	// CLOSE
	closeReq := protocol.Request{ID: 4, Command: "close"}
	closeJSON, _ := json.Marshal(closeReq)
	stdin.Write(closeJSON)
	stdin.WriteByte('\n')

	reader := protocol.NewReader(&stdin)
	var stdout bytes.Buffer
	writer := protocol.NewWriter(&stdout)

	h.Run(context.Background(), reader, writer, 1)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 5 { // handshake + 4 responses
		t.Fatalf("expected at least 5 lines, got %d: %s", len(lines), stdout.String())
	}
	responses := make(map[int64]*protocol.Response)
	for _, line := range lines[1:] { // skip handshake
		var resp protocol.Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("parse response: %v: %s", err, line)
		}
		responses[resp.ID] = &resp
	}

	// PUT should succeed locally
	if responses[1].Err != "" {
		t.Errorf("PUT error: %s", responses[1].Err)
	}

	// GET for same action should hit
	if responses[2].Miss {
		t.Error("GET should hit after local PUT")
	}

	// GET for unknown should miss
	if !responses[3].Miss {
		t.Error("GET for unknown should miss")
	}
}
