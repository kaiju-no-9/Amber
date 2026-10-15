# Build Brain: a step-by-step Go tutorial

This tutorial turns the design in [`BRAIN_IMPLEMENTATION_PLAN.md`](BRAIN_IMPLEMENTATION_PLAN.md) into a practical sequence. Work through one step at a time and run each command from the repository root.

**Start here:** No implementation code has been created yet. Follow Step 1, then pause at the end of each step and build the project yourself. I can explain the next step or help debug what you write as you go.

Brain is a YAML-driven workload engine. We will first build a useful **process-mode MVP**: it validates a workload file and runs a normal host process. This first version is not a container and does not isolate untrusted programs. After the control plane works, the later chapters explain how to add a daemon and then an OCI runtime.

## What you will build

The first slice will support:

```text
brain validate examples/hello.yaml
brain run examples/hello.yaml
```

The command is intentionally small: strict YAML decoding, validation, and direct process execution. The daemon, persistence, lifecycle commands, and actual containers come in later milestones. That order keeps each stage understandable and runnable.

## Before you start

- Install a current Go toolchain and check it with `go version`.
- Open a terminal in the repository directory.
- Read the plan’s sections 2, 6, 9, 17, and 19 for the architectural decisions behind this tutorial.
- Use a harmless command that you trust. Process mode runs with your normal user permissions.

## Step 1: Create the Go module and folders

For this local learning project, use the module path `brain`. If you later publish it, you can change the module path to your repository path.

```bash
go mod init brain
mkdir -p cmd/brain internal/config internal/runner examples
```

The first version uses only the Go standard library plus the YAML decoder:

```bash
go get gopkg.in/yaml.v3
```

Keep these package responsibilities clear:

- `cmd/brain` wires the command-line program together.
- `internal/config` owns the YAML schema and validation.
- `internal/runner` starts a process using an argument array.
- `examples` contains workload files people can copy and edit.

## Step 2: Define the workload format

Create `examples/hello.yaml`:

```yaml
apiVersion: brain.dev/v1alpha1
kind: Workload
metadata:
  name: hello
spec:
  runtime: process
  command: ["go", "version"]
  workingDir: .
  env:
    APP_ENV: development
```

`command` is an array so Brain can pass arguments directly to the operating system. Brain does not join these values into a shell command. If you want shell behavior, request a shell explicitly in the array, for example `command: ["sh", "-c", "echo hello"]`.

## Step 3: Decode YAML strictly and validate it

Create `internal/config/config.go`:

```go
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const apiVersion = "brain.dev/v1alpha1"

var workloadName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

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

	// A second YAML document is almost always accidental; reject it.
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
	if workload.Spec.WorkingDir == "" {
		workload.Spec.WorkingDir = "."
	}
	if !filepath.IsAbs(workload.Spec.WorkingDir) {
		workload.Spec.WorkingDir = filepath.Join(base, workload.Spec.WorkingDir)
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
	if w.Spec.Runtime != "process" {
		problems = append(problems, "spec.runtime must be process in this version")
	}
	if len(w.Spec.Command) == 0 || w.Spec.Command[0] == "" {
		problems = append(problems, "spec.command must contain a program")
	}
	for key := range w.Spec.Env {
		if key == "" || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(key) {
			problems = append(problems, fmt.Sprintf("invalid environment variable name %q", key))
		}
	}
	if len(problems) > 0 {
		return errors.New("invalid workload:\n - " + strings.Join(problems, "\n - "))
	}
	return nil
}
```

The name and environment patterns can both be package-level variables if you later want to avoid compiling the environment expression for each validation.

What this teaches:

1. `KnownFields(true)` makes misspelled or unsupported keys errors instead of silently ignored settings.
2. Validation is explicit and happens before execution.
3. Relative working directories are resolved from the YAML file’s directory, so the example works from any current directory.
4. The loader rejects extra YAML documents instead of quietly choosing one.

## Step 4: Add a direct process runner

Create `internal/runner/process.go`:

```go
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"brain/internal/config"
)

func Run(ctx context.Context, workload config.Workload) error {
	cmd := exec.CommandContext(ctx, workload.Spec.Command[0], workload.Spec.Command[1:]...)
	cmd.Dir = workload.Spec.WorkingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), envPairs(workload.Spec.Env)...)

	fmt.Printf("starting %q with process runtime (not a container)\n", workload.Metadata.Name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run workload %q: %w", workload.Metadata.Name, err)
	}
	return nil
}

func envPairs(values map[string]string) []string {
	pairs := make([]string, 0, len(values))
	for key, value := range values {
		pairs = append(pairs, key+"="+value)
	}
	return pairs
}
```

If you chose a module path other than `brain`, replace the import above with that path. No shell is inserted between Brain and the command. That prevents shell metacharacters in YAML from being interpreted unexpectedly.

## Step 5: Add the CLI

Create `cmd/brain/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"

	"brain/internal/config"
	"brain/internal/runner"
)

func main() {
	if err := execute(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "brain:", err)
		os.Exit(1)
	}
}

func execute(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: brain <validate|run> FILE")
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
		return runner.Run(context.Background(), workload)
	default:
		return fmt.Errorf("unknown command %q; use validate or run", command)
	}
}
```

If you chose a module path other than `brain`, update these imports to match it.

## Step 6: Build and try it

```bash
go build -o brain ./cmd/brain
./brain validate examples/hello.yaml
./brain run examples/hello.yaml
```

Expected behavior: validation prints the workload name, and running prints the Go version. Try changing `command` to a harmless program available on your computer. Then add a misspelled YAML key such as `runtme` and confirm validation fails before running anything.

If compilation says `undefined: strings`, add the missing `strings` import from Step 3. If Go says an internal import cannot be found, check that the module path in `go.mod` and the imports match exactly.

## Step 7: Make configuration errors easier to learn from

Once the basic path works, improve validation incrementally:

1. Check that `workingDir` exists and is a directory.
2. Validate environment variable names with a package-level regular expression.
3. Reject empty environment values only if that is a deliberate product rule; empty values are sometimes valid.
4. Add fixtures for a valid workload and common invalid inputs.
5. Decide and document how duplicate YAML keys are handled. Do not assume strict struct decoding catches every duplicate-key case.

Do not add container-only fields such as ports, mounts, images, or CPU limits until the selected backend actually implements them. Unsupported settings must fail clearly.

## Step 8: Add a daemon and client

The CLI currently launches a process itself. The next milestone moves workload ownership into a long-running `braind` daemon so the CLI can return while work continues. Build these pieces in order:

1. Define API request and response structs in `internal/api`.
2. Add daemon endpoints for health and creating/listing/getting/stopping/removing workloads.
3. Listen on a Unix domain socket on Unix-like systems; set restrictive permissions and refuse unsafe socket replacement.
4. Make an HTTP client whose transport dials that socket.
5. Change `brain run` to validate locally, resolve paths, and send a create request to the daemon.
6. Validate the request again in the daemon. The daemon is the trust boundary.
7. Add `brain ps`, `brain logs`, `brain inspect`, `brain stop`, and `brain rm` one at a time.
8. Shut down cleanly on termination signals and remove the socket only when Brain owns it.

Keep API types separate from YAML types. This lets the on-disk user format evolve without accidentally turning Go structs into a permanent API contract.

## Step 9: Add lifecycle state, logs, and recovery

Define explicit states such as `created`, `running`, `exited`, and `failed`. Specify valid transitions before writing lifecycle code. Then:

1. Give each workload a stable ID and a unique name.
2. Persist metadata with atomic writes (write a temporary file, sync as appropriate, then rename).
3. Store stdout and stderr in per-workload log files.
4. Capture exit code and finish time when the process ends.
5. Stop gracefully first, then enforce a timeout and use a forceful termination path where the OS supports it.
6. On daemon restart, reconcile metadata against actual processes. A saved PID by itself is not proof that the original process still exists; PIDs can be reused.
7. Ensure remove only deletes Brain-owned metadata and logs, never user directories.

## Step 10: Add a real container backend

Only start this after process mode and the daemon lifecycle are reliable. The process backend is not a sandbox. For a real container path:

1. Add an OCI runtime adapter, initially invoking a maintained runtime such as `runc`.
2. Generate an OCI bundle and `config.json` from the supported workload fields.
3. Add image reference resolution, authenticated image pulling, digest verification, and root filesystem storage.
4. Run container integration work on Linux. On macOS or Windows, use a Linux VM; do not imply native Linux namespace isolation.
5. Add mounts, resource limits, and networking only when they are implemented end-to-end.
6. Test cleanup and failure paths. Never delete a host source path when cleaning up a container.
7. Document privileges, kernel requirements, limitations, and the trust boundary.

Keep container configuration rejected on platforms or backends that cannot honor it. Do not call Brain a container engine until it can pull or otherwise prepare images, run through an OCI runtime, and manage the promised container settings.

## Step 11: Follow the milestones

Use the original plan’s acceptance criteria as checkpoints:

| Stage | Plan milestone | Result |
|---|---:|---|
| This tutorial’s first slice | 1 | Strict YAML config and process CLI |
| Daemon and socket API | 2 | CLI sends requests to a local daemon |
| Runtime and state | 3 | Named workloads with lifecycle management |
| Logs and restart recovery | 4 | Persistent, inspectable workload state |
| OCI runtime proof of concept | 5 | Linux container launch via an OCI runtime |
| Images and storage | 6 | Resolve/pull and prepare image filesystems |
| Mounts and settings | 7–8 | Supported container configuration |
| Hardening and release | 9 | Reviewed security model and release workflow |

At the end of each stage, update the README with what works, how to run it, and what remains unsupported. Keep the process-mode and container-mode claims distinct.

## A good daily build loop

For each feature, use this sequence:

1. Write down one observable behavior (for example, “unknown YAML fields fail validation”).
2. Add the smallest implementation that provides it.
3. Run the program manually with one valid and one invalid input.
4. Add automated tests for the behavior before moving to the next feature.
5. Update the docs and acceptance checklist.
6. Commit a small, complete change with a descriptive message.

Do not create every directory from the proposed architecture up front. Add a package when its milestone needs it. This keeps the codebase approachable while preserving the architecture’s boundaries.

## Important limitation

This tutorial’s first runnable slice is educational and local. It does not provide daemon-managed lifecycle, persistent state, container isolation, or protection from hostile workloads. The complete build is a multi-milestone systems project; finish and understand each stage before presenting the next stage as supported.
