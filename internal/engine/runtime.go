// engine to build and run container runtime

package engine

import (
	"context"
	//"os/signal"
)

type RuntimeState struct {
	ID     string
	Status string
	PID    int
	Bundle string
}

type ContainerRuntime interface {
	WriteBundle(bundleDir, rootfsDir, workingDir string, command []string, env map[string]string) error
	Create(ctx context.Context, id, bundleDir string) error
	Start(ctx context.Context, id string) error
	State(ctx context.Context, id string) (RuntimeState, error)
	Kill(ctx context.Context, id, signal string) error
	Delete(ctx context.Context, id string, force bool) error
}
