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

	adapter, err := NewHTTP(server.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Publish(context.Background(), []byte("png"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetScreen(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	status, err := adapter.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Online || published != 1 || screenCommands != 1 {
		t.Fatalf("status=%+v published=%d screen=%d", status, published, screenCommands)
	}
}
