package trafficpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sing-box-webui/internal/control"
	"sing-box-webui/internal/events"
	"sing-box-webui/internal/nodepool"
	"sing-box-webui/internal/supervisor"
)

const (
	StateDisabled   = "disabled"
	StateWaiting    = "waiting"
	StateMonitoring = "monitoring"
	StateTriggering = "triggering"
	StateActive     = "active"
	StateRecovering = "recovering"
	StateCooldown   = "cooldown"
	StateError      = "error"
	maxEvents       = 32
)

type Config struct {
	Enabled                   bool   `json:"enabled"`
	DownloadPoolID            string `json:"downloadPoolId"`
	TriggerRateBytesPerSecond int64  `json:"triggerRateBytesPerSecond"`
	TriggerDurationSeconds    int    `json:"triggerDurationSeconds"`
	ReleaseRateBytesPerSecond int64  `json:"releaseRateBytesPerSecond"`
	ReleaseDurationSeconds    int    `json:"releaseDurationSeconds"`
	CooldownSeconds           int    `json:"cooldownSeconds"`
}

type UpdateInput = Config

type Event struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
}

type Snapshot struct {
	Config
	State                  string     `json:"state"`
	CurrentDownloadBPS     int64      `json:"currentDownloadBps"`
	ActiveConnections      int        `json:"activeConnections"`
	TriggerProgressSeconds int        `json:"triggerProgressSeconds"`
	ReleaseProgressSeconds int        `json:"releaseProgressSeconds"`
	OriginalPoolID         string     `json:"originalPoolId,omitempty"`
	OriginalPoolName       string     `json:"originalPoolName,omitempty"`
	ActivatedAt            *time.Time `json:"activatedAt,omitempty"`
	CooldownUntil          *time.Time `json:"cooldownUntil,omitempty"`
	LastError              string     `json:"lastError,omitempty"`
	Events                 []Event    `json:"events"`
}

type RuntimeController interface {
	Status(context.Context) control.Runtime
	Apply(context.Context, control.ApplyInput) (control.Runtime, error)
}

type PoolLookup interface {
	Get(string) (nodepool.View, error)
}

type Manager struct {
	mu         sync.RWMutex
	path       string
	control    RuntimeController
	pools      PoolLookup
	events     *events.Broker
	config     Config
	snapshot   Snapshot
	original   control.ApplyInput
	lastTotal  int64
	lastSample time.Time
	aboveSince time.Time
	belowSince time.Time
}

func Open(dataDirectory string, controller RuntimeController, pools PoolLookup, broker *events.Broker) (*Manager, error) {
	if controller == nil || pools == nil {
		return nil, fmt.Errorf("traffic policy requires runtime control and node pools")
	}
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{
		path: filepath.Join(dataDirectory, "traffic-policy.json"), control: controller, pools: pools, events: broker,
		config: defaultConfig(), snapshot: Snapshot{State: StateDisabled, Events: []Event{}},
	}
	content, err := os.ReadFile(m.path)
	if err == nil {
		if err := json.Unmarshal(content, &m.config); err != nil {
			return nil, fmt.Errorf("decode traffic policy: %w", err)
		}
		if err := validate(m.config, pools); err != nil {
			return nil, fmt.Errorf("validate traffic policy: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	m.snapshot.Config = m.config
	if m.config.Enabled {
		m.snapshot.State = StateWaiting
	}
	return m, nil
}

func defaultConfig() Config {
	return Config{TriggerRateBytesPerSecond: 5 << 20, TriggerDurationSeconds: 5, ReleaseRateBytesPerSecond: 1 << 20, ReleaseDurationSeconds: 60, CooldownSeconds: 600}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.tick(ctx, now.UTC())
		}
	}
}

func (m *Manager) Get() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := m.snapshot
	result.Events = append([]Event{}, m.snapshot.Events...)
	return result
}

func (m *Manager) Update(ctx context.Context, input UpdateInput) (Snapshot, error) {
	if err := validate(input, m.pools); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	wasActive := m.snapshot.State == StateActive
	if wasActive && input.Enabled {
		m.mu.Unlock()
		return Snapshot{}, fmt.Errorf("下载池接管期间只能停用策略，不能修改配置")
	}
	original := m.original
	previous := m.config
	m.config = input
	if err := m.persistLocked(); err != nil {
		m.config = previous
		m.mu.Unlock()
		return Snapshot{}, err
	}
	if !wasActive {
		m.resetSamplesLocked()
	}
	m.snapshot.Config = input
	m.snapshot.LastError = ""
	if wasActive {
		m.snapshot.State = StateRecovering
	} else if input.Enabled {
		m.snapshot.State = StateWaiting
	} else {
		m.snapshot.State = StateDisabled
	}
	m.addEventLocked("configuration", "流量策略配置已更新")
	m.mu.Unlock()
	if wasActive {
		if _, err := m.control.Apply(ctx, original); err != nil {
			m.mu.Lock()
			m.failLocked("停用策略时恢复原代理池失败", err)
			result := m.cloneSnapshotLocked()
			m.mu.Unlock()
			return result, err
		}
		m.mu.Lock()
		m.snapshot.State = StateDisabled
		m.snapshot.ActivatedAt = nil
		m.snapshot.OriginalPoolID, m.snapshot.OriginalPoolName = "", ""
		m.resetSamplesLocked()
		m.addEventLocked("recovered", "策略已停用，已恢复原代理池")
		m.mu.Unlock()
	}
	m.publish("traffic-policy.updated", map[string]any{"enabled": input.Enabled})
	return m.Get(), nil
}

func (m *Manager) tick(ctx context.Context, now time.Time) {
	runtime := m.control.Status(ctx)
	m.mu.Lock()
	if !m.config.Enabled {
		m.snapshot.State = StateDisabled
		m.mu.Unlock()
		return
	}
	if m.snapshot.CooldownUntil != nil && now.Before(*m.snapshot.CooldownUntil) {
		m.snapshot.State = StateCooldown
		m.mu.Unlock()
		return
	}
	if m.snapshot.State == StateCooldown {
		m.snapshot.CooldownUntil = nil
	}
	if runtime.State != supervisor.StateRunning || runtime.TargetType != "pool" || runtime.PoolHealth == nil || runtime.PoolHealth.TrafficAt.IsZero() {
		m.snapshot.State = StateWaiting
		m.snapshot.CurrentDownloadBPS = 0
		m.resetSamplesLocked()
		m.mu.Unlock()
		return
	}
	if m.snapshot.State == StateActive && runtime.PoolID != m.config.DownloadPoolID {
		m.addEventLocked("cancelled", "运行目标已被手动更改，自动下载状态已结束")
		m.finishCycleLocked(now)
		m.mu.Unlock()
		return
	}
	total, sampleAt := runtime.PoolHealth.DownloadTotal, runtime.PoolHealth.TrafficAt
	if m.lastSample.IsZero() || !sampleAt.After(m.lastSample) || total < m.lastTotal {
		m.lastTotal, m.lastSample = total, sampleAt
		m.snapshot.ActiveConnections = runtime.PoolHealth.Connections
		if m.snapshot.State != StateActive {
			m.snapshot.State = StateMonitoring
		}
		m.mu.Unlock()
		return
	}
	elapsed := sampleAt.Sub(m.lastSample)
	rate := int64(float64(total-m.lastTotal) / elapsed.Seconds())
	m.lastTotal, m.lastSample = total, sampleAt
	m.snapshot.CurrentDownloadBPS = rate
	m.snapshot.ActiveConnections = runtime.PoolHealth.Connections
	if m.snapshot.State == StateActive {
		if rate <= m.config.ReleaseRateBytesPerSecond {
			if m.belowSince.IsZero() {
				m.belowSince = now
			}
			m.snapshot.ReleaseProgressSeconds = int(now.Sub(m.belowSince).Seconds())
		} else {
			m.belowSince = time.Time{}
			m.snapshot.ReleaseProgressSeconds = 0
		}
		if m.snapshot.ReleaseProgressSeconds >= m.config.ReleaseDurationSeconds {
			original := m.original
			m.snapshot.State = StateRecovering
			m.mu.Unlock()
			_, err := m.control.Apply(ctx, original)
			m.mu.Lock()
			if err != nil {
				m.failLocked("恢复原代理池失败", err)
				m.mu.Unlock()
				return
			}
			m.addEventLocked("recovered", "下载流量已回落，已恢复原代理池")
			m.finishCycleLocked(now)
		}
		m.mu.Unlock()
		return
	}
	m.snapshot.State = StateMonitoring
	if runtime.PoolID == m.config.DownloadPoolID {
		m.aboveSince = time.Time{}
		m.snapshot.TriggerProgressSeconds = 0
		m.mu.Unlock()
		return
	}
	if rate >= m.config.TriggerRateBytesPerSecond {
		if m.aboveSince.IsZero() {
			m.aboveSince = now
		}
		m.snapshot.TriggerProgressSeconds = int(now.Sub(m.aboveSince).Seconds())
	} else {
		m.aboveSince = time.Time{}
		m.snapshot.TriggerProgressSeconds = 0
	}
	if m.snapshot.TriggerProgressSeconds < m.config.TriggerDurationSeconds {
		m.mu.Unlock()
		return
	}
	m.original = control.ApplyInput{Mode: runtime.Mode, PoolID: runtime.PoolID}
	m.snapshot.OriginalPoolID, m.snapshot.OriginalPoolName = runtime.PoolID, runtime.PoolName
	m.snapshot.State = StateTriggering
	downloadPoolID := m.config.DownloadPoolID
	m.mu.Unlock()
	_, err := m.control.Apply(ctx, control.ApplyInput{Mode: runtime.Mode, PoolID: downloadPoolID})
	m.mu.Lock()
	if err != nil {
		original := m.original
		m.snapshot.State = StateRecovering
		m.mu.Unlock()
		_, rollbackErr := m.control.Apply(ctx, original)
		m.mu.Lock()
		if rollbackErr != nil {
			m.failLocked("切换下载代理池失败，且无法恢复原代理池", fmt.Errorf("切换失败: %v; 回滚失败: %w", err, rollbackErr))
		} else {
			m.addEventLocked("error", "切换下载代理池失败，已恢复原代理池："+err.Error())
			m.finishCycleLocked(now)
		}
		m.mu.Unlock()
		return
	}
	m.snapshot.State, m.snapshot.ActivatedAt = StateActive, &now
	m.snapshot.TriggerProgressSeconds = 0
	m.resetRateLocked()
	m.addEventLocked("activated", "检测到持续大流量，已切换到下载代理池")
	m.mu.Unlock()
	m.publish("traffic-policy.activated", map[string]any{"downloadPoolId": downloadPoolID})
}

func validate(config Config, pools PoolLookup) error {
	if config.TriggerRateBytesPerSecond < 64<<10 || config.TriggerRateBytesPerSecond > 10<<30 {
		return fmt.Errorf("触发速率必须在 64 KiB/s 到 10 GiB/s 之间")
	}
	if config.ReleaseRateBytesPerSecond < 0 || config.ReleaseRateBytesPerSecond >= config.TriggerRateBytesPerSecond {
		return fmt.Errorf("回落速率必须低于触发速率")
	}
	if config.TriggerDurationSeconds < 2 || config.TriggerDurationSeconds > 300 || config.ReleaseDurationSeconds < 5 || config.ReleaseDurationSeconds > 3600 || config.CooldownSeconds < 0 || config.CooldownSeconds > 86400 {
		return fmt.Errorf("时间参数超出允许范围")
	}
	if config.Enabled {
		pool, err := pools.Get(config.DownloadPoolID)
		if err != nil {
			return fmt.Errorf("下载代理池不存在")
		}
		if pool.AvailableCount < 2 {
			return fmt.Errorf("下载代理池至少需要 2 个可用节点")
		}
	}
	return nil
}

func (m *Manager) persistLocked() error {
	content, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".traffic-policy-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, m.path)
}

func (m *Manager) resetSamplesLocked() {
	m.resetRateLocked()
	m.original = control.ApplyInput{}
	m.snapshot.TriggerProgressSeconds = 0
	m.snapshot.ReleaseProgressSeconds = 0
}
func (m *Manager) resetRateLocked() {
	m.lastTotal = 0
	m.lastSample = time.Time{}
	m.aboveSince = time.Time{}
	m.belowSince = time.Time{}
}
func (m *Manager) finishCycleLocked(now time.Time) {
	m.snapshot.State = StateCooldown
	cooldownUntil := now.Add(time.Duration(m.config.CooldownSeconds) * time.Second)
	m.snapshot.CooldownUntil = &cooldownUntil
	m.snapshot.ActivatedAt = nil
	m.snapshot.OriginalPoolID = ""
	m.snapshot.OriginalPoolName = ""
	m.resetSamplesLocked()
}
func (m *Manager) failLocked(message string, err error) {
	m.snapshot.State = StateError
	m.snapshot.LastError = err.Error()
	m.addEventLocked("error", message+"："+err.Error())
	m.resetRateLocked()
}
func (m *Manager) addEventLocked(kind, message string) {
	m.snapshot.Events = append([]Event{{Timestamp: time.Now().UTC(), Type: kind, Message: message}}, m.snapshot.Events...)
	if len(m.snapshot.Events) > maxEvents {
		m.snapshot.Events = m.snapshot.Events[:maxEvents]
	}
}
func (m *Manager) cloneSnapshotLocked() Snapshot {
	result := m.snapshot
	result.Events = append([]Event{}, result.Events...)
	return result
}
func (m *Manager) publish(kind string, payload any) {
	if m.events != nil {
		_, _ = m.events.Publish(kind, payload)
	}
}
