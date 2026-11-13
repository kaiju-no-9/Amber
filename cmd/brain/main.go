package main

import (
	"brain/internal/api"
	"brain/internal/client"
	"brain/internal/config"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "brain:", err)
		os.Exit(1)
	}
}

func execute(args []string) error {
	if len(args) == 1 && args[0] == "health" {
		health, err := client.Health(context.Background(), "./brain.sock")
		if err != nil {
			return err
		}
		fmt.Printf("daemon status: %s\n", health.Status)
		return nil
	}
	if len(args) == 1 && args[0] == "ps" {
		workloads, err := client.ListWorkloads(context.Background(), "./brain.sock")
		if err != nil {
			return err
		}
		if len(workloads) == 0 {
			fmt.Println("no workloads")
			return nil
		}

		fmt.Printf("%-32s %-20s %s\n", "ID", "NAME", "STATE")
		for _, workload := range workloads {
			fmt.Printf("%-32s %-20s %s\n", workload.ID, workload.Name, workload.Status)
		}
		return nil
	}
	//  stoping parameter .
	if len(args) == 2 && args[0] == "stop" {
		if err := client.StopWorkload(context.Background(), "./brain.sock", args[1]); err != nil {
			return err
		}
		fmt.Printf("stop requested for workload %q\n", args[1])
		return nil
	}
	if len(args) == 3 && args[0] == "logs" && args[1] == "-f" {
		return followLogs(args[2])
	}
	if len(args) == 2 && args[0] == "logs" {
		logs, err := client.GetWorkloadLogs(
			context.Background(),
			"./brain.sock",
			args[1],
		)
		if err != nil {
			return err
		}
		fmt.Print(logs.Stdout)
		fmt.Fprint(os.Stderr, logs.Stderr)
		return nil
	}

	if len(args) == 2 && args[0] == "get" {
		workload, err := client.GetWorkload(context.Background(), "./brain.sock", args[1])
		if err != nil {
			return err
		}
		fmt.Printf("ID: %s\nName: %s\nState: %s\n",
			workload.ID, workload.Name, workload.Status)
		return nil
	}
	if len(args) == 2 && args[0] == "rm" {
		if err := client.RemoveWorkload(context.Background(), "./brain.sock", args[1]); err != nil {
			return err
		}
		fmt.Printf("workload %q removed\n", args[1])
		return nil
	}

	if len(args) != 2 {
		return fmt.Errorf("usage: brain <health|ps|validate|run|stop|logs [-f]|get|rm> [NAME|FILE]")
	}

	command, path := args[0], args[1]
	workload, err := config.Load(path)
	if err != nil {
		return err
	}

	switch command {
	case "validate":
		fmt.Printf("workload %q is valid (runtime: %s)\n", workload.Metadata.Name, workload.Spec.Runtime)
		return nil
	case "run":
		created, err := client.CreateWorkload(
			context.Background(),
			"./brain.sock",
			api.CreateWorkloadRequest{
				Name:       workload.Metadata.Name,
				Runtime:    workload.Spec.Runtime,
				Command:    workload.Spec.Command,
				WorkingDir: workload.Spec.WorkingDir,
				Env:        workload.Spec.Env,
				RootFS:     workload.Spec.RootFS,
			},
		)
		if err != nil {
			return err
		}
		fmt.Printf("workload %q started (id %s, state %s)\n",
			created.Name, created.ID, created.Status)
		return nil
	default:
		return fmt.Errorf("unknown command %q; use health, validate, or run", command)
	}
}

func followLogs(name string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previousStdout := ""
	previousStderr := ""

	for {
		logs, err := client.GetWorkloadLogs(ctx, "./brain.sock", name)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		_, _ = fmt.Print(logDelta(previousStdout, logs.Stdout))
		_, _ = fmt.Fprint(os.Stderr, logDelta(previousStderr, logs.Stderr))
		previousStdout = logs.Stdout
		previousStderr = logs.Stderr

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func logDelta(previous, current string) string {
	if strings.HasPrefix(current, previous) {
		return current[len(previous):]
	}

	// The daemon keeps one 10 MiB rotated file followed by the active file.
	const rotatedFileSize = 10 << 20
	if len(current) > rotatedFileSize && len(current) >= rotatedFileSize &&
		strings.HasSuffix(previous, current[:rotatedFileSize]) {
		return current[rotatedFileSize:]
	}
	if len(current) < len(previous) {
		return current
	}
	return current[len(previous):]
}
