# Brain — In-Depth Implementation Plan

> **Project goal:** Build a YAML-driven workload/container engine in Go. A developer writes a `brain.yaml` file and runs `brain run brain.yaml`; Brain validates the desired configuration, asks a local daemon to create and manage the workload, then exposes lifecycle, logs, status, and inspection commands.
>
> **Naming and scope:** In this plan, **Brain** is the project name. The product is a **container engine with a daemon and CLI**. A container runtime is the lower-level component that creates the isolated process environment. The first milestone deliberately includes a normal host-process backend so the end-to-end control plane can be tested before container isolation is added. That process backend is **not a sandbox** and must never be marketed as a secure container runtime.

---

## 1. Product Vision

### 1.1 The experience to build

A user should be able to describe a workload in YAML, start it, and manage it without hand-writing runtime commands.

```bash
brain validate brain.yaml
brain run brain.yaml
brain ps
brain logs web
brain stop web
brain inspect web
```

Brain owns configuration parsing, validation, workload metadata, daemon communication, lifecycle operations, and logs. A runtime backend is responsible for execution details. Container-only settings must not silently degrade into ordinary process behavior.

### 1.2 Target users

- Developers who want a compact, understandable alternative to writing long container commands.
- Systems programmers learning how container engines, daemons, Linux namespaces, and resource controls fit together.
- Developers who want a declarative YAML interface for repeatable local workloads.

### 1.3 Goals for the first useful release

1. YAML configuration with strict validation and useful error messages.
2. A Go CLI and long-running local daemon.
3. CLI-to-daemon communication over a Unix domain socket by default.
4. Named workload lifecycle: create/run, list, inspect, logs, stop, remove.
5. Persistent metadata and predictable cleanup.
6. A runtime adapter interface so the execution backend can evolve independently.
7. A real Linux container backend in a later milestone, preferably OCI-compatible.
8. Tests that prove the behavior and make unsupported features explicit.

### 1.4 Non-goals for the first release

- Multi-host scheduling or a Kubernetes replacement.
- A cloud control plane or multi-tenant service.
- Building images from Dockerfiles.
- Windows/macOS native container isolation. A Linux VM may be used for development on those systems.
- Claiming production-grade isolation from a minimal custom runtime.
- Running arbitrary untrusted workloads with host-process mode.

---

## 2. Key Architectural Decision

There are two distinct implementation tracks. Keep them separate in both documentation and code.

### Track A — Build the engine and daemon first (required)

Implement the CLI, API, YAML schema, metadata store, lifecycle manager, logs, and a process backend. This proves the control-plane architecture end to end with fast tests.

### Track B — Make it a container engine (required for the container claim)

Add image/root-filesystem management and a Linux runtime backend. The most practical first container backend is to generate an OCI runtime bundle and invoke an established runtime such as `runc`. This avoids treating a small collection of syscalls as a secure, standards-compatible runtime.

### Optional learning track — Write a minimal runtime yourself

After the OCI-backed implementation works, add a separate experimental Linux backend to learn namespaces, mounts, capabilities, and cgroups. Keep it behind an explicit `experimental` build/configuration flag until security review and conformance testing are complete. Do not use it for untrusted workloads.

**Recommendation:** finish Track A, then implement the OCI path, and only then attempt the custom runtime. This gets a functional project earlier without confusing a process runner with a container runtime.

---

## 3. Core User Journey

1. User creates `brain.yaml` in a project directory.
2. `brain validate brain.yaml` parses the file, checks the schema, validates paths and values, and reports errors without starting anything.
3. `brain run brain.yaml` reads the file and sends a structured request to the local daemon.
4. The daemon validates the request again; the daemon is the trust boundary and must not assume the CLI is always correct.
5. The engine checks whether a workload with the requested name already exists and applies the documented conflict policy.
6. The engine chooses the requested runtime backend. Unsupported runtime types or options fail before launch.
7. The backend creates/starts the workload and returns a stable workload ID plus runtime-specific metadata.
8. The engine records state and logs, and continuously observes process/container exit.
9. `brain ps`, `brain logs`, `brain inspect`, `brain stop`, and `brain rm` query the daemon.
10. After a daemon restart, Brain reconciles persisted metadata with actual runtime state instead of blindly trusting stored PIDs.

---

## 4. Proposed Repository Layout

Start with a modular monolith. Do not split this into multiple services yet.

```text
brain/
├── cmd/
│   ├── brain/                 # CLI entry point
│   └── braind/                # daemon entry point
├── api/
│   └── openapi.yaml           # API contract, once API shape stabilizes
├── examples/
│   ├── process.yaml           # Safe development example; ordinary process mode
│   └── container.yaml         # Future container example, enabled only when supported
├── internal/
│   ├── config/                # YAML types, decoding, schema validation
│   ├── cli/                   # CLI command handlers and formatting
│   ├── client/                # HTTP client over Unix socket
│   ├── daemon/                # HTTP server, socket ownership, shutdown
│   ├── api/                   # Request/response types and error mapping
│   ├── engine/                # Workload orchestration and lifecycle policy
│   ├── runtime/               # Backend interface and process backend
│   │   ├── process/           # Host process backend; explicitly not a sandbox
│   │   ├── oci/               # OCI bundle generator and runtime adapter
│   │   └── experimental/      # Optional hand-built Linux runtime
│   ├── image/                 # OCI image resolution, downloading, and unpacking
│   ├── storage/               # Persistent state and content-addressed blobs
│   ├── logs/                  # Log files, rotation, retrieval, follow mode
│   ├── resources/             # CPU/memory policy and platform adapters
│   ├── network/               # Port publication and network configuration
│   ├── volumes/               # Bind mounts and managed volumes
│   ├── system/                # OS/platform helpers
│   └── observability/          # Structured logs, metrics, diagnostics
├── docs/
│   ├── architecture.md
│   ├── yaml-reference.md
│   ├── security-model.md
│   ├── runtime-design.md
│   └── development.md
├── scripts/
├── testdata/
├── go.mod
├── Makefile
├── .golangci.yml
├── .gitignore
├── LICENSE
└── README.md
```

Use package boundaries as design boundaries, not as a reason to create empty packages early. Add each package when the relevant milestone requires it.

---

## 5. Language, Dependencies, and Platform Strategy

### 5.1 Language and module

- Language: Go.
- CLI binaries: `brain` and `braind`.
- Choose and pin a supported Go toolchain version when bootstrapping the repository; update it intentionally rather than opportunistically.
- Keep dependencies small and inspect transitive dependencies before introducing them.

### 5.2 Likely dependencies

Choose versions during implementation and pin them in `go.mod`.

- YAML: `gopkg.in/yaml.v3`, with strict/known-field decoding where possible.
- CLI: standard library `flag` for a tiny first version, or a CLI framework if subcommand/help complexity justifies it.
- Runtime spec: OCI runtime-spec Go definitions rather than hand-maintaining a large `config.json` schema.
- Image registry: a maintained OCI/Docker registry client/library, selected after comparing authentication, manifest, digest, and layer support.
- Logging: `log/slog` from the Go standard library at first.
- Tests: standard `testing`, `httptest`, and temporary directories; add property/fuzz tests for parsers and state transitions.

Avoid depending on Docker Engine simply to start a process if the long-term goal is to learn engine/runtime design. Conversely, do not implement registry protocols or all OCI semantics from scratch unless learning those protocols is an explicit goal.

### 5.3 Supported operating systems

- CLI and process backend: developable on common OSes where practical.
- Real Linux container runtime: Linux only.
- Detect unsupported platforms and return a direct message such as `container runtime is supported on Linux only; process mode is available here`.
- For macOS/Windows development, use a Linux VM for container integration tests. Do not imply that Linux namespaces/cgroups provide native isolation on macOS or Windows.

---

## 6. Versioned YAML Configuration

Make the user-facing file declarative and versioned. Do not expose internal Go struct names as a permanent API.

### 6.1 Proposed future schema

This illustrates the intended container schema. Each field must be enabled only after its implementation phase is complete.

```yaml
apiVersion: brain.dev/v1alpha1
kind: Workload
metadata:
  name: web

spec:
  runtime: container
  image: docker.io/library/nginx:stable-alpine
  command: ["nginx", "-g", "daemon off;"]
  workingDir: /
  env:
    APP_ENV: development
  ports:
    - host: 8080
      container: 80
      protocol: tcp
  mounts:
    - source: ./site
      target: /usr/share/nginx/html
      readOnly: true
  resources:
    memory: 256Mi
    cpus: "1.0"
  restartPolicy: "no"
```

For the initial process-only slice, use a smaller schema and reject fields it cannot honor:

```yaml
apiVersion: brain.dev/v1alpha1
kind: Workload
metadata:
  name: hello

spec:
  runtime: process
  command: ["sh", "-c", "while true; do echo hello; sleep 2; done"]
  workingDir: "."
  env:
    APP_ENV: development
  restartPolicy: "no"
```

### 6.2 Schema principles

- `apiVersion`, `kind`, `metadata.name`, and `spec.runtime` are required.
- Names must match a conservative pattern, for example lowercase letters, digits, and internal hyphens; define the exact rule once and share it between CLI and daemon.
- Commands are argument arrays, not shell strings. Running through a shell requires an explicit shell command in the array. Never concatenate arbitrary command fields into a shell string.
- YAML decoding must fail on unknown fields, duplicate keys if the selected decoder can detect them, malformed types, and trailing extra documents.
- Validate sizes, port ranges, path shape, environment variable names, and restart-policy enum values.
- Resolve relative host paths against the YAML file's directory in the CLI before submission, then have the daemon validate the resolved path again. Document that local daemon mode assumes paths refer to the daemon host.
- Never silently ignore a field. An option that is not implemented must return an actionable error.
- Introduce incompatible schema changes through a new API version or a documented migration path.
- Do not store secret values in config snapshots, logs, inspection output, or error strings by default. Environment variables are not a secrets manager.

### 6.3 Validation behavior

`brain validate brain.yaml` should report all independent validation errors in one pass where practical. Output should include a location/field, the issue, and the supported alternative.

Example:

```text
spec.ports[0].container: must be between 1 and 65535
spec.resources.memory: unsupported by runtime "process"
```

Validation must not pull images, start processes, create mounts, or mutate state.

---

## 7. CLI Design

Implement commands in stages. Keep flags consistent and make non-interactive behavior predictable.

| Command | Responsibility | Target phase |
|---|---|---|
| `brain version` | Print CLI version and build metadata | 1 |
| `brain validate FILE` | Parse and validate local YAML | 1 |
| `brain daemon status` | Check daemon health | 2 |
| `brain run FILE` | Create/start workload and print ID/status | 3 |
| `brain ps` | List workloads | 3 |
| `brain inspect NAME_OR_ID` | Print structured configuration/state | 3 |
| `brain logs NAME_OR_ID` | Print available logs | 3 |
| `brain logs -f NAME_OR_ID` | Follow log stream | 4 |
| `brain stop NAME_OR_ID` | Gracefully stop a workload | 3 |
| `brain start NAME_OR_ID` | Start a stopped workload if semantics permit | 4 |
| `brain restart NAME_OR_ID` | Stop and start according to policy | 4 |
| `brain rm NAME_OR_ID` | Remove stopped workload metadata/resources | 4 |
| `brain images pull IMAGE` | Pull a supported OCI image | 6 |
| `brain exec NAME_OR_ID -- COMMAND...` | Execute a command in a running container | later |
| `brain system info` | Show daemon, OS, runtime, storage, and feature support | later |

### CLI rules

- Exit code `0` for success; non-zero for validation, daemon, runtime, or usage failures.
- Write machine-readable JSON output only behind an explicit flag such as `--format json`.
- Human-readable output should remain stable enough for users but not be presented as a scripting API.
- Avoid printing secrets or entire environment maps in normal output.
- If the daemon is missing, say how to start it; do not auto-install or silently start a privileged daemon.
- Every command should have examples in `--help` output and documentation.

---

## 8. Daemon and API Architecture

### 8.1 Daemon responsibilities

`braind` is responsible for accepting local API requests, authenticating access to the local control socket, validating requests, coordinating lifecycle operations, persisting state, tracking workload exits, and shutting down cleanly.

The daemon must not be a thin HTTP wrapper around arbitrary shell commands. It should call explicit engine methods with typed input and predictable lifecycle semantics.

### 8.2 Transport

- Default: Unix domain socket on Linux/macOS.
- Suggested development socket: a path under the user's runtime directory; avoid a global writable socket.
- Socket directory permissions should be restricted, and socket file permissions should be owner-only by default.
- Do not listen on TCP by default. Remote access needs a separate authentication and threat-model project.
- Remove stale socket files only after checking that no active daemon owns the endpoint.
- Provide graceful shutdown and cleanup on SIGINT/SIGTERM.

### 8.3 Initial API contract

Use versioned endpoints; keep the API internal until the first release stabilizes.

| Method | Endpoint | Purpose |
|---|---|---|
| `GET` | `/v1/health` | Liveness/health information |
| `POST` | `/v1/workloads` | Validate and create/start a workload |
| `GET` | `/v1/workloads` | List workloads |
| `GET` | `/v1/workloads/{id}` | Inspect workload details |
| `POST` | `/v1/workloads/{id}/stop` | Stop workload |
| `POST` | `/v1/workloads/{id}/start` | Start previously stopped workload, if supported |
| `POST` | `/v1/workloads/{id}/restart` | Restart workload |
| `DELETE` | `/v1/workloads/{id}` | Remove workload |
| `GET` | `/v1/workloads/{id}/logs` | Retrieve/follow logs |

For API calls, use clear JSON request/response structures. Map domain errors to status codes consistently: malformed input, invalid config, not found, conflict, unsupported feature, runtime failure, and daemon internal error must be distinguishable.

### 8.4 Validation in two places

The CLI validates a local file early for fast feedback. The daemon validates the submitted structured object again because it owns resources and cannot trust callers. Share the pure validation package to avoid duplicated rules, but do not omit daemon validation.

### 8.5 Request safety

- Add request body size limits.
- Set reasonable HTTP server timeouts where compatible with streaming logs.
- Reject unknown API fields where practical.
- Do not expose raw stack traces or sensitive host paths in ordinary user errors.
- Use context cancellation for API requests and lifecycle operations.
- Make create/start operations idempotent or return a documented conflict when a name already exists; do not accidentally create two workloads for retries.

---

## 9. Engine and Runtime Interfaces

The engine should depend on a small backend contract rather than directly on `exec.Cmd` or OCI implementation details.

Conceptual interface:

```go
type Runtime interface {
    Create(ctx context.Context, spec WorkloadSpec) (RuntimeHandle, error)
    Start(ctx context.Context, handle RuntimeHandle) error
    Stop(ctx context.Context, handle RuntimeHandle, timeout time.Duration) error
    Delete(ctx context.Context, handle RuntimeHandle) error
    Inspect(ctx context.Context, handle RuntimeHandle) (RuntimeState, error)
    Logs(ctx context.Context, handle RuntimeHandle, opts LogOptions) (io.ReadCloser, error)
}
```

Treat this as a design sketch, not final code. The exact method set may evolve after the lifecycle state model is defined.

### Responsibilities by layer

- **Config package:** parse and validate user input; no side effects.
- **CLI client:** send requests and display responses; no direct host-process creation.
- **Daemon:** transport, socket security, request lifecycle, shutdown.
- **Engine:** name conflicts, runtime selection, state transitions, recovery, and policy.
- **Runtime backend:** create/start/stop/delete/inspect execution units.
- **Image store:** resolve image references and manage manifests/layers/rootfs.
- **State store:** durable metadata and state transition history.
- **Logs package:** bounded disk storage, retrieval, follow mode, and rotation.

Keep the runtime backend from modifying high-level policy. For example, the engine decides that stop means “send a graceful termination signal, wait for a configured period, then force kill”; the backend performs the OS/runtime-specific work.

---

## 10. Workload Lifecycle and State Model

Define lifecycle semantics before adding many commands.

Suggested state model:

```text
CREATED -> STARTING -> RUNNING -> STOPPING -> STOPPED
    |          |          |            |
    +----------+----------+------------+--> FAILED

STOPPED -> STARTING
STOPPED -> DELETING -> DELETED
FAILED  -> DELETING -> DELETED
```

The exact states may be simplified for the MVP, but never derive state solely from whether a PID field exists.

### Required behavior

- Assign a unique, stable internal ID to each workload. Keep the human-readable name separately.
- Prevent two active workloads from owning the same unique name unless explicit replacement semantics are later implemented.
- Record timestamps for creation, start, finish, stop, and last state transition.
- Record the last exit code and a short failure reason, without leaking secrets.
- Make stop idempotent: stopping an already stopped workload should return a clear result, not crash the daemon.
- Use graceful termination followed by a timeout and force kill where supported.
- Record why a workload stopped: user request, process exit, daemon recovery, failure, or policy action.
- When state storage and runtime state disagree, reconcile using runtime inspection and process identity checks. PIDs can be reused; a persisted PID alone is not a safe identity.
- Deletion must be carefully ordered: stop workload, delete runtime resources, release mounts/network/resources, then remove metadata. Preserve diagnostic information if cleanup partially fails.
- Document restart-policy semantics before implementing automatic restarts. Avoid infinite rapid crash loops; add backoff and a maximum restart rate if restart policies are added.

---

## 11. Persistence and File Layout

Use a configurable state directory. Keep metadata separate from bulky image layers and logs.

Example Linux layout:

```text
$XDG_STATE_HOME/brain/            # user-mode development
├── state.json or db/             # durable metadata
├── workloads/<id>/               # per-workload metadata and runtime files
├── logs/<id>/stdout.log
├── logs/<id>/stderr.log
├── images/                       # later: manifests and content blobs
├── rootfs/                       # later: unpacked root filesystems
└── tmp/                          # temporary files, cleanable on startup
```

### MVP storage choice

Start with atomic JSON metadata files if there are only a few objects and the storage semantics are carefully defined. Write to a temporary file, fsync when needed, and rename atomically. If concurrent updates, filtering, or migrations become cumbersome, move to SQLite with explicit schema migrations. Do not invent a bespoke embedded database.

### Persistence requirements

- Never store state in the repository working directory by default.
- Avoid half-written metadata after a crash.
- Persist a schema version and migrate stored state intentionally.
- Restrict permissions on state, sockets, credentials, and logs.
- Do not serialize live Go implementation details directly; define explicit metadata structs.
- Mark temporary resources so startup recovery can clean abandoned artifacts safely.

---

## 12. Logs and Observability

### Logging behavior

- Capture stdout and stderr separately in the MVP.
- Provide `brain logs NAME` and `brain logs -f NAME`.
- Keep logs outside the image/root filesystem.
- Add configurable rotation or size limits before the tool is used for long-running services.
- Be explicit about whether logs survive workload removal.
- Do not include environment variable values, auth tokens, registry credentials, or raw config secrets in daemon logs.

### Daemon logs

Use structured logs with useful fields such as `request_id`, `workload_id`, `name`, `runtime`, `operation`, `duration_ms`, and `error_class`. Avoid logging every polling cycle.

### Diagnostics

A later `brain system info` should show:

- Brain version and build commit.
- Operating system and architecture.
- Daemon health and socket location.
- Available runtime backends and their versions.
- Whether the host appears to support required Linux features.
- State/log/image storage paths and free-space warnings.

Do not add a remote telemetry service or upload user data by default.

---

## 13. OCI Image and Root Filesystem Design

This section begins the transition from process engine to container engine.

### 13.1 Image reference handling

Support a deliberately small but useful set first:

1. Fully qualified registry/repository/tag.
2. A default registry for familiar shorthand only when documented.
3. Digest-pinned references for reproducibility.
4. Registry credentials from standard credential helpers/files, not YAML literals.

Resolve a tag to a manifest digest, record the digest used, and distinguish the user-provided tag from the immutable content identity. Handle architecture/platform selection explicitly.

### 13.2 Image storage

An OCI image is made of a manifest/config and one or more layers. The implementation should:

- Fetch manifests and layer blobs through a maintained library.
- Verify content digests before accepting blobs.
- Store blobs content-addressably so layers are not duplicated unnecessarily.
- Track which image manifests refer to which blobs.
- Unpack layers into a root filesystem using a well-tested implementation where possible.
- Enforce extraction path safety. Reject archive entries that escape the target directory through `..`, absolute paths, or unsafe symlink combinations.
- Handle whiteout semantics correctly; do not assume a layer is just a directory copy.
- Track partial downloads and remove them safely.
- Expose `brain images list`, `brain images inspect`, and eventually `brain images rm`.

### 13.3 Storage driver progression

- First usable implementation: unpack into per-image/per-container directories with conservative disk cleanup.
- Later optimization: use overlayfs where supported and correctly configured.
- Never require overlayfs before a simple, testable rootfs implementation works.
- Report disk usage and image-layer reference counts before adding garbage collection.

### 13.4 Image acceptance tests

- Pull and unpack a small public image on Linux.
- Validate a recorded manifest/layer digest against expected values.
- Reject corrupt blobs and malformed layer archives.
- Handle repeated pulls without duplicating the same content blobs.
- Run an image with a known command and verify its exit code and logs.
- Recover safely from interrupted pulls.

---

## 14. OCI Runtime Backend

### 14.1 Preferred initial backend

Generate an OCI runtime bundle and invoke an installed `runc` binary. The OCI Runtime Specification standardizes the container configuration and lifecycle model; `runc` is a reference implementation of that contract. Brain remains responsible for the user-facing YAML, image storage, daemon/API, metadata, and higher-level lifecycle policy.

### 14.2 Runtime bundle

A bundle typically contains:

```text
bundle/<workload-id>/
├── config.json
└── rootfs/
```

Use maintained OCI Go structs to produce `config.json`. Do not write it with string concatenation. Validate generated output, including absolute working directory paths and a real rootfs directory.

### 14.3 Runtime adapter tasks

- Detect runtime binary and version.
- Use `exec.CommandContext` with argument arrays; do not run a shell.
- Pass a controlled environment.
- Capture stdout/stderr and distinguish runtime errors from workload exit failures.
- Call create/start/state/kill/delete operations with validated workload IDs.
- Use a consistent runtime root/state directory.
- Reconcile containers after daemon restart by asking the runtime for state.
- Ensure runtime commands cannot be injected through names, IDs, image references, or paths.
- Time out operations that otherwise might hang, while respecting long-running workload semantics.

### 14.4 Security baseline before labeling it a container engine

At minimum, plan and test:

- Separate PID, mount, UTS, IPC, and network namespaces as appropriate.
- A container root filesystem and controlled mount table.
- Dropped Linux capabilities by default.
- No-new-privileges where compatible.
- A restrictive seccomp profile where supported.
- Read-only rootfs option.
- Correct ownership and user mappings, especially for rootless mode.
- cgroup resource limits when configured.
- Masking or restricting sensitive host paths.
- Safe handling of `/proc`, `/sys`, `/dev`, and pseudo-filesystems.
- Clear privilege requirements and failure behavior.

Use the OCI specification and kernel documentation as implementation references; they describe many details that a custom implementation would otherwise overlook.

---

## 15. Optional Custom Linux Runtime Track

Treat this as an advanced, isolated project track—not a shortcut around OCI implementation details.

### Learning objectives

Understand process isolation, namespaces, mount propagation, root filesystem setup, privilege dropping, cgroups v2, and process lifecycle on Linux.

### Suggested sequence

1. **Linux-only experiment:** launch one process in a new user/UTS/PID/mount namespace where the host allows it. Use an intentionally harmless test program.
2. **Root filesystem:** enter a prepared rootfs with a controlled mount layout. Ensure cleanup works after normal exit and failure.
3. **PID namespace and init behavior:** understand reaping orphaned children and signal forwarding; avoid leaving zombies.
4. **Capabilities and privilege dropping:** drop unnecessary capabilities, set UID/GID, set `no_new_privs`, and make privileged setup boundaries explicit.
5. **cgroups v2:** create/manage a dedicated cgroup, set CPU/memory limits, and remove it after exit. Detect controllers and delegation permissions instead of assuming they exist.
6. **Networking:** only after process and filesystem isolation work. A rootless user-mode network helper is usually less risky for a first implementation than managing host bridges and firewall rules.
7. **Seccomp/LSM:** apply policy and add tests showing prohibited operations fail as expected.
8. **Conformance and threat review:** compare behavior with OCI expectations, test adversarial path/mount cases, and document unsupported behavior.

### Explicit limits

- A proof-of-concept runtime is not secure merely because it uses namespaces and cgroups.
- Never make it the default for untrusted workloads without a security review, adversarial tests, and careful handling of privilege transitions.
- Keep the experimental backend behind an explicit selection such as `runtime: experimental-linux` and print a warning.
- Do not run the engine itself as root unless a specific operation requires it; isolate any privileged helper and keep its API narrow.

---

## 16. Networking, Ports, Mounts, and Resources

Implement each feature as a separate milestone. YAML fields should not become available until the backend can enforce them.

### 16.1 Ports

First support TCP publishing for a single host port to a container port. Define how conflicts are reported, how wildcard bindings work, and whether ports bind to loopback or all interfaces by default. Prefer loopback-only exposure as the safer developer default.

For rootless/container networking, evaluate a maintained user-mode networking helper. Implementing a host bridge, veth pairs, NAT, and firewall rules is a separate systems project and should not be smuggled into the first runtime milestone.

### 16.2 Mounts and volumes

- Begin with bind mounts only.
- Resolve host source paths and verify they exist according to the selected policy.
- Validate destination paths as absolute paths inside the container.
- Support read-only mounts first; writable mounts should be explicit.
- Reject device mounts and dangerous propagation options until separately designed.
- Ensure cleanup never recursively deletes a host path that the user supplied as a bind source.
- Add named managed volumes only after their ownership and deletion semantics are well-defined.

### 16.3 Resources

- Memory limits: support cgroups v2 where available; validate units and report permission/controller errors.
- CPU limit: define whether values mean CPU quota, shares/weight, or fractional core count, then map clearly to cgroup controls.
- Do not promise a limit if the kernel or runtime cannot enforce it.
- Verify limits from inside/outside the container during integration tests.

---

## 17. Security Model and Threat Boundaries

Document these boundaries in `docs/security-model.md` before enabling container mode.

### MVP process backend

A process launched in process mode can normally access the host filesystem, network, and permissions of the daemon's user. Therefore:

- Only use trusted commands and configuration.
- Never call it a sandbox.
- Never execute arbitrary third-party projects automatically.
- Do not expose a TCP daemon endpoint.

### Daemon and socket

- Default to a user-owned socket with restricted directory and socket permissions.
- Do not run as root by default.
- If a privileged mode is added, separate privileged operations into a small helper with narrow typed requests.
- Avoid a general-purpose endpoint like “run arbitrary command as root.”
- Validate every incoming request and limit request sizes.
- Ensure a less-privileged user cannot replace the socket path or state directories with symlinks to sensitive locations.

### Container and filesystem security

- Use safe archive extraction and guard against path traversal, symlink escapes, and image-layer attacks.
- Never mount the host root or container runtime socket by default.
- Apply least privilege: non-root user where possible, dropped capabilities, read-only root filesystem where compatible, and limited resources.
- Do not pass host credentials into containers automatically.
- Keep image provenance and digest information visible.
- Be careful with bind mount deletion/cleanup and symlink resolution races.

### Secret handling

- Avoid embedding credentials in YAML checked into Git.
- Use registry credential helpers or permission-restricted credential files.
- Redact values in debug logs and inspection output.
- Explain that environment variables may be visible to processes or inspection tools and are not a full secrets solution.

---

## 18. Testing Strategy

Testing is part of each phase, not a final cleanup task.

### Unit tests

- YAML decoding: valid file, missing fields, unknown fields, duplicate keys, invalid types, multiple documents, invalid values.
- Name and path validation.
- Resource-unit parsing and port validation.
- State transition rules and conflict handling.
- API error mapping.
- Persistence atomicity, corruption handling, and migrations.
- Runtime adapter command argument construction.
- Log rotation and follow behavior.

### Integration tests

- Start daemon on a temporary socket and state directory.
- Run CLI validation and create/list/inspect/log/stop/remove flows.
- Start short-lived and long-lived test processes; verify exit code and timestamps.
- Stop a process gracefully and verify escalation after timeout.
- Kill/restart the daemon and verify reconciliation behavior.
- Exercise simultaneous API requests and workload-name conflicts.
- On Linux CI, pull and run a tiny test image through the OCI backend.
- Test runtime unavailable, permission denied, insufficient disk, invalid image, malformed config, and port conflict cases.

### Fuzzing and adversarial tests

- Fuzz YAML/schema parsing and name validation.
- Test image-layer archives with absolute paths, `..`, weird symlink chains, hard links, huge file sizes, and malformed headers.
- Test config values that resemble flags or contain spaces/newlines to ensure they remain literal arguments.
- Test stale state, PID reuse scenarios, and partial cleanup failures.
- Test that unsupported options fail instead of silently being ignored.

### CI gates

For every pull request:

```bash
gofmt -w .
go test ./...
go vet ./...
go test -race ./...
```

Use linting and vulnerability scanning in CI as appropriate. Run Linux runtime integration tests in a dedicated Linux job with explicitly documented privileges. Avoid making ordinary unit tests require root.

---

## 19. Milestones and Acceptance Criteria

Each milestone should produce a small demonstrable result. Do not start the next major subsystem until the current one is testable.

### Milestone 0 — Architecture and scope

**Build:** `docs/architecture.md`, runtime/backend decision, threat model draft, initial YAML contract.

**Done when:**
- Process mode and container mode are clearly distinguished.
- Unsupported features and platform expectations are written down.
- The first end-to-end user journey is agreed upon.

### Milestone 1 — Go project skeleton and configuration

**Build:** Go module, `brain version`, YAML types, strict decoder, validator, sample process config.

**Done when:**
- Valid example passes.
- Missing/unknown/invalid fields return actionable errors.
- Validation has no side effects.
- Unit tests cover schema and boundary cases.

### Milestone 2 — CLI and daemon shell

**Build:** `braind`, local socket listener, `/v1/health`, CLI client, graceful shutdown.

**Done when:**
- CLI can determine whether daemon is running.
- Socket directory/file permissions are restricted.
- Stale socket handling is safe.
- API client errors explain how to recover.

### Milestone 3 — Engine state and process backend

**Build:** runtime interface, process backend, create/run, list, inspect, stop, remove, basic logs, metadata store.

**Done when:**
- A YAML file runs an explicit command and lists the workload.
- Output/error and exit status are captured.
- Stopping and removing are repeatable and idempotent where appropriate.
- Documentation explicitly says process mode is not isolated.

### Milestone 4 — Recovery, reliability, and logs

**Build:** durable state versioning, crash recovery, follow logs, graceful/force stop, log rotation, conflict semantics.

**Done when:**
- Daemon restart does not falsely report every workload as running.
- A persisted PID is never accepted as sufficient proof of workload identity.
- Interrupted operations leave recoverable or clearly diagnosable state.
- Integration tests pass repeatedly.

### Milestone 5 — OCI runtime adapter proof of concept

**Build:** bundle generation, runtime binary detection, create/start/state/kill/delete integration using a prepared rootfs.

**Done when:**
- A minimal OCI bundle starts and stops on Linux.
- All runtime calls use typed/validated arguments.
- Runtime errors are surfaced clearly.
- Cleanup is verified after success and failure.

### Milestone 6 — Image management

**Build:** image reference parsing, manifest resolution, blob download/digest verification, safe layer unpacking, image listing.

**Done when:**
- Brain can pull a small public image by tag and digest.
- Corrupt blobs and unsafe archives are rejected.
- Repeated pulls reuse content-addressed blobs.
- The extracted rootfs runs through the OCI adapter.

### Milestone 7 — Container configuration and storage

**Build:** command, working directory, environment, read-only rootfs, bind mounts, metadata inspection, safe cleanup.

**Done when:**
- Supported YAML fields map deterministically to OCI config.
- Unsupported mount options fail validation.
- Host paths are never removed as part of container cleanup.
- Container logs and exit codes work end-to-end.

### Milestone 8 — Resource controls and networking

**Build:** memory/CPU limits and a narrowly scoped port-publishing implementation.

**Done when:**
- Limits are verifiably enforced where supported.
- Missing cgroup controllers/permissions produce an error instead of a false success.
- Port conflicts are reported and cleaned up.
- Exposure defaults are documented and tested.

### Milestone 9 — Security review and release hardening

**Build:** security documentation, adversarial tests, daemon permission review, runtime capability defaults, packaging and versioning.

**Done when:**
- A reviewer can identify the trust boundary and privileged operations.
- No known “process mode is a sandbox” messaging remains.
- Integration tests pass on a supported Linux environment.
- Install/build/run instructions work from a clean checkout.

### Optional Milestone 10 — Experimental custom runtime

**Build:** Linux-specific namespace/rootfs/cgroup implementation behind a separate backend.

**Done when:**
- The feature is clearly labeled experimental.
- It passes tests for isolation properties it claims to implement.
- Unsupported kernel features fail closed.
- A documented security review and comparison with OCI behavior exist.

---

## 20. Development Workflow

### Initial local setup

```bash
git init
go mod init github.com/<username>/brain
mkdir -p cmd/brain cmd/braind internal/config internal/engine internal/runtime/process internal/daemon internal/client examples docs
```

Use the actual repository path when publishing. Add CI early. Build the smallest vertical slice rather than creating the entire directory tree with empty placeholder packages.

### Recommended feature branch sequence

1. `feat/config-validation`
2. `feat/daemon-socket-api`
3. `feat/process-runtime`
4. `feat/state-recovery-and-logs`
5. `feat/oci-runtime-adapter`
6. `feat/oci-image-store`
7. `feat/container-mounts-resources`
8. `feat/networking`
9. `chore/security-hardening-release`

Each branch should include code, tests, and documentation. Keep runtime privileges and root-required integration tests clearly isolated.

### Definition of done for any feature

- The YAML/API contract is documented.
- Validation and failure behavior are defined.
- Unit tests cover expected and invalid inputs.
- Integration tests cover the user-visible path where relevant.
- Logs/errors are useful without leaking secrets.
- Cleanup/recovery behavior is considered.
- Unsupported platforms and permission requirements are explicit.

---

## 21. First Vertical Slice: Exact Task Checklist

This is the first implementation sprint. It should result in a demonstrable program—not just a design skeleton.

### A. Configuration

- [ ] Define `apiVersion`, `kind`, `metadata.name`, `spec.runtime`, `spec.command`, `spec.workingDir`, and `spec.env`.
- [ ] Implement strict YAML decoding and version validation.
- [ ] Validate workload names and non-empty command arrays.
- [ ] Resolve the working directory relative to the YAML file.
- [ ] Reject fields that process mode cannot implement.
- [ ] Add valid and invalid fixture YAML files.

### B. CLI

- [ ] Implement `brain version`.
- [ ] Implement `brain validate FILE`.
- [ ] Implement `brain run FILE` using a daemon API client.
- [ ] Implement `brain ps`, `brain logs NAME`, `brain stop NAME`, `brain inspect NAME`, and `brain rm NAME` progressively.
- [ ] Standardize exit codes and human-readable errors.

### C. Daemon

- [ ] Add `braind` flags for socket path and state directory.
- [ ] Create a restricted local socket safely.
- [ ] Implement `/v1/health` and typed workload endpoints.
- [ ] Revalidate create requests.
- [ ] Shut down on signals and close the socket cleanly.

### D. Engine and process mode

- [ ] Define runtime and workload state types.
- [ ] Implement process launch with argument arrays—no implicit shell.
- [ ] Capture stdout/stderr to per-workload files.
- [ ] Store workload metadata atomically.
- [ ] Implement stop with graceful signal and timeout handling on supported platforms.
- [ ] Track exit code and final state.
- [ ] Add safe remove/cleanup semantics.

### E. Tests and demo

- [ ] Unit-test config validation and state transitions.
- [ ] Start a daemon in a temporary directory during integration tests.
- [ ] Run a long-lived test command, verify `ps`, retrieve logs, stop, and remove it.
- [ ] Test malformed YAML, unsupported feature, duplicate name, missing daemon, and cleanup failure.
- [ ] Record a short terminal demo in the README after the tests pass.

**Sprint acceptance test:** From a clean checkout, the user can build Brain, start `braind`, validate an example YAML file, run a harmless local process, observe its state/logs, stop it, and remove it. Every limitation is stated accurately.

---

## 22. Definition of a Real First Release

Do not call the first process-only milestone a container runtime. It is a useful **YAML-driven process engine** and the control plane for Brain.

Brain can reasonably be called a **container engine** after it can, at minimum, resolve/pull OCI images, prepare container root filesystems, run workloads through an OCI runtime, manage lifecycle and state, and provide the supported configuration semantics end to end.

Call it a **custom container runtime** only if Brain itself implements the low-level execution/isolation responsibilities rather than delegating them to `runc` or another runtime. Even then, reserve “production-ready” claims until security, conformance, recovery, and adversarial testing support them.

---

## 23. Reference Documentation

Use official specifications and operating-system documentation as design references. These are references, not a replacement for testing the exact runtime behavior Brain supports.

- [Open Container Initiative Runtime Specification](https://github.com/opencontainers/runtime-spec) — container lifecycle and runtime configuration model.
- [OCI Runtime Specification: Configuration](https://github.com/opencontainers/runtime-spec/blob/main/config.md) — process, root filesystem, mounts, and configuration fields.
- [OCI Runtime Specification: Linux configuration](https://github.com/opencontainers/runtime-spec/blob/main/config-linux.md) — namespaces, cgroups, capabilities, and Linux-specific settings.
- [Linux kernel cgroup v2 documentation](https://docs.kernel.org/admin-guide/cgroup-v2.html) — resource control behavior and controller interfaces.

Review the versions actually supported by the selected runtime and the target distribution. Keep a note in `docs/runtime-design.md` listing which OCI/runtime versions have been tested.

---

## Final Recommendation

Build Brain as a **Go container engine with a local daemon, CLI, versioned YAML configuration, persistent workload state, and a pluggable runtime backend**. First complete the end-to-end process-mode slice to validate the architecture. Then add OCI image handling and an OCI runtime adapter on Linux. Once that works, build a separate experimental custom runtime if learning kernel-level container internals is a major goal.

This sequence produces an honest, usable result at every stage, keeps the project from becoming an oversized one-shot rewrite of Docker, and makes the hardest systems concepts testable one at a time.
