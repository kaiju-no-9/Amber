package oci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	//"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func WriteBundle(bundleDir string, command []string, env map[string]string) error {
	if len(command) == 0 || command[0] == "" {
		return fmt.Errorf("container command is required")
	}

	bundlePath, err := filepath.Abs(bundleDir)
	if err != nil {
		return fmt.Errorf("resolve bundle directory: %w", err)
	}
	if err := os.MkdirAll(bundlePath, 0700); err != nil {
		return fmt.Errorf("create bundle directory: %w", err)
	}

	rootfsPath := filepath.Join(bundlePath, "rootfs")
	rootfsInfo, err := os.Stat(rootfsPath)
	if err != nil {
		return fmt.Errorf("find rootfs directory: %w", err)
	}
	if !rootfsInfo.IsDir() {
		return fmt.Errorf("rootfs path %q is not a directory", rootfsPath)
	}

	envKeys := make([]string, 0, len(env))
	for key := range env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)

	envPairs := make([]string, 0, len(env))
	for _, key := range envKeys {
		envPairs = append(envPairs, key+"="+env[key])
	}

	config := specs.Spec{
		Version: specs.Version,
		Process: &specs.Process{
			User: specs.User{UID: 0, GID: 0},
			Args: append([]string(nil), command...),
			Env:  envPairs,
			Cwd:  "/",
		},
		Root: &specs.Root{
			Path: "rootfs",
		},
		Linux: &specs.Linux{
			Namespaces: []specs.LinuxNamespace{
				{Type: specs.PIDNamespace},
				{Type: specs.NetworkNamespace},
				{Type: specs.MountNamespace},
				{Type: specs.IPCNamespace},
				{Type: specs.UTSNamespace},
			},
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode OCI config: %w", err)
	}
	data = append(data, '\n')

	configPath := filepath.Join(bundlePath, "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("write OCI config: %w", err)
	}
	return nil
}
