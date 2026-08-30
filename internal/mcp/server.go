package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Server struct {
	apiURL string
	token  string
	client *http.Client
	input  io.Reader
	output io.Writer
}

func New(apiURL, token string) *Server {
	return &Server{apiURL: strings.TrimRight(apiURL, "/"), token: token,
		client: &http.Client{Timeout: 15 * time.Minute}, input: os.Stdin, output: os.Stdout}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.input)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	encoder := json.NewEncoder(s.output)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var value request
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			_ = encoder.Encode(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: err.Error()}})
			continue
		}
		if len(value.ID) == 0 {
			continue
		}
		result, err := s.handle(ctx, value)
		reply := response{JSONRPC: "2.0", ID: value.ID, Result: result}
		if err != nil {
			reply.Result = nil
			reply.Error = &rpcError{Code: -32000, Message: err.Error()}
		}
		if err := encoder.Encode(reply); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, request request) (any, error) {
	switch request.Method {
	case "initialize":
		return map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]string{"name": "pixel-steward", "version": "0.1.0"}}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools()}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, err
		}
		body, err := s.call(ctx, params.Name, params.Arguments)
		if err != nil {
			return map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]string{{"type": "text", "text": string(body)}}}, nil
	default:
		return nil, fmt.Errorf("method %s is not supported", request.Method)
	}
}

func tools() []map[string]any {
	return []map[string]any{
		{"name": "studio_budget", "description": "Return the current lease's hard inference budget, usage, reservations, remaining amounts, lease end, and operator-controlled reasoning setting.", "inputSchema": objectSchema(nil, nil)},
		{"name": "studio_sql", "description": "Run one flexible read-only PostgreSQL SELECT/WITH query against shared display history. Results are capped at 500 rows.", "inputSchema": objectSchema(map[string]any{"query": map[string]string{"type": "string", "description": "A read-only PostgreSQL query"}}, []string{"query"})},
		{"name": "studio_journal", "description": "Write your own concise 1-3 sentence account of what you showed, learned, or left for future agents. Write one entry before finishing each wake; this is the human-readable shared history.", "inputSchema": objectSchema(map[string]any{"entry": map[string]any{"type": "string", "minLength": 1, "maxLength": 1200, "description": "A self-contained 1-3 sentence journal entry"}}, []string{"entry"})},
		{"name": "studio_exec", "description": "Run an arbitrary shell command inside your disposable sandbox. This can create programs and assets or manage Docker in that sandbox; it cannot access the controller or homelab.", "inputSchema": objectSchema(map[string]any{"command": map[string]string{"type": "string"}, "timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 600000}}, []string{"command"})},
		{"name": "studio_publish", "description": "Explicitly commit one finished image from your sandbox to the physical display. The panel retains it until your next commit or blackout. Use this only when the asset is ready, not for animation frames.", "inputSchema": objectSchema(map[string]any{"path": map[string]string{"type": "string", "description": "Workspace-relative PNG, JPEG, GIF, or raw 64x64 RGB path"}}, []string{"path"})},
		{"name": "studio_watch", "description": "Turn a changing workspace image into a long-running physical scene without inference per frame. The controller archives samples, builds complete device-resident GIF clips in the background, and conservatively swaps clips while the old one keeps playing. Defaults are safe; optionally choose clip length, playback delay, and refresh cadence.", "inputSchema": objectSchema(map[string]any{
			"path":            map[string]string{"type": "string", "description": "Workspace-relative changing PNG, JPEG, or raw 64x64 RGB path"},
			"fps":             map[string]any{"type": "number", "minimum": 0.01, "description": "Local sampling rate; this is not a device upload rate"},
			"clip_frames":     map[string]any{"type": "integer", "minimum": 2, "maximum": 8, "description": "Frames in each resident animation; omit for controller default"},
			"frame_delay_ms":  map[string]any{"type": "integer", "minimum": 50, "maximum": 60000, "description": "Device-side playback delay per frame; omit for controller default"},
			"refresh_seconds": map[string]any{"type": "integer", "minimum": 1, "description": "How often a newly completed clip may replace the resident clip; controller minimum still applies"},
		}, []string{"path", "fps"})},
		{"name": "studio_schedule", "description": "Schedule a future model wake within this lease. You control timing, recurrence, missed-action policy, and arbitrary JSON context.", "inputSchema": objectSchema(map[string]any{
			"label": map[string]string{"type": "string"}, "run_at": map[string]string{"type": "string", "format": "date-time"},
			"interval_seconds": map[string]any{"type": "integer", "minimum": 0}, "missed_policy": map[string]any{"type": "string", "enum": []string{"skip", "defer"}},
			"payload": map[string]any{"type": "object", "additionalProperties": true},
		}, []string{"label", "run_at", "missed_policy"})},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	value := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		value["required"] = required
	}
	return value
}

func (s *Server) call(ctx context.Context, name string, arguments json.RawMessage) ([]byte, error) {
	method, path := http.MethodPost, ""
	body := arguments
	switch name {
	case "studio_budget":
		method, path, body = http.MethodGet, "/agent/v1/budget", nil
	case "studio_sql":
		path = "/agent/v1/sql"
	case "studio_journal":
		path = "/agent/v1/journal"
	case "studio_exec":
		path = "/agent/v1/exec"
	case "studio_publish":
		path = "/agent/v1/publish"
	case "studio_watch":
		path = "/agent/v1/watch"
	case "studio_schedule":
		path = "/agent/v1/schedules"
	default:
		return nil, fmt.Errorf("unknown tool %s", name)
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, s.apiURL+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("controller returned %s: %s", response.Status, strings.TrimSpace(string(result)))
	}
	return result, nil
}
