package main

import (
	"brain/internal/api"
	"brain/internal/client"
	"brain/internal/config"
	"context"
	"fmt"
	"os"
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

	if len(args) != 2 {
		return fmt.Errorf("usage: brain <health|validate|run> [FILE]")
	}

	command, path := args[0], args[1]
	workload, err := config.Load(path)
	if err != nil {
		return err
	}

	switch command {
	case "validate":
		fmt.Printf("workload %q is valid (runtime: process)\n", workload.Metadata.Name)
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
