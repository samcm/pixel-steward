package executor

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Command struct {
	execCommand    []string
	readCommand    []string
	suspendCommand []string
	resumeCommand  []string
	resetCommand   []string
	maxRuntime     time.Duration
}

type CommandConfig struct {
	ExecCommand    []string
	ReadCommand    []string
	SuspendCommand []string
	ResumeCommand  []string
	ResetCommand   []string
	MaxRuntime     time.Duration
}

func NewCommand(config CommandConfig) (*Command, error) {
	if len(config.ExecCommand) == 0 || len(config.ReadCommand) == 0 || len(config.SuspendCommand) == 0 || len(config.ResumeCommand) == 0 || len(config.ResetCommand) == 0 {
		return nil, fmt.Errorf("all command executor templates are required")
	}
	return &Command{execCommand: config.ExecCommand, readCommand: config.ReadCommand,
		suspendCommand: config.SuspendCommand, resumeCommand: config.ResumeCommand, resetCommand: config.ResetCommand,
		maxRuntime: config.MaxRuntime}, nil
}

func (c *Command) Exec(parent context.Context, leaseID, script string, timeout time.Duration) (Result, error) {
	if timeout <= 0 || timeout > c.maxRuntime {
		timeout = c.maxRuntime
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	started := time.Now()
	command := commandFromTemplate(ctx, c.execCommand, map[string]string{
		"lease_id": leaseID, "command_b64": base64.RawStdEncoding.EncodeToString([]byte(script)),
		"timeout_seconds": strconv.FormatInt(max(1, int64(timeout.Seconds())), 10),
	})
	var stdout, stderr bytes.Buffer
	command.Stdout = limitWriter{destination: &stdout, remaining: 1 << 20}
	command.Stderr = limitWriter{destination: &stderr, remaining: 1 << 20}
	err := command.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started).Milliseconds()}
	var exitErr *exec.ExitError
	if errorsAs(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

func (c *Command) ReadFile(ctx context.Context, leaseID, path string) (io.ReadCloser, string, error) {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "..") || strings.ContainsRune(path, '\x00') {
		return nil, "", fmt.Errorf("invalid sandbox path")
	}
	command := commandFromTemplate(ctx, c.readCommand, map[string]string{
		"lease_id": leaseID, "path_b64": base64.RawStdEncoding.EncodeToString([]byte(path)),
	})
	data, err := command.Output()
	if err != nil {
		return nil, "", err
	}
	if len(data) > 32<<20 {
		return nil, "", fmt.Errorf("sandbox file exceeds 32 MiB")
	}
	return io.NopCloser(bytes.NewReader(data)), contentType(path), nil
}

func (c *Command) Suspend(ctx context.Context, leaseID string) error {
	return commandFromTemplate(ctx, c.suspendCommand, map[string]string{"lease_id": leaseID}).Run()
}

func (c *Command) Resume(ctx context.Context, leaseID string) error {
	result, err := commandFromTemplate(ctx, c.resumeCommand, map[string]string{"lease_id": leaseID}).CombinedOutput()
	if err != nil && !bytes.Contains(bytes.ToLower(result), []byte("not paused")) {
		return fmt.Errorf("resume sandbox: %w: %s", err, strings.TrimSpace(string(result)))
	}
	return nil
}

func (c *Command) Destroy(ctx context.Context, leaseID string) error {
	result, err := commandFromTemplate(ctx, c.resetCommand, map[string]string{"lease_id": leaseID}).CombinedOutput()
	if err != nil {
		return fmt.Errorf("reset sandbox: %w: %s", err, strings.TrimSpace(string(result)))
	}
	return nil
}

func commandFromTemplate(ctx context.Context, template []string, values map[string]string) *exec.Cmd {
	arguments := make([]string, len(template))
	for index, argument := range template {
		for key, value := range values {
			argument = strings.ReplaceAll(argument, "{"+key+"}", value)
		}
		arguments[index] = argument
	}
	return exec.CommandContext(ctx, arguments[0], arguments[1:]...)
}
