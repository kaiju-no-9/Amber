package engine

import "time"

type State string

const (
	StateCreated  State = "created"
	StateStarting State = "Starting"
	StateRunning  State = "running"
	StateExited   State = "Exited"
	StateFailed   State = "Failed"
	StateStopped  State = "Stopped"
)

type Spec struct {
	Name       string
	Runtime    string
	Command    []string
	WorkingDir string
	Env        map[string]string
}

type Workload struct {
	ID            string
	Name          string
	State         State
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	ExitCode      *int
	FailureReason string
}
