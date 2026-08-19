//go:build linux

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const maxLogBytes = 256 << 10

var ErrAlreadyRunning = errors.New("sing-box is already running")

type Manager struct {
	mu         sync.RWMutex
	binary     string
	snapshot   Snapshot
	command    *exec.Cmd
	done       chan struct{}
	stopWanted bool
	logs       *ringBuffer
}

func NewManager(binary string) *Manager {
	return &Manager{
		binary:   binary,
		snapshot: Snapshot{State: StateStopped},
		logs:     newRingBuffer(maxLogBytes),
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
	command := exec.Command(m.binary, "run", "-c", configPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = m.logs
	command.Stderr = m.logs
	if err := command.Start(); err != nil {
		m.snapshot.State = StateFailed
		m.snapshot.LastError = err.Error()
		result := m.snapshot
		m.mu.Unlock()
		return result, fmt.Errorf("start sing-box: %w", err)
	}
	m.command = command
	m.done = make(chan struct{})
	m.snapshot.State = StateRunning
	m.snapshot.PID = command.Process.Pid
	m.snapshot.StartedAt = time.Now().UTC()
	result := m.snapshot
	done := m.done
	m.mu.Unlock()

	go m.wait(generation, command, done)

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
	case <-time.After(300 * time.Millisecond):
		return result, nil
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
		m.snapshot.State = StateStopped
		m.snapshot.PID = 0
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

func (m *Manager) wait(generation uint64, command *exec.Cmd, done chan struct{}) {
	err := command.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.snapshot.Generation {
		close(done)
		return
	}
	m.command = nil
	m.snapshot.PID = 0
	if m.stopWanted {
		m.snapshot.State = StateStopped
		m.snapshot.LastError = ""
	} else {
		m.snapshot.State = StateFailed
		if err != nil {
			m.snapshot.LastError = err.Error()
		} else {
			m.snapshot.LastError = "sing-box exited unexpectedly"
		}
	}
	close(done)
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
