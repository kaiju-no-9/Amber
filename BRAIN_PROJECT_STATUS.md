# Brain Project Status

This file records the current implementation state against `BRAIN_IMPLEMENTATION_PLAN.md`.

## Current scope

Brain supports ordinary host processes through a local daemon. Process mode does not provide container isolation, namespaces, or cgroup resource limits. The OCI runtime and image pull paths are implemented as proofs of concept; container execution still needs acceptance on Linux.

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

## Milestone 6 — OCI image management proof of concept

The CLI now supports `brain images pull IMAGE` and `brain images list`. Pull resolves tags/digests for the host's Linux architecture, uses the default Docker/Podman credential keychain, caches compressed layers by SHA-256 digest, and reuses cached content. It verifies downloaded compressed layer digests and applies OCI whiteouts while unpacking. Archive traversal, absolute paths, unsafe symlink targets, and hardlinks to non-regular/out-of-root files are rejected.

Container YAML can select either a prepared `rootfs` or an already-pulled `image`, but must set exactly one. `brain run` resolves `image` from the local cache and passes its rootfs to the daemon. An image pull does not happen implicitly during `run`.

This milestone is **implemented but not accepted**. No automated or Linux acceptance tests have been run. Validate registry pulls, corrupt-layer rejection, whiteout behavior, interrupted pull cleanup, rootfs permissions, and execution through `runc` on Linux before relying on it. The extractor deliberately rejects device nodes and unsupported archive entry types; this may exclude some images.

Example container YAML using an image:

```yaml
apiVersion: brain.dev/v1alpha1
kind: Workload
metadata:
  name: container-hello
spec:
  runtime: container
  image: busybox:latest
  command: ["/bin/echo", "hello from OCI"]
  workingDir: /
  env: {}
```

Pull the image first with `brain images pull busybox:latest`. Container execution still requires Linux and `runc`.

## Remaining milestones

Milestones 7–9 and optional 10 are not implemented yet:

1. Container root filesystem management, mounts, and full OCI workload configuration.
2. Linux CPU/memory controls and networking.
3. Security review, Linux integration coverage, packaging, and release hardening.
4. Optional experimental custom runtime using Linux namespaces and cgroups.

OCI execution, namespaces, cgroups, and networking require a Linux development environment. This checkout is being developed on macOS (`darwin/arm64`), so those behaviors need verification inside a Linux VM or host. The process backend must continue to reject container-only settings rather than silently treating them as process settings.

## Local changes

Milestone 6 implementation changes are local in the working tree and have not been pushed to GitHub.
