package executor

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrDisabled = errors.New("sandbox execution is disabled")

type Result struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int64  `json:"duration_ms"`
}

type Executor interface {
	Exec(context.Context, string, string, time.Duration) (Result, error)
	ReadFile(context.Context, string, string) (io.ReadCloser, string, error)
	Suspend(context.Context, string) error
	Resume(context.Context, string) error
	Destroy(context.Context, string) error
}

type Disabled struct{}

func (Disabled) Exec(context.Context, string, string, time.Duration) (Result, error) {
	return Result{}, ErrDisabled
}
func (Disabled) ReadFile(context.Context, string, string) (io.ReadCloser, string, error) {
	return nil, "", ErrDisabled
}
func (Disabled) Suspend(context.Context, string) error { return nil }
func (Disabled) Resume(context.Context, string) error  { return nil }
func (Disabled) Destroy(context.Context, string) error { return nil }
