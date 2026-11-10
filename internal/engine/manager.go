package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	//"internal/runtime/exithook"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
	//"golang.org/x/tools/go/analysis/passes/stdmethods"
)

var ErrNameConflict = errors.New("a workload with this name already exists")

type managedWorkload struct {
	workload      Workload
	spec          Spec
	command       *exec.Cmd
	stopRequested bool
	done          chan struct{}
	stdoutPath    string
	stderrPath    string
	stdoutLog     *rotatingLogWriter
	stderrLog     *rotatingLogWriter
}

type Manager struct {
	mu        sync.Mutex
	byID      map[string]*managedWorkload
	byName    map[string]string
	statePath string
}

func NewManager(statePath string) (*Manager, error) {
	m := &Manager{
		byID:      make(map[string]*managedWorkload),
		byName:    make(map[string]string),
		statePath: statePath,
	}

	state, err := readPersistedState(statePath)
	if err != nil {
		return nil, err
	}
	if state.Version != 1 {
		return nil, fmt.Errorf("unsupported state file version %d", state.Version)
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("find cache directory: %w", err)
	}
	stateChanged := false
	for _, saved := range state.Workloads {
		workload := saved.Workload

		// The manager can’t reconnect to a process after restarting yet.
		if workload.State == StateRunning || workload.State == StateStarting {
			workload.State = StateFailed
			stateChanged = true
			workload.FailureReason = "daemon restarted; process recovery is not implemented"
			now := time.Now()
			workload.FinishedAt = &now
		}

		logDir := filepath.Join(cacheDir, "brain", "logs", workload.ID)
		entry := &managedWorkload{
			workload:   workload,
			spec:       saved.Spec,
			stdoutPath: filepath.Join(logDir, "stdout.log"),
			stderrPath: filepath.Join(logDir, "stderr.log"),
		}
		m.byID[workload.ID] = entry
		m.byName[workload.Name] = workload.ID
	}
	if stateChanged {
		if err := m.saveLocked(); err != nil {
			return nil, fmt.Errorf("save recovered state: %w", err)
		}
	}

	return m, nil
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
	//......................................
	id := hex.EncodeToString(idBytes)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return Workload{}, fmt.Errorf("find cache dir: %w", err)
	}
	logDir := filepath.Join(cacheDir, "brain", "logs", id)
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return Workload{}, fmt.Errorf("create log dir: %w", err)
	}
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stdoutLog, err := openRotatingLogWriter(stdoutPath)
	if err != nil {
		return Workload{}, fmt.Errorf("create stdout log: %w", err)
	}
	stderrPath := filepath.Join(logDir, "stderr.log")
	stderrLog, err := openRotatingLogWriter(stderrPath)
	if err != nil {
		_ = stdoutLog.Close()
		return Workload{}, fmt.Errorf("create stderr log: %w", err)
	}
	closeLogs := true
	defer func() {
		if closeLogs {
			_ = stdoutLog.Close()
			_ = stderrLog.Close()
		}
	}()
	//...................................
	cmd := exec.Command(spec.Command[0], spec.Command[1:]...)
	cmd.Dir = spec.WorkingDir
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog
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
		workload:   workload,
		spec:       spec,
		command:    cmd,
		done:       make(chan struct{}),
		stdoutPath: stdoutPath,
		stderrPath: stderrPath,
		stdoutLog:  stdoutLog,
		stderrLog:  stderrLog,
	}
	m.byID[id] = entry
	m.byName[spec.Name] = id
	if err := m.saveLocked(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		delete(m.byID, id)
		delete(m.byName, spec.Name)
		return Workload{}, fmt.Errorf("save state: %w", err)
	}

	closeLogs = false
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
	defer func() {
		if entry.stdoutLog != nil {
			_ = entry.stdoutLog.Close()
		}
		if entry.stderrLog != nil {
			_ = entry.stderrLog.Close()
		}
		if saveErr := m.saveLocked(); saveErr != nil {
			log.Printf("save workload state after process exit (id %s): %v", id, saveErr)
		}
		if entry.done != nil {
			close(entry.done)
		}
	}()

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

	entry.stopRequested = true
	process := entry.command.Process
	done := entry.done
	if err := process.Signal(os.Interrupt); err != nil {
		entry.stopRequested = false
		m.mu.Unlock()
		return fmt.Errorf("stop workload %q: %w", name, err)
	}
	m.mu.Unlock()

	if done == nil {
		return nil
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("force stop workload %q: %w", name, err)
		}
		<-done
		return nil
	}
}

// Shutdown stops managed processes before the daemon exits so they are not left
// running without a manager that can inspect or stop them.
func (m *Manager) Shutdown() error {
	m.mu.Lock()
	names := make([]string, 0, len(m.byName))
	for name, id := range m.byName {
		if m.byID[id].workload.State == StateRunning {
			names = append(names, name)
		}
	}
	m.mu.Unlock()

	var shutdownErrors []error
	for _, name := range names {
		if err := m.Stop(name); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	return errors.Join(shutdownErrors...)
}

func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id, exists := m.byName[name]
	if !exists {
		return fmt.Errorf("workload %q not found", name)
	}

	entry := m.byID[id]
	if entry.workload.State == StateRunning || entry.workload.State == StateStarting {
		return fmt.Errorf("workload %q is still running", name)
	}

	logDir := ""
	if entry.stdoutPath != "" {
		logDir = filepath.Dir(entry.stdoutPath)
	}

	delete(m.byID, id)
	delete(m.byName, name)

	if err := m.saveLocked(); err != nil {
		m.byID[id] = entry
		m.byName[name] = id
		return fmt.Errorf("save workload removal: %w", err)
	}

	if logDir != "" {
		if err := os.RemoveAll(logDir); err != nil {
			return fmt.Errorf("workload was removed, but its logs could not be deleted: %w", err)
		}
	}

	return nil
}

func (m *Manager) Logs(name string) ([]byte, []byte, error) {
	m.mu.Lock()

	id, exists := m.byName[name]
	if !exists {
		m.mu.Unlock()
		return nil, nil, fmt.Errorf("workload %q not found", name)
	}
	entry := m.byID[id]
	stdoutPath := entry.stdoutPath
	stderrPath := entry.stderrPath
	m.mu.Unlock()

	stdout, err := readRotatedLog(stdoutPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read stdout: %w", err)
	}
	stderr, err := readRotatedLog(stderrPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read stderr: %w", err)
	}
	return stdout, stderr, nil
}

func readRotatedLog(path string) ([]byte, error) {
	previous, previousErr := os.ReadFile(path + ".1")
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return nil, previousErr
	}
	current, currentErr := os.ReadFile(path)
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return nil, currentErr
	}
	return append(previous, current...), nil
}

func (m *Manager) saveLocked() error {
	state := persistedState{
		Version: 1,
	}

	for _, entry := range m.byID {
		state.Workloads = append(state.Workloads, persistedWorkload{
			Workload: entry.workload,
			Spec:     entry.spec,
		})
	}
	return writePersistedState(m.statePath, state)
}
