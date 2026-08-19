//go:build linux

package systemproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Controller interface {
	Available(context.Context) (bool, string)
	Apply(context.Context, string, uint16) error
	Restore(context.Context) error
}

type GNOMEController struct {
	mu           sync.Mutex
	binary       string
	snapshotPath string
}

type setting struct {
	Schema string `json:"schema"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

var managedSettings = [][2]string{
	{"org.gnome.system.proxy", "mode"},
	{"org.gnome.system.proxy", "ignore-hosts"},
	{"org.gnome.system.proxy.http", "host"},
	{"org.gnome.system.proxy.http", "port"},
	{"org.gnome.system.proxy.https", "host"},
	{"org.gnome.system.proxy.https", "port"},
	{"org.gnome.system.proxy.socks", "host"},
	{"org.gnome.system.proxy.socks", "port"},
}

func NewGNOMEController(stateDirectory string) *GNOMEController {
	binary, _ := exec.LookPath("gsettings")
	return &GNOMEController{
		binary:       binary,
		snapshotPath: filepath.Join(stateDirectory, "system-proxy-snapshot.json"),
	}
}

func (c *GNOMEController) Available(ctx context.Context) (bool, string) {
	if c.binary == "" {
		return false, "未找到 gsettings，当前桌面环境不支持自动设置系统代理"
	}
	output, err := exec.CommandContext(ctx, c.binary, "writable", "org.gnome.system.proxy", "mode").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "true" {
		return false, "GNOME 系统代理设置不可写"
	}
	return true, "GNOME 系统代理可用"
}

func (c *GNOMEController) Apply(ctx context.Context, host string, port uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if available, detail := c.Available(ctx); !available {
		return fmt.Errorf("%s", detail)
	}
	if err := os.MkdirAll(filepath.Dir(c.snapshotPath), 0o700); err != nil {
		return fmt.Errorf("create proxy state directory: %w", err)
	}
	if _, err := os.Stat(c.snapshotPath); errors.Is(err, os.ErrNotExist) {
		if err := c.captureSnapshot(ctx); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("inspect system proxy snapshot: %w", err)
	}

	portValue := strconv.FormatUint(uint64(port), 10)
	changes := []setting{
		{Schema: "org.gnome.system.proxy.http", Key: "host", Value: host},
		{Schema: "org.gnome.system.proxy.http", Key: "port", Value: portValue},
		{Schema: "org.gnome.system.proxy.https", Key: "host", Value: host},
		{Schema: "org.gnome.system.proxy.https", Key: "port", Value: portValue},
		{Schema: "org.gnome.system.proxy.socks", Key: "host", Value: host},
		{Schema: "org.gnome.system.proxy.socks", Key: "port", Value: portValue},
		{Schema: "org.gnome.system.proxy", Key: "ignore-hosts", Value: "['localhost', '127.0.0.0/8', '::1', '192.168.0.0/16', '10.0.0.0/8', '172.16.0.0/12', '*.local']"},
		{Schema: "org.gnome.system.proxy", Key: "mode", Value: "manual"},
	}
	for _, change := range changes {
		if err := c.set(ctx, change); err != nil {
			_ = c.restoreLocked(context.Background())
			return err
		}
	}
	return nil
}

func (c *GNOMEController) Restore(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restoreLocked(ctx)
}

func (c *GNOMEController) captureSnapshot(ctx context.Context) error {
	snapshot := make([]setting, 0, len(managedSettings))
	for _, key := range managedSettings {
		output, err := exec.CommandContext(ctx, c.binary, "get", key[0], key[1]).CombinedOutput()
		if err != nil {
			return fmt.Errorf("read system proxy setting %s.%s: %w", key[0], key[1], err)
		}
		snapshot = append(snapshot, setting{Schema: key[0], Key: key[1], Value: strings.TrimSpace(string(output))})
	}
	content, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode system proxy snapshot: %w", err)
	}
	if err := os.WriteFile(c.snapshotPath, content, 0o600); err != nil {
		return fmt.Errorf("write system proxy snapshot: %w", err)
	}
	return os.Chmod(c.snapshotPath, 0o600)
}

func (c *GNOMEController) restoreLocked(ctx context.Context) error {
	content, err := os.ReadFile(c.snapshotPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read system proxy snapshot: %w", err)
	}
	var snapshot []setting
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return fmt.Errorf("parse system proxy snapshot: %w", err)
	}
	for _, original := range snapshot {
		if err := c.set(ctx, original); err != nil {
			return fmt.Errorf("restore system proxy: %w", err)
		}
	}
	if err := os.Remove(c.snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove system proxy snapshot: %w", err)
	}
	return nil
}

func (c *GNOMEController) set(ctx context.Context, value setting) error {
	output, err := exec.CommandContext(ctx, c.binary, "set", value.Schema, value.Key, value.Value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("set system proxy %s.%s: %w: %s", value.Schema, value.Key, err, strings.TrimSpace(string(output)))
	}
	return nil
}
