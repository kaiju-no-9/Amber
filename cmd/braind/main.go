package main

import (
	"brain/internal/api"
	"brain/internal/engine"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return fmt.Errorf("get user cache directory: %w", err)
	}
	defaultStatePath := filepath.Join(cacheDir, "brain", "state.json")
	statePath := flag.String("state", defaultStatePath, "Path to the workload state file")
	socketPath := flag.String("socket", "./brain.sock", "Unix socket path")

	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(*socketPath), 0700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	listener, err := net.Listen("unix", *socketPath)
	if err != nil {
		return fmt.Errorf("listen on %q: %w (if it is stale, remove it manually)", *socketPath, err)
	}
	defer os.Remove(*socketPath)
	defer listener.Close()

	if err := os.Chmod(*socketPath, 0600); err != nil {
		return fmt.Errorf("restrict socket permissions: %w", err)
	}

	mgr, err := engine.NewManager(*statePath)
	if err != nil {
		return fmt.Errorf("new manager: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.HealthResponse{Status: "ok"})
	})

	mux.HandleFunc("/v1/workloads", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			workloads := mgr.List()
			result := make([]api.WorkloadResponse, 0, len(workloads))
			for _, workload := range workloads {
				result = append(result, api.WorkloadResponse{
					ID:     workload.ID,
					Name:   workload.Name,
					Status: string(workload.State),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req api.CreateWorkloadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid request body: %s", err), http.StatusBadRequest)
			return
		}

		workload, err := mgr.Start(engine.Spec{
			Name:       req.Name,
			Runtime:    req.Runtime,
			Command:    req.Command,
			WorkingDir: req.WorkingDir,
			Env:        req.Env,
		})
		if err != nil {
			if errors.Is(err, engine.ErrNameConflict) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.WorkloadResponse{
			ID:     workload.ID,
			Name:   workload.Name,
			Status: string(workload.State),
		})
	})

	mux.HandleFunc("DELETE /v1/workloads/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, found := mgr.Get(name); !found {
			http.NotFound(w, r)
			return
		}
		if err := mgr.Remove(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	mux.HandleFunc("/v1/workloads/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/v1/workloads/")
		if name == "" || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}
		workload, found := mgr.Get(name)
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.WorkloadResponse{
			ID:     workload.ID,
			Name:   workload.Name,
			Status: string(workload.State),
		})
	})
	mux.HandleFunc("/v1/workloads/{name}/logs", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, found := mgr.Get(name); !found {
			http.NotFound(w, r)
			return
		}
		stdout, stderr, err := mgr.Logs(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.WorkloadLogsResponse{
			Stdout: string(stdout),
			Stderr: string(stderr),
		})
	})
	mux.HandleFunc("/v1/workloads/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if _, found := mgr.Get(name); !found {
			http.NotFound(w, r)
			return
		}
		if err := mgr.Stop(name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Serve(listener)
	}()

	log.Printf("braind listening on %s", *socketPath)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down daemon: %w", err)
		}
		err := <-serverErr
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve API: %w", err)
		}
		return nil
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve API: %w", err)
		}
		return nil
	}
}
