package runner

import (
	"brain/internal/config"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func Run(ctx context.Context, workload config.Workload) error {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("find cache dir: %w", err)
	}
	logDir := filepath.Join(cacheDir, "brain", "logs", workload.Metadata.Name)
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	stdoutPath := filepath.Join(logDir, "stdout.log")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("create stdout log: %w", err)
	}
	defer stdoutFile.Close()

	stderrPath := filepath.Join(logDir, "stderr.log")
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("create stderr log: %w", err)
	}
	defer stderrFile.Close()

	cmd := exec.CommandContext(
		ctx,
		workload.Spec.Command[0],
		workload.Spec.Command[1:]...,
	)
	cmd.Dir = workload.Spec.WorkingDir
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	cmd.Stdin = os.Stdin

	cmd.Env = os.Environ()
	for key, value := range workload.Spec.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	fmt.Printf("starting %q with process runtime (not a container)\n", workload.Metadata.Name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run workload %q: %w", workload.Metadata.Name, err)
	}
	return nil
}
