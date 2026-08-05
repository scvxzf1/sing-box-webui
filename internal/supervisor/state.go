package supervisor

import "time"

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

type Snapshot struct {
	State      State     `json:"state"`
	Generation uint64    `json:"generation"`
	PID        int       `json:"pid,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	LastError  string    `json:"lastError,omitempty"`
}

func (s State) Valid() bool {
	switch s {
	case StateStopped, StateStarting, StateRunning, StateStopping, StateFailed:
		return true
	default:
		return false
	}
}
