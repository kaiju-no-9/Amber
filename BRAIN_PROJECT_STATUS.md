# Brain Project Status

This file records the current implementation state against `BRAIN_IMPLEMENTATION_PLAN.md`.

## Current scope

Brain currently runs ordinary host processes through a local daemon. Process mode does not provide container isolation, namespaces, or cgroup resource limits.

## Milestone 4 — Recovery, reliability, and logs

The following implementation is present:

- The daemon reloads persisted workload metadata. A workload saved as `running` or `Starting` is marked `Failed` on daemon startup, with a reason explaining that process recovery is not implemented; the changed state is saved.
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

## Remaining milestones

Milestone 5 and later are not implemented yet:

1. OCI runtime adapter and bundle execution.
2. OCI image resolution, download, digest verification, and safe layer extraction.
3. Container root filesystem, mounts, and OCI configuration.
4. Linux CPU/memory controls and networking.
5. Security review, Linux integration coverage, packaging, and release hardening.
6. Optional experimental custom runtime using Linux namespaces and cgroups.

The OCI runtime, namespaces, cgroups, and networking milestones require a Linux development environment. This checkout is being developed on macOS (`darwin/arm64`), so those features need implementation and verification inside a Linux VM or host. The process backend must continue to reject container-only settings rather than silently treating them as process settings.

## Local changes

Recent Milestone 4 changes are local in the working tree. They have not been pushed to GitHub.
