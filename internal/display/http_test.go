package display

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPAdapterContract(t *testing.T) {
	var mu sync.Mutex
	var published, screenCommands int
	var hold string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch request.URL.Path {
		case "/api/image":
			published++
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm() error = %v", err)
			}
			file, _, err := request.FormFile("file")
			if err != nil {
				t.Errorf("FormFile() error = %v", err)
				return
			}
			defer file.Close()
			hold = request.FormValue("seconds")
			if payload, _ := io.ReadAll(file); string(payload) != "png" {
				t.Errorf("payload = %q", payload)
			}
			response.WriteHeader(http.StatusOK)
		case "/api/screen":
			screenCommands++
			response.WriteHeader(http.StatusOK)
		case "/api/status":
			_ = json.NewEncoder(response).Encode(map[string]any{"device": map[string]any{"online": true, "frames": 3}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter, err := NewHTTP(server.URL, 100, "immediate", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Publish(context.Background(), []byte("png"), 0); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetScreen(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Online || published != 1 || screenCommands != 1 || hold != "0" {
		t.Fatalf("status=%+v published=%d screen=%d hold=%q", status, published, screenCommands, hold)
	}
}

func TestHTTPAdapterBufferedStreamContract(t *testing.T) {
	var paths []string
	var source, hold string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/api/stream/frame":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm() error = %v", err)
			}
			source = request.FormValue("source")
			hold = request.FormValue("seconds")
			file, _, err := request.FormFile("file")
			if err != nil {
				t.Errorf("FormFile() error = %v", err)
				return
			}
			defer file.Close()
			if payload, _ := io.ReadAll(file); string(payload) != "png" {
				t.Errorf("payload = %q", payload)
			}
		case "/api/stream/flush":
			var body struct {
				Source  string `json:"source"`
				Seconds int64  `json:"seconds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode flush: %v", err)
			}
			if body.Source != "pixel-steward" || body.Seconds != 5 {
				t.Errorf("flush = %+v", body)
			}
		default:
			http.NotFound(response, request)
			return
		}
		response.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	adapter, err := NewHTTP(server.URL, 100, "buffered_stream", "pixel-steward")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Publish(context.Background(), []byte("png"), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/api/stream/frame" || paths[1] != "/api/stream/flush" || source != "pixel-steward" || hold != "5" {
		t.Fatalf("paths=%q source=%q hold=%q", paths, source, hold)
	}
}

func TestHTTPStatusUnderstandsScreenOff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write([]byte(`{"device":{"online":true},"screen_off":true}`))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, 1, "immediate", "")
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ScreenOn {
		t.Fatal("screen_off was reported as on")
	}
}

// TestHTTPStatusPrefersAuthoritativeErrorTime covers the misreport that started
// this: the proxy has been serving the same fault for hours and knows when it
// began, but the adapter used to stamp it with the first poll that saw it and
// present a stale outage as fresh.
func TestHTTPStatusPrefersAuthoritativeErrorTime(t *testing.T) {
	began := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{"device": map[string]any{
			"online":        false,
			"last_error":    "panel refused frame",
			"last_error_at": began.Format(time.RFC3339Nano),
		}})
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, 1, "immediate", "")
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastErrorAt == nil {
		t.Fatal("authoritative last_error_at was dropped")
	}
	if !status.LastErrorAt.Equal(began) {
		t.Fatalf("LastErrorAt = %s, want authoritative %s", status.LastErrorAt, began)
	}
	if status.LastError != "panel refused frame" {
		t.Fatalf("LastError = %q", status.LastError)
	}
	if status.CheckedAt.IsZero() {
		t.Fatal("CheckedAt was not set")
	}

	// A second poll must not let the first-observed fallback displace the
	// authoritative value either.
	again, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.LastErrorAt == nil || !again.LastErrorAt.Equal(began) {
		t.Fatalf("second poll LastErrorAt = %v, want %s", again.LastErrorAt, began)
	}
}

func TestHTTPStatusFallsBackWhenErrorTimeAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"device":{"online":false,"last_error":"panel refused frame"}}`))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, 1, "immediate", "")
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastErrorAt == nil {
		t.Fatal("fallback timestamp was not applied")
	}
	if status.LastErrorAt.Before(before.Add(-2*time.Second)) || status.LastErrorAt.After(time.Now().UTC().Add(2*time.Second)) {
		t.Fatalf("fallback LastErrorAt = %s, want approximately %s", status.LastErrorAt, before)
	}
	if status.CheckedAt.IsZero() {
		t.Fatal("CheckedAt was not set")
	}
}

func TestHTTPStatusWithoutErrorHasNoErrorTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"device":{"online":true,"frames":12}}`))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, 1, "immediate", "")
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.LastError != "" || status.LastErrorAt != nil {
		t.Fatalf("healthy device reported error %q at %v", status.LastError, status.LastErrorAt)
	}
	if status.CheckedAt.IsZero() {
		t.Fatal("CheckedAt was not set")
	}
}

// TestHTTPStatusFallbackResetsBetweenErrors pins the bookkeeping: once the fault
// clears, a later different fault must be aged from when it appeared, not from
// the first sighting of the earlier one.
func TestHTTPStatusFallbackResetsBetweenErrors(t *testing.T) {
	var mu sync.Mutex
	body := `{"device":{"online":false,"last_error":"panel refused frame"}}`
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, 1, "immediate", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.LastErrorAt == nil {
		t.Fatal("first fault had no fallback timestamp")
	}

	mu.Lock()
	body = `{"device":{"online":true}}`
	mu.Unlock()
	cleared, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cleared.LastErrorAt != nil {
		t.Fatalf("cleared fault kept timestamp %s", cleared.LastErrorAt)
	}

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	body = `{"device":{"online":false,"last_error":"proxy unreachable"}}`
	mu.Unlock()
	second, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.LastErrorAt == nil {
		t.Fatal("recurring fault had no fallback timestamp")
	}
	if !second.LastErrorAt.After(*first.LastErrorAt) {
		t.Fatalf("second fault reused the first timestamp: %s vs %s", second.LastErrorAt, first.LastErrorAt)
	}
	if second.LastError != "proxy unreachable" {
		t.Fatalf("LastError = %q", second.LastError)
	}
}
