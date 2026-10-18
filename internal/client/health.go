package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"brain/internal/api"
)

func Health(ctx context.Context, socketPath string) (api.HealthResponse, error) {
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
		"http://brain/v1/health",
		nil,
	)
	if err != nil {
		return api.HealthResponse{}, fmt.Errorf("create health request: %w", err)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return api.HealthResponse{}, fmt.Errorf("contact daemon: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return api.HealthResponse{}, fmt.Errorf("daemon health endpoint returned %s", response.Status)
	}

	var health api.HealthResponse
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return api.HealthResponse{}, fmt.Errorf("decode health response: %w", err)
	}
	return health, nil
}
