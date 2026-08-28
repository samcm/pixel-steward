package display

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HTTP publishes through a trusted display daemon implementing Pixel Steward's
// small adapter contract. The daemon, not this adapter, owns device-specific
// transport behavior.
type HTTP struct {
	baseURL string
	client  *http.Client

	mu       sync.Mutex
	lastSend time.Time
	minimum  time.Duration
}

func NewHTTP(baseURL string, maxFPS float64) (*HTTP, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("display base URL must be an absolute HTTP URL")
	}
	if maxFPS <= 0 {
		return nil, fmt.Errorf("max FPS must be greater than zero")
	}

	return &HTTP{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 15 * time.Second},
		minimum: time.Duration(float64(time.Second) / maxFPS),
	}, nil
}

func (h *HTTP) Publish(ctx context.Context, png []byte, hold time.Duration) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if wait := h.minimum - time.Since(h.lastSend); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	seconds, err := writer.CreateFormField("seconds")
	if err != nil {
		return err
	}
	_, _ = io.WriteString(seconds, strconv.FormatInt(max(1, int64(hold.Seconds())), 10))
	file, err := writer.CreateFormFile("file", "frame.png")
	if err != nil {
		return err
	}
	if _, err := file.Write(png); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/api/image", &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := h.client.Do(request)
	h.lastSend = time.Now()
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("display publish returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}

	return nil
}

func (h *HTTP) SetScreen(ctx context.Context, on bool) error {
	body, _ := json.Marshal(map[string]bool{"on": on})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/api/screen", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("display screen command returned %s", response.Status)
	}

	return nil
}

func (h *HTTP) Status(ctx context.Context) (Status, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+"/api/status", nil)
	if err != nil {
		return Status{}, err
	}
	response, err := h.client.Do(request)
	if err != nil {
		return Status{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Status{}, fmt.Errorf("display status returned %s", response.Status)
	}

	var raw struct {
		Device struct {
			Online    bool      `json:"online"`
			LastError string    `json:"last_error"`
			Frames    uint64    `json:"frames"`
			Skipped   uint64    `json:"skipped"`
			LastOK    time.Time `json:"last_ok"`
		} `json:"device"`
		ScreenOn bool `json:"screen_on"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&raw); err != nil {
		return Status{}, err
	}

	return Status{
		Online:      raw.Device.Online,
		ScreenOn:    raw.ScreenOn,
		LastFrameAt: raw.Device.LastOK,
		LastError:   raw.Device.LastError,
		Frames:      raw.Device.Frames,
		Skipped:     raw.Device.Skipped,
	}, nil
}
