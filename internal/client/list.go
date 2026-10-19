package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"brain/internal/api"
)

func ListWorkloads(ctx context.Context, socketPath string) ([]api.WorkloadResponse, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://brain/v1/workloads",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create list request: %w", err)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon list request returned %s", response.Status)
	}

	var workloads []api.WorkloadResponse
	if err := json.NewDecoder(response.Body).Decode(&workloads); err != nil {
		return nil, fmt.Errorf("decode workload list: %w", err)
	}
	return workloads, nil
}

func GetWorkload(ctx context.Context, socketPath, name string) (api.WorkloadResponse, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("http://brain/v1/workloads/%s", strings.TrimPrefix(name, "/")),
		nil,
	)
	if err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("create get request: %w", err)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return api.WorkloadResponse{}, fmt.Errorf("daemon get request returned %s", response.Status)
	}

	var workload api.WorkloadResponse
	if err := json.NewDecoder(response.Body).Decode(&workload); err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("decode workload: %w", err)
	}
	return workload, nil
}

func StopWorkload(ctx context.Context, socketPath, name string) error {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	defer transport.CloseIdleConnections()

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("http://brain/v1/workloads/%s/stop", name),
		nil,
	)
	if err != nil {
		return fmt.Errorf("create stop request: %w", err)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		return fmt.Errorf("daemon stop request returned %s", response.Status)
	}
	return nil
}
