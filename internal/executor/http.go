package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTP struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHTTP(baseURL, token string) (*HTTP, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("sandbox base URL must be an absolute HTTP URL")
	}
	if token == "" {
		return nil, fmt.Errorf("sandbox token is required")
	}
	return &HTTP{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: &http.Client{}}, nil
}

func (h *HTTP) Exec(ctx context.Context, leaseID, command string, timeout time.Duration) (Result, error) {
	body, _ := json.Marshal(map[string]any{"command": command, "timeout_ms": timeout.Milliseconds()})
	response, err := h.do(ctx, http.MethodPost, "/v1/leases/"+url.PathEscape(leaseID)+"/exec", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	var result Result
	err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result)
	return result, err
}

func (h *HTTP) ReadFile(ctx context.Context, leaseID, path string) (io.ReadCloser, string, error) {
	response, err := h.do(ctx, http.MethodGet, "/v1/leases/"+url.PathEscape(leaseID)+"/file?path="+url.QueryEscape(path), nil)
	if err != nil {
		return nil, "", err
	}
	return response.Body, response.Header.Get("Content-Type"), nil
}

func (h *HTTP) Suspend(ctx context.Context, leaseID string) error {
	return h.action(ctx, leaseID, "suspend")
}
func (h *HTTP) Resume(ctx context.Context, leaseID string) error {
	return h.action(ctx, leaseID, "resume")
}
func (h *HTTP) Destroy(ctx context.Context, leaseID string) error {
	return h.action(ctx, leaseID, "destroy")
}

func (h *HTTP) action(ctx context.Context, leaseID, action string) error {
	response, err := h.do(ctx, http.MethodPost, "/v1/leases/"+url.PathEscape(leaseID)+"/"+action, nil)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (h *HTTP) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+h.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		return nil, fmt.Errorf("sandbox returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return response, nil
}

type HTTPServer struct {
	token   string
	backend Executor
}

func NewHTTPServer(token string, backend Executor) *HTTPServer {
	return &HTTPServer{token: token, backend: backend}
}

func (s *HTTPServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "leases" {
		http.NotFound(response, request)
		return
	}
	leaseID, action := parts[2], parts[3]
	switch action {
	case "exec":
		var body struct {
			Command   string `json:"command"`
			TimeoutMS int64  `json:"timeout_ms"`
		}
		if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := s.backend.Exec(request.Context(), leaseID, body.Command, time.Duration(body.TimeoutMS)*time.Millisecond)
		writeExecutorJSON(response, result, err)
	case "file":
		reader, contentType, err := s.backend.ReadFile(request.Context(), leaseID, request.URL.Query().Get("path"))
		if err != nil {
			http.Error(response, err.Error(), http.StatusNotFound)
			return
		}
		defer reader.Close()
		response.Header().Set("Content-Type", contentType)
		_, _ = io.Copy(response, io.LimitReader(reader, 32<<20))
	case "suspend", "resume", "destroy":
		var err error
		if action == "suspend" {
			err = s.backend.Suspend(request.Context(), leaseID)
		} else if action == "resume" {
			err = s.backend.Resume(request.Context(), leaseID)
		} else {
			err = s.backend.Destroy(request.Context(), leaseID)
		}
		writeExecutorJSON(response, map[string]string{"status": action}, err)
	default:
		http.NotFound(response, request)
	}
}

func writeExecutorJSON(response http.ResponseWriter, value any, err error) {
	response.Header().Set("Content-Type", "application/json")
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(response).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(response).Encode(value)
}
