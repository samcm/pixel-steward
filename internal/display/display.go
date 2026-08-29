package display

import (
	"context"
	"time"
)

// Status is the observed state of the display proxy and panel. CheckedAt and
// LastErrorAt exist so an operator can tell a live outage from a historic one:
// a LastError without a known age must never be presented as a current fault.
type Status struct {
	Online      bool       `json:"online"`
	ScreenOn    bool       `json:"screen_on"`
	LastFrameAt *time.Time `json:"last_frame_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
	CheckedAt   time.Time  `json:"checked_at,omitempty"`
	Frames      uint64     `json:"frames"`
	Skipped     uint64     `json:"skipped"`
}

type Display interface {
	Publish(context.Context, []byte, string, time.Duration) error
	SetScreen(context.Context, bool) error
	Status(context.Context) (Status, error)
}
