package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"
)

var ErrNameConflict = errors.New("a workload with this name already exists")

type managedWorkload struct {
	workload      Workload
	spec          Spec
	command       *exec.Cmd
	stopRequested bool
}

type Manager struct {
	mu     sync.Mutex
	byID   map[string]*managedWorkload
	byName map[string]string
}

func NewManager() *Manager {
	return &Manager{
		byID:   make(map[string]*managedWorkload),
		byName: make(map[string]string),
	}
}

func (m *Manager) Start(spec Spec) (Workload, error) {
	if spec.Name == "" {
		return Workload{}, errors.New("workload name is required")
	}
	if spec.Runtime != "process" {
		return Workload{}, errors.New("only the process runtime is supported")
	}
	if len(spec.Command) == 0 || spec.Command[0] == "" {
		return Workload{}, errors.New("workload command is required")
	}

	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Workload{}, fmt.Errorf("generate workload ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Dir = spec.WorkingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	for key, value := range spec.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.byName[spec.Name]; exists {
		return Workload{}, ErrNameConflict
	}
	if err := cmd.Start(); err != nil {
		return Workload{}, fmt.Errorf("start process: %w", err)
	}

	now := time.Now()
	workload := Workload{
		ID:        id,
		Name:      spec.Name,
		State:     StateRunning,
		CreatedAt: now,
		StartedAt: &now,
	}
	entry := &managedWorkload{
		workload: workload,
		spec:     spec,
		command:  cmd,
	}
	m.byID[id] = entry
	m.byName[spec.Name] = id

	go m.wait(id, cmd)
	return workload, nil
}

func (m *Manager) wait(id string, cmd *exec.Cmd) {
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, exists := m.byID[id]
	if !exists {
		return
	}

	now := time.Now()
	entry.workload.FinishedAt = &now

	if err == nil {
		code := 0
		entry.workload.ExitCode = &code
		if entry.stopRequested {
			entry.workload.State = StateStopped
		} else {
			entry.workload.State = StateExited
		}
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		entry.workload.ExitCode = &code
		if entry.stopRequested {
			entry.workload.State = StateStopped
		} else {
			entry.workload.State = StateExited
		}
		return
	}

	entry.workload.State = StateFailed
	entry.workload.FailureReason = err.Error()
}

func (m *Manager) Get(name string) (Workload, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id, exists := m.byName[name]
	if !exists {
		return Workload{}, false
	}
	return m.byID[id].workload, true
}

func (m *Manager) List() []Workload {
	m.mu.Lock()
	defer m.mu.Unlock()
	workloads := make([]Workload, 0, len(m.byID))
	for _, entry := range m.byID {
		workloads = append(workloads, entry.workload)
	}
	sort.Slice(workloads, func(i, j int) bool {
		return workloads[i].CreatedAt.Before(workloads[j].CreatedAt)
	})
	return workloads
}

//  for stopping

func (m *Manager) Stop(name string) error {
	m.mu.Lock()

	id, exists := m.byName[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("workload %q not found", name)
	}

	entry := m.byID[id]
	if entry.workload.State != StateRunning {
		m.mu.Unlock()
		return nil
	}

	if entry.command == nil || entry.command.Process == nil {
		m.mu.Unlock()
		return fmt.Errorf("workload %q has no running process", name)
	}

	if err := entry.command.Process.Signal(os.Interrupt); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("stop workload %q: %w", name, err)
	}

	entry.stopRequested = true
	m.mu.Unlock()
	return nil
}
