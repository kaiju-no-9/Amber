package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"brain/internal/api"
)

func CreateWorkload(
	ctx context.Context,
	socketPath string,
	workload api.CreateWorkloadRequest,
) (api.WorkloadResponse, error) {
	body, err := json.Marshal(workload)
	if err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("encode workload request: %w", err)
	}

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
		"http://brain/v1/workloads",
		bytes.NewReader(body),
	)
	if err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("create workload request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := httpClient.Do(request)
	if err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return api.WorkloadResponse{}, fmt.Errorf(
			"daemon rejected workload (%s): %s",
			response.Status,
			bytes.TrimSpace(message),
		)
	}

	var result api.WorkloadResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return api.WorkloadResponse{}, fmt.Errorf("decode create response: %w", err)
	}
	return result, nil
}
