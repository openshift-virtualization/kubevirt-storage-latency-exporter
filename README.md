# KubeVirt Metrics Exporter

A Prometheus exporter that monitors storage I/O latency for OpenShift Virtualization workloads. It runs as a DaemonSet and combines three collection methods in a single container:

- **QMP subsystem** — connects to each VM's QEMU Monitor Protocol to collect per-disk read/write/flush latency histograms directly from the hypervisor
- **QGA subsystem** — uses the QEMU Guest Agent to collect guest-side I/O latency and IOPS from Windows VMs via Windows Performance Counters (PDH raw counters)
- **eBPF subsystem** — attaches kernel tracepoints and kprobes to capture block and NFS I/O latency across the node, correlated to Kubernetes pods and PersistentVolumeClaims

All three subsystems are independently enabled/disabled and degrade gracefully if one fails to start.

## Metrics

All metrics are exported under the `kubevirt_storage_*` prefix.

### QMP metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `kubevirt_storage_qmp_io_latency_seconds` | histogram | namespace, vmi, node, drive, operation, persistentvolumeclaim | Per-disk I/O latency for KubeVirt VMs |
| `kubevirt_storage_virtqueue_inuse` | gauge | namespace, vmi, node, drive, persistentvolumeclaim, queue | In-flight descriptors in a virtio-blk virtqueue |
| `kubevirt_storage_virtqueue_size` | gauge | namespace, vmi, node, drive, persistentvolumeclaim, queue | Capacity (max descriptors) of a virtio-blk virtqueue |
| `kubevirt_storage_qmp_scrape_errors_total` | counter | | Errors during QMP poll cycles |
| `kubevirt_storage_qmp_last_poll_timestamp_seconds` | gauge | | Unix timestamp of last QMP poll |

### QGA metrics (guest-side, Windows)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `kubevirt_storage_guest_io_latency_avg_seconds` | gauge | namespace, vmi, node, disk, drive, operation, persistentvolumeclaim | Average guest-side I/O latency per disk (read/write), derived via Little's Law |
| `kubevirt_storage_guest_io_operations_per_second` | gauge | namespace, vmi, node, disk, drive, operation, persistentvolumeclaim | Guest-side IOPS per disk (read/write) |
| `kubevirt_storage_qga_scrape_errors_total` | counter | | Errors during QGA poll cycles |
| `kubevirt_storage_qga_last_poll_timestamp_seconds` | gauge | | Unix timestamp of last QGA poll |

The `disk` label contains the raw Windows PhysicalDisk name (e.g. `"1 E:"`). The `drive` and `persistentvolumeclaim` labels are populated by correlating guest disk PCI addresses (from the `guest-get-disks` QGA command) with libvirt domain XML disk aliases (`ua-<volumeName>`), then mapping volume names to PVC claim names via the virt-launcher pod spec. If disk mapping is unavailable (e.g. old guest agent), `drive` and `persistentvolumeclaim` are empty.

The QGA subsystem collects raw Windows Performance Counters (`Win32_PerfRawData_PerfDisk_PhysicalDisk`) via `wmic` executed through the QEMU Guest Agent. Metrics are computed by diffing two successive counter snapshots using Little's Law to derive latency from uint64 queue-length counters (avoiding uint32 overflow in the direct latency counters). VMs without a guest agent (e.g., Linux) or with `guest-exec` blacklisted are automatically detected and excluded after a configurable number of retries.

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

Virtqueue saturation per disk (ratio of in-flight descriptors to capacity):
```promql
kubevirt_storage_virtqueue_inuse / kubevirt_storage_virtqueue_size
```

## Configuration

Shared flags apply to all subsystems. QMP-specific flags are prefixed with `--qmp-`, QGA-specific with `--qga-`, eBPF-specific with `--ebpf-`. All flags can be overridden via environment variables.

### Shared

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--listen-address` | `LISTEN_ADDRESS` | `:8080` | Metrics server listen address |
| `--log-level` | `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `--boundaries` | `BOUNDARIES` | `10000000,100000000,1000000000` | Histogram bucket boundaries in nanoseconds |
| | `NODE_NAME` | (required) | Node name, typically from downward API |
| `--namespaces` | `NAMESPACES` | (all) | Comma-separated namespace filter (applies to both QMP and eBPF) |
| `--cri-socket` | `CRI_SOCKET` | `/run/crio/crio.sock` | CRI socket path for container discovery (shared by QMP and QGA) |

### QMP

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--enable-qmp` | `ENABLE_QMP` | `true` | Enable QMP collection |
| `--qmp-poll-interval` | `QMP_POLL_INTERVAL` | `1m` | VM scrape interval |
| `--qmp-concurrency` | `QMP_CONCURRENCY` | `8` | Max parallel QMP operations |
| `--qmp-timeout` | `QMP_TIMEOUT` | `5s` | Per-operation QMP timeout |
| `--qmp-label-filter` | `QMP_LABEL_FILTER` | | Additional pod label selector |

### QGA

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--enable-qga` | `ENABLE_QGA` | `true` | Enable QGA guest-side I/O collection |
| `--qga-poll-interval` | `QGA_POLL_INTERVAL` | `1m` | Guest metrics poll interval |
| `--qga-timeout` | `QGA_TIMEOUT` | `10` | Per-command QGA timeout (seconds) |
| `--qga-exec-wait` | `QGA_EXEC_WAIT` | `1s` | Wait between guest-exec and guest-exec-status |
| `--qga-retries` | `QGA_RETRIES` | `10` | Max consecutive failures before stopping collection for a VM |
| `--qga-concurrency` | `QGA_CONCURRENCY` | `8` | Max parallel QGA operations |
| `--qga-label-filter` | `QGA_LABEL_FILTER` | | Additional pod label selector for QGA |

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
make build        # generates eBPF bindings and builds the binary (requires clang/llvm)
make test         # runs all tests (requires generated eBPF bindings)
make test-unit    # runs unit tests for non-eBPF packages (works on macOS)
make image        # builds container image with podman (includes full toolchain)
```

To build and push a custom image:

```bash
make push IMAGE=quay.io/myuser/kubevirt-metrics-exporter TAG=v0.1.0
```

## Deploying

### From a release

Download the install manifest from the [latest release](https://github.com/openshift-virtualization/kubevirt-metrics-exporter/releases/latest):

OpenShift:

```bash
oc apply -f https://github.com/openshift-virtualization/kubevirt-metrics-exporter/releases/latest/download/install-openshift.yaml
```

Kubernetes:

```bash
kubectl apply -f https://github.com/openshift-virtualization/kubevirt-metrics-exporter/releases/latest/download/install-kubernetes.yaml
```

### From source

OpenShift:

```bash
make deploy
```

Kubernetes:

```bash
make deploy-kubernetes
```

To deploy with a custom image:

```bash
make deploy IMAGE=quay.io/myuser/kubevirt-metrics-exporter TAG=v0.1.0
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
