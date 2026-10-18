package runner

import (
	"brain/internal/config"
	"context"
	"fmt"
	"os"
	"os/exec"
)

func Run(ctx context.Context, workload config.Workload) error {
	cmd := exec.CommandContext(
		ctx,
		workload.Spec.Command[0],
		workload.Spec.Command[1:]...,
	)
	cmd.Dir = workload.Spec.WorkingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
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
