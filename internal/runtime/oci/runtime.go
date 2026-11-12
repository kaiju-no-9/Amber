package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

type Runtime struct {
	binary string
	root   string
}

func NewRuntime(binaryPath, stateRoot string) (*Runtime, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("OCI containers require Linux; current OS is %s", runtime.GOOS)
	}

	if binaryPath == "" {
		var err error
		binaryPath, err = exec.LookPath("runc")
		if err != nil {
			return nil, fmt.Errorf("find runc in PATH: %w", err)
		}
	}

	if stateRoot == "" {
		return nil, fmt.Errorf("runc state directory is required")
	}
	absoluteRoot, err := filepath.Abs(stateRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve runc state directory: %w", err)
	}
	if err := os.MkdirAll(absoluteRoot, 0700); err != nil {
		return nil, fmt.Errorf("create runc state directory: %w", err)
	}

	return &Runtime{binary: binaryPath, root: absoluteRoot}, nil
}

func (r *Runtime) Create(ctx context.Context, id, bundleDir string) error {
	if err := validateID(id); err != nil {
		return err
	}
	absoluteBundle, err := filepath.Abs(bundleDir)
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	return r.run(ctx, "--root", r.root, "create", "--bundle", absoluteBundle, id)
}

func (r *Runtime) Start(ctx context.Context, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	return r.run(ctx, "--root", r.root, "start", id)
}

func (r *Runtime) State(ctx context.Context, id string) (specs.State, error) {
	if err := validateID(id); err != nil {
		return specs.State{}, err
	}

	output, err := r.output(ctx, "--root", r.root, "state", id)
	if err != nil {
		return specs.State{}, err
	}

	var state specs.State
	if err := json.Unmarshal(output, &state); err != nil {
		return specs.State{}, fmt.Errorf("decode runc state: %w", err)
	}
	if state.ID != id {
		return specs.State{}, fmt.Errorf("runc returned state for unexpected ID %q", state.ID)
	}
	return state, nil
}

func (r *Runtime) Kill(ctx context.Context, id, signal string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if signal != "TERM" && signal != "KILL" {
		return fmt.Errorf("unsupported signal %q; use TERM or KILL", signal)
	}
	return r.run(ctx, "--root", r.root, "kill", id, signal)
}

func (r *Runtime) Delete(ctx context.Context, id string, force bool) error {
	if err := validateID(id); err != nil {
		return err
	}

	args := []string{"--root", r.root, "delete"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, id)
	return r.run(ctx, args...)
}

func (r *Runtime) run(ctx context.Context, args ...string) error {
	_, err := r.output(ctx, args...)
	return err
}

func (r *Runtime) output(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, r.binary, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("runc %s: %w: %s",
			args[len(args)-1], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func validateID(id string) error {
	if id == "" || strings.HasPrefix(id, "-") ||
		strings.ContainsAny(id, "/\\\x00") {
		return fmt.Errorf("invalid container ID %q", id)
	}
	return nil
}