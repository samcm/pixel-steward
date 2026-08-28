package objectstore

import (
	"context"
	"io"
	"time"
)

type Object struct {
	Key         string    `json:"key"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store interface {
	Put(context.Context, string, string, io.Reader) (Object, error)
	Get(context.Context, string) (io.ReadCloser, Object, error)
}
