package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type persistedWorkload struct {
	Workload Workload `json:"workload"`
	Spec     Spec     `json:"spec"`
}

type persistedState struct {
	Version   int                 `json:"version"`
	Workloads []persistedWorkload `json:"workloads"`
}

func writePersistedState(path string, state persistedState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	file, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary state file: %w", err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func readPersistedState(path string) (persistedState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persistedState{Version: 1}, nil
		}
		return persistedState{}, fmt.Errorf("read state: %w", err)
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return persistedState{}, fmt.Errorf("decode state: %w", err)
	}

	return state, nil
}
