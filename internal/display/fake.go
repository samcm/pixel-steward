package display

import (
	"bytes"
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

type Fake struct {
	mu sync.RWMutex

	status   Status
	lastPNG  []byte
	lastHash [sha256.Size]byte
	hasFrame bool
}

func NewFake() *Fake {
	return &Fake{status: Status{Online: true, ScreenOn: true}}
}

func (f *Fake) Publish(_ context.Context, png []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	digest := sha256.Sum256(png)
	if f.hasFrame && digest == f.lastHash {
		f.status.Skipped++
		return nil
	}
	f.lastPNG = bytes.Clone(png)
	f.lastHash = digest
	f.hasFrame = true
	f.status.ScreenOn = true
	f.status.Frames++
	f.status.LastFrameAt = time.Now().UTC()
	f.status.LastError = ""

	return nil
}

func (f *Fake) SetScreen(_ context.Context, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.ScreenOn = on

	return nil
}

func (f *Fake) Status(_ context.Context) (Status, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return f.status, nil
}

func (f *Fake) LastPNG() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()

	return bytes.Clone(f.lastPNG)
}
