package objectstore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Filesystem struct {
	root string
}

func NewFilesystem(root string) (*Filesystem, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}

	return &Filesystem{root: absolute}, nil
}

func (f *Filesystem) Put(ctx context.Context, key, contentType string, source io.Reader) (Object, error) {
	path, err := f.path(key)
	if err != nil {
		return Object{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Object{}, err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".pixel-steward-object-*")
	if err != nil {
		return Object{}, err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	written, err := copyContext(ctx, temporary, source)
	if err != nil {
		return Object{}, err
	}
	if err := temporary.Sync(); err != nil {
		return Object{}, err
	}
	if err := temporary.Close(); err != nil {
		return Object{}, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return Object{}, err
	}
	committed = true

	return Object{Key: key, Size: written, ContentType: contentType, CreatedAt: time.Now().UTC()}, nil
}

func (f *Filesystem) Get(_ context.Context, key string) (io.ReadCloser, Object, error) {
	path, err := f.path(key)
	if err != nil {
		return nil, Object{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Object{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, Object{}, err
	}

	return file, Object{Key: key, Size: info.Size(), CreatedAt: info.ModTime()}, nil
}

func (f *Filesystem) path(key string) (string, error) {
	if key == "" || filepath.IsAbs(key) || strings.ContainsRune(key, '\x00') {
		return "", fmt.Errorf("invalid object key")
	}
	cleaned := filepath.Clean(filepath.FromSlash(key))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("object key escapes storage root")
	}
	path := filepath.Join(f.root, cleaned)
	if path != f.root && !strings.HasPrefix(path, f.root+string(filepath.Separator)) {
		return "", fmt.Errorf("object key escapes storage root")
	}

	return path, nil
}

func copyContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			count, writeErr := destination.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return written, writeErr
			}
			if count != read {
				return written, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}
