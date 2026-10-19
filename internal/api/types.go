package api

type HealthResponse struct {
	Status string `json:"status"`
}

type CreateWorkloadRequest struct {
	Name       string            `json:"name"`
	Runtime    string            `json:"runtime"`
	Command    []string          `json:"command"`
	WorkingDir string            `json:"workingDir"`
	Env        map[string]string `json:"env"`
}

type WorkloadResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type WorkloadLogsResponse struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}
