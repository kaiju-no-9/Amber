package engine

import (
	"context"
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
	workload       Workload
	spec           Spec
	command        *exec.Cmd
	stopRequested  bool
	done           chan struct{}
	stdoutPath     string
	stderrPath     string
	stdoutLog      *rotatingLogWriter
	stderrLog      *rotatingLogWriter
	container      bool
	bundleDir      string
	runtimeDeleted bool
	needsReconcile bool
}

type Manager struct {
	mu               sync.Mutex
	byID             map[string]*managedWorkload
	byName           map[string]string
	statePath        string
	containerRuntime ContainerRuntime
	bundleRoot       string
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

		wasActive := workload.State == StateRunning || workload.State == StateStarting
		if wasActive {
			workload.State = StateFailed
			stateChanged = true
			if saved.Spec.Runtime == "container" {
				workload.FailureReason = "daemon restarted; OCI runtime state reconciliation is pending"
			} else {
				workload.FailureReason = "daemon restarted; process recovery is not implemented"
			}
			now := time.Now()
			workload.FinishedAt = &now
		}

		logDir := filepath.Join(cacheDir, "brain", "logs", workload.ID)
		entry := &managedWorkload{
			workload:       workload,
			spec:           saved.Spec,
			stdoutPath:     filepath.Join(logDir, "stdout.log"),
			stderrPath:     filepath.Join(logDir, "stderr.log"),
			container:      saved.Spec.Runtime == "container",
			needsReconcile: wasActive,
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

func (m *Manager) ConfigureContainerRuntime(runtime ContainerRuntime, bundleRoot string) error {
	if runtime == nil {
		return errors.New("container runtime is required")
	}
	if bundleRoot == "" {
		return errors.New("container bundle directory is required")
	}
	absoluteBundleRoot, err := filepath.Abs(bundleRoot)
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	if err := os.MkdirAll(absoluteBundleRoot, 0700); err != nil {
		return fmt.Errorf("create bundle directory: %w", err)
	}

	m.mu.Lock()
	m.containerRuntime = runtime
	m.bundleRoot = absoluteBundleRoot
	var reconcileIDs []string
	for id, entry := range m.byID {
		if entry.container && entry.needsReconcile {
			entry.bundleDir = filepath.Join(absoluteBundleRoot, id)
			reconcileIDs = append(reconcileIDs, id)
		}
	}
	m.mu.Unlock()

	for _, id := range reconcileIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		state, stateErr := runtime.State(ctx, id)
		cancel()

		m.mu.Lock()
		entry := m.byID[id]
		if entry == nil {
			m.mu.Unlock()
			continue
		}
		entry.needsReconcile = false
		if stateErr != nil {
			entry.workload.FailureReason = "daemon restart; could not inspect OCI runtime state: " + stateErr.Error()
		} else {
			switch state.Status {
			case "running":
				entry.workload.State = StateRunning
				entry.workload.FailureReason = ""
				entry.workload.FinishedAt = nil
				entry.done = make(chan struct{})
			case "stopped":
				entry.workload.State = StateExited
				entry.workload.FailureReason = ""
				now := time.Now()
				entry.workload.FinishedAt = &now
				if err := runtime.Delete(context.Background(), id, false); err == nil {
					entry.runtimeDeleted = true
				} else {
					log.Printf("delete recovered OCI container (id %s): %v", id, err)
				}
			default:
				entry.workload.FailureReason = fmt.Sprintf("daemon restarted while OCI runtime was %q", state.Status)
				if err := runtime.Delete(context.Background(), id, true); err == nil {
					entry.runtimeDeleted = true
				}
			}
		}
		if err := m.saveLocked(); err != nil {
			log.Printf("save reconciled OCI state (id %s): %v", id, err)
		}
		shouldMonitor := entry.workload.State == StateRunning
		m.mu.Unlock()
		if shouldMonitor {
			go m.monitorContainer(id, runtime)
		}
	}
	return nil
}

func (m *Manager) Start(spec Spec) (Workload, error) {
	if spec.Name == "" {
		return Workload{}, errors.New("workload name is required")
	}
	if len(spec.Command) == 0 || spec.Command[0] == "" {
		return Workload{}, errors.New("workload command is required")
	}
	if spec.Runtime == "container" {
		return m.startContainer(spec)
	}
	if spec.Runtime != "process" {
		return Workload{}, fmt.Errorf("unsupported runtime %q", spec.Runtime)
	}
	if spec.RootFS != "" {
		return Workload{}, errors.New("rootfs is only valid for the container runtime")
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

func (m *Manager) startContainer(spec Spec) (Workload, error) {
	if spec.RootFS == "" {
		return Workload{}, errors.New("container rootfs is required")
	}

	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Workload{}, fmt.Errorf("generate workload ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	m.mu.Lock()
	if _, exists := m.byName[spec.Name]; exists {
		m.mu.Unlock()
		return Workload{}, ErrNameConflict
	}
	runtime := m.containerRuntime
	bundleRoot := m.bundleRoot
	if runtime == nil {
		m.mu.Unlock()
		return Workload{}, errors.New("container runtime is unavailable; OCI containers require Linux and runc")
	}
	bundleDir := filepath.Join(bundleRoot, id)
	now := time.Now()
	workload := Workload{ID: id, Name: spec.Name, State: StateStarting, CreatedAt: now}
	entry := &managedWorkload{
		workload:  workload,
		spec:      spec,
		done:      make(chan struct{}),
		container: true,
		bundleDir: bundleDir,
	}
	m.byID[id] = entry
	m.byName[spec.Name] = id
	if err := m.saveLocked(); err != nil {
		delete(m.byID, id)
		delete(m.byName, spec.Name)
		m.mu.Unlock()
		return Workload{}, fmt.Errorf("save starting container: %w", err)
	}
	m.mu.Unlock()

	cleanup := func(created bool, cause error) (Workload, error) {
		if created {
			_ = runtime.Kill(context.Background(), id, "KILL")
			_ = runtime.Delete(context.Background(), id, true)
		}
		_ = os.RemoveAll(bundleDir)
		m.mu.Lock()
		delete(m.byID, id)
		delete(m.byName, spec.Name)
		saveErr := m.saveLocked()
		m.mu.Unlock()
		if saveErr != nil {
			return Workload{}, fmt.Errorf("%v; also failed to save rollback: %w", cause, saveErr)
		}
		return Workload{}, cause
	}

	if err := runtime.WriteBundle(bundleDir, spec.RootFS, spec.WorkingDir, spec.Command, spec.Env); err != nil {
		return cleanup(false, fmt.Errorf("write OCI bundle: %w", err))
	}
	if err := runtime.Create(context.Background(), id, bundleDir); err != nil {
		return cleanup(true, fmt.Errorf("create OCI container: %w", err))
	}
	if err := runtime.Start(context.Background(), id); err != nil {
		return cleanup(true, fmt.Errorf("start OCI container: %w", err))
	}

	m.mu.Lock()
	entry = m.byID[id]
	now = time.Now()
	entry.workload.State = StateRunning
	entry.workload.StartedAt = &now
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return cleanup(true, fmt.Errorf("save running container: %w", err))
	}
	workload = entry.workload
	m.mu.Unlock()

	go m.monitorContainer(id, runtime)
	return workload, nil
}

func (m *Manager) monitorContainer(id string, runtime ContainerRuntime) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		state, err := runtime.State(context.Background(), id)
		if err != nil {
			log.Printf("inspect OCI container (id %s): %v", id, err)
			continue
		}
		if state.Status != "stopped" {
			continue
		}
		deleteErr := runtime.Delete(context.Background(), id, false)
		if deleteErr != nil {
			log.Printf("delete stopped OCI container (id %s): %v", id, deleteErr)
		}

		m.mu.Lock()
		entry, exists := m.byID[id]
		if exists {
			now := time.Now()
			if entry.stopRequested {
				entry.workload.State = StateStopped
			} else {
				entry.workload.State = StateExited
			}
			entry.workload.FinishedAt = &now
			entry.runtimeDeleted = deleteErr == nil
			if saveErr := m.saveLocked(); saveErr != nil {
				log.Printf("save OCI container state (id %s): %v", id, saveErr)
			}
			if entry.done != nil {
				close(entry.done)
			}
		}
		m.mu.Unlock()
		return
	}
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
	if entry.container {
		runtime := m.containerRuntime
		done := entry.done
		entry.stopRequested = true
		m.mu.Unlock()
		if runtime == nil {
			return errors.New("container runtime is unavailable")
		}
		if err := runtime.Kill(context.Background(), id, "TERM"); err != nil {
			return fmt.Errorf("stop container %q: %w", name, err)
		}
		if done == nil {
			return nil
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
			return nil
		case <-timer.C:
			select {
			case <-done:
				return nil
			default:
			}
			if err := runtime.Kill(context.Background(), id, "KILL"); err != nil {
				return fmt.Errorf("force stop container %q: %w", name, err)
			}
			forceTimer := time.NewTimer(5 * time.Second)
			defer forceTimer.Stop()
			select {
			case <-done:
				return nil
			case <-forceTimer.C:
				return fmt.Errorf("container %q did not stop after SIGKILL", name)
			}
		}
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
	if entry.container && !entry.runtimeDeleted {
		if m.containerRuntime == nil {
			return errors.New("cannot remove container metadata while the OCI runtime is unavailable")
		}
		if err := m.containerRuntime.Delete(context.Background(), id, true); err != nil {
			return fmt.Errorf("delete OCI container resources: %w", err)
		}
		entry.runtimeDeleted = true
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
	if entry.bundleDir != "" {
		if err := os.RemoveAll(entry.bundleDir); err != nil {
			return fmt.Errorf("workload was removed, but its bundle could not be deleted: %w", err)
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
	if entry.container {
		m.mu.Unlock()
		return nil, nil, errors.New("container logs are not available in the OCI proof of concept")
	}
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
