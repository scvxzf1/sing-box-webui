package singbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxCommandOutput = 64 << 10

type Client struct {
	binary string
}

func (c *Client) BinaryPath() string {
	if c == nil {
		return ""
	}
	return c.binary
}

type CommandError struct {
	Operation string
	Output    string
	Err       error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("sing-box %s failed: %v", e.Operation, e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

func NewClient(binary string) (*Client, error) {
	if !filepath.IsAbs(binary) {
		return nil, fmt.Errorf("sing-box path must be absolute")
	}
	absolute := filepath.Clean(binary)
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect sing-box binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("sing-box path is not an executable regular file")
	}
	return &Client{binary: absolute}, nil
}

func (c *Client) Version(ctx context.Context) (string, error) {
	output, err := c.run(ctx, "version", "version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output), nil
}

func (c *Client) Check(ctx context.Context, configPath string) error {
	absolute, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	_, err = c.run(ctx, "check", "check", "-c", absolute)
	return err
}

func (c *Client) run(ctx context.Context, operation string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, c.binary, arguments...)
	output := &limitedBuffer{limit: maxCommandOutput}
	command.Stdout = output
	command.Stderr = output

	if err := command.Run(); err != nil {
		return "", &CommandError{Operation: operation, Output: output.String(), Err: err}
	}
	return output.String(), nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
			b.truncated = true
		}
		_, _ = b.buffer.Write(data)
	} else {
		b.truncated = true
	}
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	if b.truncated {
		return b.buffer.String() + "\n[output truncated]"
	}
	return b.buffer.String()
}
