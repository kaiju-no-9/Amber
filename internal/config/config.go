package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const apiVersion = "brain.dev/v1alpha1"

var (
	workloadName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	envName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Workload struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type Spec struct {
	Runtime    string            `yaml:"runtime"`
	Command    []string          `yaml:"command"`
	WorkingDir string            `yaml:"workingDir"`
	Env        map[string]string `yaml:"env"`
	RootFS     string            `yaml:"rootfs"`
}

func Load(path string) (Workload, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Workload{}, fmt.Errorf("read workload file: %w", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var workload Workload
	if err := decoder.Decode(&workload); err != nil {
		return Workload{}, fmt.Errorf("decode workload YAML: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Workload{}, errors.New("workload file must contain exactly one YAML document")
		}
		return Workload{}, fmt.Errorf("decode trailing YAML: %w", err)
	}
	if err := workload.Validate(); err != nil {
		return Workload{}, err
	}

	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Workload{}, fmt.Errorf("resolve workload file directory: %w", err)
	}
	if workload.Spec.Runtime == "container" {
		if workload.Spec.WorkingDir == "" {
			workload.Spec.WorkingDir = "/"
		}
		if !pathpkg.IsAbs(workload.Spec.WorkingDir) {
			return Workload{}, errors.New("spec.workingDir must be an absolute path inside the container")
		}
		if workload.Spec.RootFS != "" && !filepath.IsAbs(workload.Spec.RootFS) {
			workload.Spec.RootFS = filepath.Join(base, workload.Spec.RootFS)
		}
		rootfsInfo, err := os.Stat(workload.Spec.RootFS)
		if err != nil {
			return Workload{}, fmt.Errorf("inspect spec.rootfs: %w", err)
		}
		if !rootfsInfo.IsDir() {
			return Workload{}, fmt.Errorf("spec.rootfs %q is not a directory", workload.Spec.RootFS)
		}
	} else {
		if workload.Spec.WorkingDir == "" {
			workload.Spec.WorkingDir = "."
		}
		if !filepath.IsAbs(workload.Spec.WorkingDir) {
			workload.Spec.WorkingDir = filepath.Join(base, workload.Spec.WorkingDir)
		}
	}

	return workload, nil
}

func (w Workload) Validate() error {
	var problems []string

	if w.APIVersion != apiVersion {
		problems = append(problems, "apiVersion must be "+apiVersion)
	}
	if w.Kind != "Workload" {
		problems = append(problems, "kind must be Workload")
	}
	if !workloadName.MatchString(w.Metadata.Name) {
		problems = append(problems, "metadata.name must be 1-63 lowercase letters, digits, or internal hyphens")
	}
	switch w.Spec.Runtime {
	case "process":
		if w.Spec.RootFS != "" {
			problems = append(problems, "spec.rootfs is only valid when spec.runtime is container")
		}
	case "container":
		if w.Spec.RootFS == "" {
			problems = append(problems, "spec.rootfs is required when spec.runtime is container")
		}
	default:
		problems = append(problems, "spec.runtime must be process or container")
	}
	if len(w.Spec.Command) == 0 || w.Spec.Command[0] == "" {
		problems = append(problems, "spec.command must contain a program")
	}
	for key := range w.Spec.Env {
		if !envName.MatchString(key) {
			problems = append(problems, fmt.Sprintf("invalid environment variable name %q", key))
		}
	}

	if len(problems) > 0 {
		return errors.New("invalid workload:\n - " + strings.Join(problems, "\n - "))
	}
	return nil
}
