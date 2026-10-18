package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
