# Brain Project Status

This file records the current implementation state against `BRAIN_IMPLEMENTATION_PLAN.md`.

## Current scope

Brain supports ordinary host processes through a local daemon. Process mode does not provide container isolation, namespaces, or cgroup resource limits. A prepared-rootfs OCI path is now wired in code, but it is Linux-only and has not been run on Linux.

## Milestone 4 — Recovery, reliability, and logs

The following implementation is present:

- The daemon reloads persisted workload metadata. A process workload saved as `running` or `Starting` is marked `Failed` on restart because process recovery is not implemented; the changed state is saved.
- `brain logs -f NAME` polls the daemon once per second and can be stopped with Ctrl+C.
- Stop sends an interrupt, waits up to five seconds, and then force-kills the process.
- The daemon stops managed processes during orderly shutdown, so they are not intentionally left running without a manager.
- Each stdout and stderr stream is limited to one active 10 MiB log and one 10 MiB rotated backup.
- Duplicate workload names return a conflict. Removing a workload requires it to be stopped.
- The remove API returns HTTP 204, matching the CLI client's expected response.

The two binaries build on macOS with:

```bash
GOCACHE=/tmp/stow-go-build-cache go build -o /tmp/brain-next ./cmd/brain
GOCACHE=/tmp/stow-go-build-cache go build -o /tmp/braind ./cmd/braind
```

Automated and manual acceptance tests have not been run for these recent changes. In particular, verify follow mode across log rotation and verify that a workload ignoring interrupts is killed after the timeout before treating this milestone as fully accepted.

## Milestone 5 — OCI runtime adapter proof of concept

The code path now:

- Accepts `runtime: container` and a prepared `rootfs` directory in YAML.
- Resolves the rootfs relative to the YAML file and checks that it is a directory.
- Generates an OCI `config.json` with the OCI runtime-spec Go types.
- Invokes `runc` for create, start, state, stop signals, and delete.
- Tracks the runtime state and reconciles active container records when the daemon restarts and `runc` is available.
- Rejects container startup when the daemon is not on Linux or cannot configure `runc`.
- Sets `noNewPrivileges` in the generated OCI process configuration; the workload still runs as UID 0 and this proof of concept has not had a security review.

Container workload YAML for this proof of concept has this shape; `rootfs` must point to an existing Linux root filesystem directory:

```yaml
apiVersion: brain.dev/v1alpha1
kind: Workload
metadata:
  name: container-hello
spec:
  runtime: container
  rootfs: ./rootfs
  command: ["/bin/echo", "hello from OCI"]
  workingDir: /
  env: {}
```

This milestone is **implemented but not accepted**. Builds pass on macOS, but OCI container execution, `runc` lifecycle behavior, and runtime recovery still need Linux acceptance checks. Container log capture is not implemented in this proof of concept.

## Remaining milestones

Milestones 6–9 and optional 10 are not implemented yet:

1. OCI image resolution, download, digest verification, and safe layer extraction.
2. Container root filesystem management, mounts, and full OCI workload configuration.
3. Linux CPU/memory controls and networking.
4. Security review, Linux integration coverage, packaging, and release hardening.
5. Optional experimental custom runtime using Linux namespaces and cgroups.

OCI execution, namespaces, cgroups, and networking require a Linux development environment. This checkout is being developed on macOS (`darwin/arm64`), so those behaviors need verification inside a Linux VM or host. The process backend must continue to reject container-only settings rather than silently treating them as process settings.

## Local changes

Recent Milestone 4 changes are local in the working tree. They have not been pushed to GitHub.
