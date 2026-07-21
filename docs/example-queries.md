# Example PromQL Queries

Per-metric query reference for kubevirt-metrics-exporter. All rate windows use `[5m]` — widen to `[15m]` or `[1h]` for trending, narrow to `[2m]` for live debugging.

---

## QMP subsystem

### `kubevirt_vmi_storage_io_latency_seconds`

Per-disk I/O latency histogram collected from QEMU's block backend. Labels: `namespace`, `name`, `node`, `pod`, `disk`, `persistentvolumeclaim`, `operation` (read/write/flush).

**P99 write latency per VM and disk**
```promql
histogram_quantile(0.99,
  sum by (namespace, name, disk, le) (
    rate(kubevirt_vmi_storage_io_latency_seconds_bucket{operation="write"}[5m])
  )
)
```

**P99 read latency per VM and disk**
```promql
histogram_quantile(0.99,
  sum by (namespace, name, disk, le) (
    rate(kubevirt_vmi_storage_io_latency_seconds_bucket{operation="read"}[5m])
  )
)
```

**P99 flush latency** — relevant for databases and journaling filesystems
```promql
histogram_quantile(0.99,
  sum by (namespace, name, disk, le) (
    rate(kubevirt_vmi_storage_io_latency_seconds_bucket{operation="flush"}[5m])
  )
)
```

**Mean latency** — useful when sample counts are too low for a stable percentile
```promql
sum by (namespace, name, disk, operation) (
  rate(kubevirt_vmi_storage_io_latency_seconds_sum[5m])
)
/
sum by (namespace, name, disk, operation) (
  rate(kubevirt_vmi_storage_io_latency_seconds_count[5m])
)
```

**VMs with P99 write latency above 100ms**
```promql
histogram_quantile(0.99,
  sum by (namespace, name, disk, le) (
    rate(kubevirt_vmi_storage_io_latency_seconds_bucket{operation="write"}[5m])
  )
) > 0.1
```

---

### `kubevirt_vmi_storage_queue_inuse` / `kubevirt_vmi_storage_queue_size`

In-flight descriptors and queue capacity for storage virtqueues. Labels: `namespace`, `name`, `node`, `pod`, `disk`, `persistentvolumeclaim`, `queue`, `bus`.

**Virtqueue utilisation ratio** — values approaching 1.0 indicate saturation
```promql
sum by (namespace, name, disk, bus) (kubevirt_vmi_storage_queue_inuse)
/
sum by (namespace, name, disk, bus) (kubevirt_vmi_storage_queue_size > 0)
```

**Saturated disks (>90% queue utilisation)**
```promql
sum by (namespace, name, disk, persistentvolumeclaim, bus) (
  kubevirt_vmi_storage_queue_inuse
)
/
(
  sum by (namespace, name, disk, persistentvolumeclaim, bus) (
    kubevirt_vmi_storage_queue_size
  ) > 0
) > 0.9
```

**Maximum in-flight across all queues per disk**
```promql
max by (namespace, name, disk, bus) (kubevirt_vmi_storage_queue_inuse)
```

---

## eBPF subsystem

### `kme_block_io_latency_seconds`

Host kernel block I/O latency attributed to a pod's PVC via `/proc/1/mountinfo`. Labels: `node`, `namespace`, `persistentvolumeclaim`, `pod`, `operation` (read/write/discard/flush).

**P99 read/write latency per PVC**
```promql
histogram_quantile(0.99,
  sum by (namespace, persistentvolumeclaim, pod, operation, le) (
    rate(kme_block_io_latency_seconds_bucket{operation=~"read|write"}[5m])
  )
)
```

**Mean block I/O latency per PVC**
```promql
sum by (namespace, persistentvolumeclaim, pod, operation) (
  rate(kme_block_io_latency_seconds_sum[5m])
)
/
sum by (namespace, persistentvolumeclaim, pod, operation) (
  rate(kme_block_io_latency_seconds_count[5m])
)
```

**PVCs with P99 latency above 100ms**
```promql
histogram_quantile(0.99,
  sum by (namespace, persistentvolumeclaim, pod, operation, le) (
    rate(kme_block_io_latency_seconds_bucket{operation=~"read|write"}[5m])
  )
) > 0.1
```

---

### `kme_system_block_io_latency_seconds`

Block I/O latency for devices that could not be attributed to a pod (system block devices). Labels: `node`, `device`, `operation`.

**P99 latency for system/unresolved block devices**
```promql
histogram_quantile(0.99,
  sum by (node, device, operation, le) (
    rate(kme_system_block_io_latency_seconds_bucket[5m])
  )
)
```

---

### `kme_nfs_io_latency_seconds`

NFS I/O latency from kernel tracepoints (`nfs_initiate_*` → `nfs_*_done`). Labels: `node`, `namespace`, `persistentvolumeclaim`, `pod`, `operation` (read/write).

**P99 NFS read/write latency per PVC**
```promql
histogram_quantile(0.99,
  sum by (namespace, persistentvolumeclaim, pod, operation, le) (
    rate(kme_nfs_io_latency_seconds_bucket[5m])
  )
)
```

**NFS IOPS rate per PVC**
```promql
sum by (namespace, persistentvolumeclaim, pod, operation) (
  rate(kme_nfs_io_latency_seconds_count[5m])
)
```

---

### `kme_nfs_vfs_latency_seconds`

NFS VFS call latency from kprobes (disabled by default; enable with `--enable-ebpf-nfs-kprobe`). Labels: `node`, `namespace`, `persistentvolumeclaim`, `pod`, `operation` (read/write/open/getattr).

**P99 latency by VFS operation type**
```promql
histogram_quantile(0.99,
  sum by (namespace, persistentvolumeclaim, operation, le) (
    rate(kme_nfs_vfs_latency_seconds_bucket[5m])
  )
)
```

**getattr rate** — high rates may indicate metadata-heavy workloads or missing attribute caching
```promql
sum by (namespace, persistentvolumeclaim, pod) (
  rate(kme_nfs_vfs_latency_seconds_count{operation="getattr"}[5m])
)
```

---

### `kme_subsystem_active`

Whether each eBPF subsystem loaded successfully. Label: `subsystem` (block/nfs/nfs_kprobe). Value is 1 (loaded) or 0 (failed).

**Check which subsystems are active**
```promql
kme_subsystem_active
```

---

## QGA subsystem (Windows VMs)

### `kubevirt_vmi_storage_guest_latency_avg_seconds`

Guest-observed average I/O latency derived from Windows Performance Counters via Little's Law. Labels: `namespace`, `name`, `node`, `pod`, `disk`, `persistentvolumeclaim`, `operation`, `drive`.

**Guest average write latency per disk**
```promql
kubevirt_vmi_storage_guest_latency_avg_seconds{operation="write"}
```

**Guest vs hypervisor latency gap** — a positive gap reveals queuing or overhead inside the guest; requires matching on `disk`
```promql
kubevirt_vmi_storage_guest_latency_avg_seconds{operation="write"}
- on(namespace, name, disk) group_left()
  (
    sum by (namespace, name, disk) (
      rate(kubevirt_vmi_storage_io_latency_seconds_sum{operation="write"}[5m])
    )
    /
    sum by (namespace, name, disk) (
      rate(kubevirt_vmi_storage_io_latency_seconds_count{operation="write"}[5m])
    )
  )
```

---

### `kubevirt_vmi_storage_guest_iops`

Guest-side IOPS derived from Windows Performance Counters. Labels: `namespace`, `name`, `node`, `pod`, `disk`, `persistentvolumeclaim`, `operation`, `drive`.

**Total guest IOPS per VM (read + write)**
```promql
sum by (namespace, name) (
  kubevirt_vmi_storage_guest_iops
)
```

**Read/write IOPS breakdown per disk**
```promql
kubevirt_vmi_storage_guest_iops
```

---

## KVM subsystem

### `kubevirt_vmi_kvm_exits_total`

Total VM exits since the VM started. Labels: `namespace`, `name`, `node`, `pod`.

**Exit rate per VM**
```promql
rate(kubevirt_vmi_kvm_exits_total[5m])
```

**Top 10 VMs by exit rate** — spot noisy neighbours
```promql
topk(10,
  sum by (namespace, name, node) (
    rate(kubevirt_vmi_kvm_exits_total[5m])
  )
)
```

**Per-node total exit rate** — node-level exit pressure
```promql
sum by (node) (
  rate(kubevirt_vmi_kvm_exits_total[5m])
)
```

---

### `kubevirt_vmi_kvm_halt_exits_total`

Exits triggered by a guest halt instruction (vCPU going idle). Labels: `namespace`, `name`, `node`, `pod`.

**Non-halt exit rate** — exits that represent real hypervisor work (I/O, MMIO, emulated instructions); the most useful KVM signal
```promql
rate(kubevirt_vmi_kvm_exits_total[5m])
- rate(kubevirt_vmi_kvm_halt_exits_total[5m])
```

**Halt fraction** — proportion of exits that are idle sleeps; a low value on a busy VM indicates genuine exit pressure
```promql
rate(kubevirt_vmi_kvm_halt_exits_total[5m])
/
rate(kubevirt_vmi_kvm_exits_total[5m])
```

---

### `kubevirt_vmi_kvm_hypercalls_total`

Guest-initiated calls to the hypervisor (paravirt operations: balloon driver, clock, etc.). Labels: `namespace`, `name`, `node`, `pod`.

**Hypercall rate per VM**
```promql
rate(kubevirt_vmi_kvm_hypercalls_total[5m])
```

**Hypercalls as a fraction of all exits** — high fraction suggests a paravirt-heavy guest
```promql
rate(kubevirt_vmi_kvm_hypercalls_total[5m])
/
rate(kubevirt_vmi_kvm_exits_total[5m])
```

---

### `kubevirt_vmi_kvm_tlb_flushes_total`

TLB flush events. High rates are expected for memory-intensive or multi-vCPU workloads; a sudden spike on a previously stable VM may indicate workload change or vCPU migration pressure. Labels: `namespace`, `name`, `node`, `pod`.

**TLB flush rate per VM**
```promql
rate(kubevirt_vmi_kvm_tlb_flushes_total[5m])
```

**TLB flushes as a fraction of all exits**
```promql
rate(kubevirt_vmi_kvm_tlb_flushes_total[5m])
/
rate(kubevirt_vmi_kvm_exits_total[5m])
```

---

## Exporter health

**Poll age per subsystem** — how long since each subsystem last completed a cycle
```promql
time() - kme_qmp_last_poll_timestamp_seconds
time() - kme_qga_last_poll_timestamp_seconds
time() - kme_kvm_last_poll_timestamp_seconds
```

**Scrape error rate per subsystem**
```promql
rate(kme_qmp_scrape_errors_total[5m])
rate(kme_qga_scrape_errors_total[5m])
rate(kme_kvm_scrape_errors_total[5m])
```
