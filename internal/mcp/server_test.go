package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInitializeAndListTools(t *testing.T) {
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\"}\n")
	var output bytes.Buffer
	server := New("http://invalid", "token")
	server.input, server.output = input, &output
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "studio_budget") || !strings.Contains(output.String(), "studio_journal") || !strings.Contains(output.String(), "protocolVersion") {
		t.Fatalf("unexpected output: %s", output.String())
	}
}
