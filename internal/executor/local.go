package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Local struct {
	root       string
	maxRuntime time.Duration
}

func NewLocal(root string, maxRuntime time.Duration) (*Local, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &Local{root: absolute, maxRuntime: maxRuntime}, nil
}

func (l *Local) Exec(parent context.Context, leaseID, command string, timeout time.Duration) (Result, error) {
	if timeout <= 0 || timeout > l.maxRuntime {
		timeout = l.maxRuntime
	}
	directory, err := l.directory(leaseID)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	process := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	process.Dir = directory
	process.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + directory, "LANG=C.UTF-8"}
	var stdout, stderr bytes.Buffer
	process.Stdout = limitWriter{destination: &stdout, remaining: 1 << 20}
	process.Stderr = limitWriter{destination: &stderr, remaining: 1 << 20}
	err = process.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started).Milliseconds()}
	if exitErr := new(exec.ExitError); errorsAs(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (l *Local) ReadFile(_ context.Context, leaseID, name string) (io.ReadCloser, string, error) {
	directory, err := l.directory(leaseID)
	if err != nil {
		return nil, "", err
	}
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("file escapes sandbox workspace")
	}
	path := filepath.Join(directory, cleaned)
	if !strings.HasPrefix(path, directory+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("file escapes sandbox workspace")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	return file, contentType(path), nil
}

func (l *Local) Suspend(context.Context, string) error { return nil }
func (l *Local) Resume(context.Context, string) error  { return nil }
func (l *Local) Destroy(_ context.Context, leaseID string) error {
	_, err := l.directory(leaseID)
	return err
}

func (l *Local) directory(leaseID string) (string, error) {
	if leaseID == "" || strings.ContainsAny(leaseID, `/\\\x00`) || strings.Contains(leaseID, "..") {
		return "", fmt.Errorf("invalid lease ID")
	}
	return filepath.Join(l.root, leaseID), nil
}

func contentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

type limitWriter struct {
	destination io.Writer
	remaining   int64
}

func (w limitWriter) Write(value []byte) (int, error) {
	original := len(value)
	if int64(len(value)) > w.remaining {
		value = value[:w.remaining]
	}
	if len(value) > 0 {
		_, _ = w.destination.Write(value)
	}
	return original, nil
}

func errorsAs(err error, target any) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}
