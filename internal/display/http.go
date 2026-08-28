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

	mu          sync.Mutex
	lastSend    time.Time
	minimum     time.Duration
	lastError   string
	lastErrorAt time.Time
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
	_, _ = io.WriteString(seconds, strconv.FormatInt(max(0, int64(hold.Seconds())), 10))
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
			Online      bool      `json:"online"`
			LastError   string    `json:"last_error"`
			LastErrorAt time.Time `json:"last_error_at"`
			Frames      uint64    `json:"frames"`
			Skipped     uint64    `json:"skipped"`
			LastOK      time.Time `json:"last_ok"`
		} `json:"device"`
		ScreenOn  *bool `json:"screen_on"`
		ScreenOff *bool `json:"screen_off"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&raw); err != nil {
		return Status{}, err
	}

	screenOn := true
	if raw.ScreenOn != nil {
		screenOn = *raw.ScreenOn
	} else if raw.ScreenOff != nil {
		screenOn = !*raw.ScreenOff
	}
	now := time.Now().UTC()
	status := Status{
		Online:      raw.Device.Online,
		ScreenOn:    screenOn,
		LastFrameAt: optionalTime(raw.Device.LastOK),
		LastError:   raw.Device.LastError,
		CheckedAt:   now,
		Frames:      raw.Device.Frames,
		Skipped:     raw.Device.Skipped,
	}
	// device.last_error_at is authoritative when the proxy supplies it: it is the
	// real age of the fault, which can be hours older than the first poll that
	// observed it. observeError still runs on every poll so its bookkeeping stays
	// honest -- an empty message clears the remembered error, so a later
	// recurrence earns a fresh fallback timestamp instead of inheriting the old
	// one. The fallback fills only a genuine gap.
	fallback := h.observeError(raw.Device.LastError, now)
	if authoritative := optionalTime(raw.Device.LastErrorAt); authoritative != nil && status.LastError != "" {
		status.LastErrorAt = authoritative
	} else {
		status.LastErrorAt = fallback
	}
	return status, nil
}

// optionalTime drops a zero timestamp so the operator surface can tell
// "never" apart from the epoch.
func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// observeError timestamps the first sighting of each distinct device error so
// the operator UI can age it. It is the fallback for proxies that report the
// error text without a device.last_error_at, and it runs on every poll to keep
// that fallback honest across clear-and-recur cycles.
func (h *HTTP) observeError(message string, now time.Time) *time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if message == "" {
		h.lastError, h.lastErrorAt = "", time.Time{}
		return nil
	}
	if message != h.lastError {
		h.lastError, h.lastErrorAt = message, now
	}
	at := h.lastErrorAt
	return &at
}
