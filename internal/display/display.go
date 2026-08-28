package display

import (
	"context"
	"time"
)

type Status struct {
	Online      bool      `json:"online"`
	ScreenOn    bool      `json:"screen_on"`
	LastFrameAt time.Time `json:"last_frame_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Frames      uint64    `json:"frames"`
	Skipped     uint64    `json:"skipped"`
}

type Display interface {
	Publish(context.Context, []byte, time.Duration) error
	SetScreen(context.Context, bool) error
	Status(context.Context) (Status, error)
}
