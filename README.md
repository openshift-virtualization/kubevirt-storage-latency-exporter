# KubeVirt Storage Latency Exporter

A Prometheus exporter that monitors storage I/O latency for OpenShift Virtualization workloads. It runs as a DaemonSet and combines two collection methods in a single container:

- **QMP subsystem** — connects to each VM's QEMU Monitor Protocol to collect per-disk read/write/flush latency histograms directly from the hypervisor
- **eBPF subsystem** — attaches kernel tracepoints and kprobes to capture block and NFS I/O latency across the node, correlated to Kubernetes pods and PersistentVolumeClaims

Both subsystems are independently enabled/disabled and degrade gracefully if one fails to start.

## Metrics

All metrics are exported under the `kubevirt_storage_*` prefix.

### QMP metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `kubevirt_storage_qmp_io_latency_seconds` | histogram | namespace, vmi, node, drive, operation, persistentvolumeclaim | Per-disk I/O latency for KubeVirt VMs |
| `kubevirt_storage_qmp_scrape_errors_total` | counter | | Errors during QMP poll cycles |
| `kubevirt_storage_qmp_last_poll_timestamp_seconds` | gauge | | Unix timestamp of last QMP poll |

### eBPF metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `kubevirt_storage_block_io_latency_seconds` | histogram | node, namespace, persistentvolumeclaim, pod, operation | Block I/O latency attributed to pod volumes |
| `kubevirt_storage_system_block_io_latency_seconds` | histogram | node, device, operation | Block I/O latency for system/unresolved devices |
| `kubevirt_storage_nfs_io_latency_seconds` | histogram | node, namespace, persistentvolumeclaim, pod, operation | NFS I/O latency (tracepoint-based) |
| `kubevirt_storage_nfs_vfs_latency_seconds` | histogram | node, namespace, persistentvolumeclaim, pod, operation | NFS VFS call latency (kprobe-based) |
| `kubevirt_storage_subsystem_active` | gauge | subsystem | Whether an eBPF subsystem loaded successfully (1) or not (0) |

### Example PromQL

P99 write latency per VMI:
```promql
histogram_quantile(0.99,
  sum by (vmi, le) (
    rate(kubevirt_storage_qmp_io_latency_seconds_bucket{operation="write"}[5m])
  )
)
```

## Configuration

Shared flags apply to both subsystems. QMP-specific flags are prefixed with `--qmp-`, eBPF-specific with `--ebpf-`. All flags can be overridden via environment variables.

### Shared

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--listen-address` | `LISTEN_ADDRESS` | `:8080` | Metrics server listen address |
| `--log-level` | `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `--boundaries` | `BOUNDARIES` | `10000000,100000000,1000000000` | Histogram bucket boundaries in nanoseconds |
| | `NODE_NAME` | (required) | Node name, typically from downward API |
| `--namespaces` | `NAMESPACES` | (all) | Comma-separated namespace filter (applies to both QMP and eBPF) |

### QMP

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--enable-qmp` | `ENABLE_QMP` | `true` | Enable QMP collection |
| `--qmp-poll-interval` | `QMP_POLL_INTERVAL` | `1m` | VM scrape interval |
| `--qmp-concurrency` | `QMP_CONCURRENCY` | `8` | Max parallel QMP operations |
| `--qmp-timeout` | `QMP_TIMEOUT` | `5s` | Per-operation QMP timeout |
| `--qmp-cri-socket` | `QMP_CRI_SOCKET` | `/run/crio/crio.sock` | CRI-O socket path |
| `--qmp-label-filter` | `QMP_LABEL_FILTER` | | Additional pod label selector |

### eBPF

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--enable-ebpf` | `ENABLE_EBPF` | `true` | Enable eBPF collection |
| `--enable-ebpf-block` | `ENABLE_EBPF_BLOCK` | `true` | Enable block I/O tracing |
| `--enable-ebpf-nfs` | `ENABLE_EBPF_NFS` | `true` | Enable NFS tracing |
| `--enable-ebpf-nfs-kprobe` | `ENABLE_EBPF_NFS_KPROBE` | `false` | Enable NFS VFS kprobe tracing |
| `--ebpf-scan-interval` | `EBPF_SCAN_INTERVAL` | `30` | Device-to-pod resolution interval (seconds) |
| `--ebpf-proc-path` | `EBPF_PROC_PATH` | `/proc` | Host proc filesystem path |

## Building

Prerequisites: Go 1.25+, clang, llvm, libbpf-devel

```bash
make build        # generates eBPF bindings and builds the binary
make test         # runs all tests
make image        # builds container image with podman
```

To build and push a custom image:

```bash
make push IMAGE=quay.io/myuser/kubevirt-storage-latency-exporter TAG=v0.1.0
```

## Deploying

### From a release

Download the install manifest from the [latest release](https://github.com/openshift-virtualization/kubevirt-storage-latency-exporter/releases/latest):

```bash
# OpenShift
oc apply -f https://github.com/openshift-virtualization/kubevirt-storage-latency-exporter/releases/latest/download/install-openshift.yaml

# Kubernetes
kubectl apply -f https://github.com/openshift-virtualization/kubevirt-storage-latency-exporter/releases/latest/download/install-kubernetes.yaml
```

### From source

```bash
# OpenShift
make deploy

# Kubernetes
make deploy-kubernetes
```

To deploy with a custom image:

```bash
make deploy IMAGE=quay.io/myuser/kubevirt-storage-latency-exporter TAG=v0.1.0
```

The OpenShift variant includes SecurityContextConstraints, worker node selector, and PodMonitor for Prometheus scraping.

### Required capabilities

| Capability | Reason |
|-----------|--------|
| `hostPID` | Access VM virtqemud sockets via `/proc/<pid>/root/` |
| `SYS_PTRACE` | Traverse `/proc/<pid>/root/` of other containers |
| `DAC_OVERRIDE` | Connect to virtqemud socket owned by qemu UID |
| `BPF` | Load and attach eBPF programs |
| `PERFMON` | Attach to kernel tracepoints and kprobes |
| `SYS_RESOURCE` | Increase eBPF map memory limits |
