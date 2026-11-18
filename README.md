<div align="center">

<h1 align="center"><code>Brain — workloads, under control</code></h1>

<samp>
Define a workload. Send it to the local daemon. Follow its lifecycle.
</samp>

<br />

<img src="https://img.shields.io/badge/Go-1.27%2B-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go 1.27 or later" />
<img src="https://img.shields.io/badge/control%20plane-local-343A40?style=for-the-badge" alt="Local control plane" />
<img src="https://img.shields.io/badge/runtime-host%20process-E6A23C?style=for-the-badge" alt="Current runtime: host process" />

</div>

<br />

<p align="center"><samp>ꜱᴍᴀʟʟ ᴇɴɢɪɴᴇ · ᴄʟɪ + ᴅᴀᴇᴍᴏ · ᴏᴄɪ ɪꜱ ᴛʜᴇ ᴅɪʀᴇᴄᴛɪᴏɴ</samp></p>

---

## <samp>› what is brain?</samp>

Brain is a Go project for understanding the pieces behind a container engine: a CLI, a local control API, workload lifecycle management, persistent state, logs, and a runtime adapter boundary.

Today, Brain runs **ordinary host processes** through braind and has an OCI runtime proof of concept that uses `runc` on Linux. Container execution has not yet been accepted on Linux and must not be treated as a production security boundary.

<p align="center"><samp>⚠ process mode is not a container or a security boundary. Run only workloads you trust.</samp></p>

## <samp>› features</samp>

| Capability | What Brain does today |
|---|---|
| **YAML workloads** | Validates a versioned workload schema, command, runtime, name, and environment keys. |
| **Local daemon** | Exposes a versioned HTTP API over an owner-only Unix socket. |
| **Workload lifecycle** | Starts, lists, inspects, stops, and removes named host processes. |
| **Logs** | Captures stdout and stderr separately, rotates each stream, and supports follow mode. |
| **Persistent metadata** | Saves workload state and marks previously running workloads failed after restart, since process reattachment is not implemented. |

## <samp>› workload flow</samp>

<div align="center">
  <img src="assets/brain-container-flow.png" width="100%" alt="Brain container runtime architecture flow chart" />
</div>

<p align="center"><samp>CURRENT: CLI → daemon → process &nbsp; · &nbsp; PLANNED: registry, OCI runtime, networking, volumes</samp></p>

For a process workload, the CLI validates YAML and calls the daemon over an owner-only Unix socket. The manager starts the host process, records lifecycle state, and captures its logs. The OCI proof of concept can pull Linux images into a local cache, prepare a root filesystem and bundle, then delegate container setup to an OCI runtime on Linux.

## <samp>› technology</samp>

- **Go** for the CLI, daemon, and workload manager
- **YAML v3** for strict workload configuration decoding
- **HTTP over Unix sockets** for local CLI-to-daemon requests
- **Go os/exec** for the current direct host-process backend
- **JSON state and local rotating log files** for persistence

## <samp>› quick start</samp>

Requires **Go 1.27+**.

~~~bash
# Build the CLI and daemon
go build -o brain ./cmd/brain
go build -o braind ./cmd/braind
~~~

Start the daemon from the repository root in one terminal:

~~~bash
./braind
~~~

In a second terminal, also from the repository root:

~~~bash
./brain validate examples/hello.yml
./brain run examples/hello.yml
./brain ps
./brain logs hello
~~~

Stop and remove it when you’re done:

~~~bash
./brain stop hello
./brain rm hello
~~~

The daemon creates `./brain.sock` in its working directory and stores workload metadata under the user cache directory. Run the CLI from that same directory so it can find the socket. Ctrl+C shuts the daemon down and stops managed processes.

## <samp>› describe a workload</samp>

~~~yaml
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
~~~

Commands are argument arrays and run directly; Brain does not invoke a shell implicitly. Set an explicit shell command when shell behavior is needed.

For the Linux OCI proof of concept, first pull an image into Brain's per-user cache:

~~~bash
brain images pull busybox:latest
brain images list
~~~

Then use `image: busybox:latest` instead of `rootfs:` in a `runtime: container` workload and run it on Linux with `runc` available. Image pulls select the host architecture's Linux image. The pull is explicit; `brain run` uses the cached image and does not contact a registry.

## <samp>› commands</samp>

| Command | Action |
|---|---|
| `brain health` | Check daemon health |
| `brain validate FILE` | Parse and validate YAML |
| `brain run FILE` | Create and start a workload |
| `brain ps` | List workloads |
| `brain get NAME` | Inspect workload ID and state |
| `brain logs NAME` | Print captured output |
| `brain logs -f NAME` | Follow output (one-second polling) |
| `brain stop NAME` | Interrupt; force-kill after five seconds if needed |
| `brain rm NAME` | Remove a stopped workload and its logs |
| `brain images pull IMAGE` | Resolve and cache an OCI image and unpack its rootfs |
| `brain images list` | List locally cached image references |

## <samp>› build path</samp>

~~~text
YAML + CLI + local API + process lifecycle + logs      ✓ current
OCI image pull + digest verification + root filesystem → implemented; Linux acceptance pending
OCI bundle + Linux runtime                            → proof of concept; Linux acceptance pending
Mounts / resources / networking / security hardening → planned
Networking + mounts + Linux integration hardening     → planned
~~~

Container execution, namespaces, cgroups, and networking require Linux verification. Brain is a learning project and not a production container engine or Docker replacement.


<p align="center"><samp>Go · YAML · Unix sockets · OCI proof of concept</samp></p>
