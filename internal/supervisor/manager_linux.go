//go:build linux

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	maxLogBytes  = 256 << 10
	startupGrace = 300 * time.Millisecond
)

type restartPolicy struct {
	maxRestarts  int
	initialDelay time.Duration
	maxDelay     time.Duration
	stableWindow time.Duration
	jitter       func(time.Duration) time.Duration
}

var defaultRestartPolicy = restartPolicy{
	maxRestarts:  3,
	initialDelay: 250 * time.Millisecond,
	maxDelay:     2 * time.Second,
	stableWindow: 30 * time.Second,
	jitter:       jitter20Percent,
}

var ErrAlreadyRunning = errors.New("sing-box is already running")

type Manager struct {
	mu         sync.RWMutex
	binary     string
	snapshot   Snapshot
	command    *exec.Cmd
	done       chan struct{}
	stopWanted bool
	logs       *ringBuffer

	configPath     string
	restartCount   int
	restartPolicy  restartPolicy
	processStarted time.Time
}

func NewManager(binary string) *Manager {
	return newManagerWithPolicy(binary, defaultRestartPolicy)
}

func newManagerWithPolicy(binary string, policy restartPolicy) *Manager {
	return &Manager{
		binary:        binary,
		snapshot:      Snapshot{State: StateStopped},
		logs:          newRingBuffer(maxLogBytes),
		restartPolicy: policy,
	}
}

func (m *Manager) Start(ctx context.Context, configPath string) (Snapshot, error) {
	m.mu.Lock()
	if m.snapshot.State == StateRunning || m.snapshot.State == StateStarting || m.snapshot.State == StateStopping {
		m.mu.Unlock()
		return Snapshot{}, ErrAlreadyRunning
	}
	m.snapshot.Generation++
	generation := m.snapshot.Generation
	m.snapshot.State = StateStarting
	m.snapshot.LastError = ""
	m.stopWanted = false
	m.configPath = configPath
	m.restartCount = 0
	m.done = make(chan struct{})
	if err := m.startProcessLocked(generation); err != nil {
		m.snapshot.State = StateFailed
		m.snapshot.LastError = err.Error()
		m.finishLocked()
		result := m.snapshot
		m.mu.Unlock()
		return result, fmt.Errorf("start sing-box: %w", err)
	}
	done := m.done
	m.mu.Unlock()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = m.Stop(stopCtx)
			return Snapshot{}, ctx.Err()
		case <-done:
			snapshot := m.Snapshot()
			if snapshot.State == StateFailed {
				return snapshot, fmt.Errorf("sing-box exited during startup: %s", snapshot.LastError)
			}
			return snapshot, nil
		case <-ticker.C:
			snapshot := m.Snapshot()
			if snapshot.State == StateRunning && time.Since(snapshot.StartedAt) >= startupGrace {
				return snapshot, nil
			}
		}
	}
}

func (m *Manager) Stop(ctx context.Context) (Snapshot, error) {
	m.mu.Lock()
	if m.snapshot.State == StateStopped {
		result := m.snapshot
		m.mu.Unlock()
		return result, nil
	}
	if m.command == nil || m.command.Process == nil {
		m.stopWanted = true
		m.snapshot.State = StateStopped
		m.snapshot.PID = 0
		m.snapshot.LastError = ""
		m.finishLocked()
		result := m.snapshot
		m.mu.Unlock()
		return result, nil
	}
	m.snapshot.State = StateStopping
	m.stopWanted = true
	pid := m.command.Process.Pid
	done := m.done
	m.mu.Unlock()

	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return m.Snapshot(), fmt.Errorf("signal sing-box process group: %w", err)
	}
	select {
	case <-done:
		return m.Snapshot(), nil
	case <-ctx.Done():
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return m.Snapshot(), fmt.Errorf("stop sing-box after timeout: %w", ctx.Err())
		}
		return m.Snapshot(), fmt.Errorf("stop sing-box: %w", ctx.Err())
	}
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (m *Manager) Logs() string {
	return m.logs.String()
}

func (m *Manager) startProcessLocked(generation uint64) error {
	command := exec.Command(m.binary, "run", "-c", m.configPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = m.logs
	command.Stderr = m.logs
	if err := command.Start(); err != nil {
		return err
	}
	m.command = command
	m.processStarted = time.Now()
	m.snapshot.State = StateRunning
	m.snapshot.PID = command.Process.Pid
	m.snapshot.StartedAt = m.processStarted.UTC()
	m.snapshot.LastError = ""
	go m.wait(generation, command)
	return nil
}

func (m *Manager) wait(generation uint64, command *exec.Cmd) {
	err := command.Wait()
	m.mu.Lock()
	if generation != m.snapshot.Generation {
		m.mu.Unlock()
		return
	}
	m.command = nil
	m.snapshot.PID = 0
	if m.stopWanted {
		m.snapshot.State = StateStopped
		m.snapshot.LastError = ""
		m.finishLocked()
		m.mu.Unlock()
		return
	}

	lastError := "sing-box exited unexpectedly"
	if err != nil {
		lastError = err.Error()
	}
	m.snapshot.LastError = lastError
	if time.Since(m.processStarted) >= m.restartPolicy.stableWindow {
		m.restartCount = 0
	}
	m.scheduleRestartLocked(generation)
	m.mu.Unlock()
}

func (m *Manager) scheduleRestartLocked(generation uint64) {
	if m.restartCount >= m.restartPolicy.maxRestarts {
		m.snapshot.State = StateFailed
		m.finishLocked()
		return
	}
	delay := m.restartDelayLocked()
	m.restartCount++
	m.snapshot.State = StateStarting
	done := m.done
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}

		m.mu.Lock()
		defer m.mu.Unlock()
		if generation != m.snapshot.Generation || m.stopWanted || m.done != done {
			return
		}
		if err := m.startProcessLocked(generation); err != nil {
			m.snapshot.LastError = err.Error()
			m.scheduleRestartLocked(generation)
		}
	}()
}

func (m *Manager) restartDelayLocked() time.Duration {
	delay := m.restartPolicy.initialDelay
	for attempt := 0; attempt < m.restartCount && delay < m.restartPolicy.maxDelay; attempt++ {
		delay *= 2
		if delay > m.restartPolicy.maxDelay {
			delay = m.restartPolicy.maxDelay
		}
	}
	if m.restartPolicy.jitter != nil {
		return m.restartPolicy.jitter(delay)
	}
	return delay
}

func (m *Manager) finishLocked() {
	if m.done != nil {
		close(m.done)
		m.done = nil
	}
}

func jitter20Percent(delay time.Duration) time.Duration {
	span := delay / 5
	if span <= 0 {
		return delay
	}
	return delay - span + time.Duration(rand.Int64N(int64(2*span)+1))
}

type ringBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	capacity int
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{capacity: capacity}
}

func (b *ringBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(data)
	if len(data) >= b.capacity {
		b.buffer.Reset()
		_, _ = b.buffer.Write(data[len(data)-b.capacity:])
		return original, nil
	}
	overflow := b.buffer.Len() + len(data) - b.capacity
	if overflow > 0 {
		current := b.buffer.Bytes()
		remaining := append([]byte(nil), current[overflow:]...)
		b.buffer.Reset()
		_, _ = b.buffer.Write(remaining)
	}
	_, _ = b.buffer.Write(data)
	return original, nil
}

func (b *ringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
