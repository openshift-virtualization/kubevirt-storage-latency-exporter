package ebpf

import (
	"log/slog"

	"github.com/openshift-virtualization/kubevirt-metrics-exporter/pkg/device"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	podBlockDesc = prometheus.NewDesc(
		"kme_block_io_latency_seconds",
		"Histogram of block I/O latency in seconds, attributed to a pod volume",
		[]string{"node", "namespace", "persistentvolumeclaim", "pod", "operation"},
		nil,
	)
	systemBlockDesc = prometheus.NewDesc(
		"kme_system_block_io_latency_seconds",
		"Histogram of block I/O latency in seconds for system/unresolvable devices",
		[]string{"node", "device", "operation"},
		nil,
	)
	nfsDesc = prometheus.NewDesc(
		"kme_nfs_io_latency_seconds",
		"Histogram of NFS I/O latency in seconds",
		[]string{"node", "namespace", "persistentvolumeclaim", "pod", "operation"},
		nil,
	)
	nfsVfsDesc = prometheus.NewDesc(
		"kme_nfs_vfs_latency_seconds",
		"Histogram of NFS VFS call latency in seconds (kprobe-based)",
		[]string{"node", "namespace", "persistentvolumeclaim", "pod", "operation"},
		nil,
	)
	subsystemDesc = prometheus.NewDesc(
		"kme_subsystem_active",
		"Whether an eBPF monitoring subsystem is active (1) or failed to load (0)",
		[]string{"subsystem"},
		nil,
	)

	opLabels       = [4]string{"read", "write", "discard", "flush"}
	nfsVfsOpLabels = [4]string{"read", "write", "open", "getattr"}
)

type Collector struct {
	programs   *Programs
	resolver   *device.Resolver
	nodeName   string
	buckets    []float64
	namespaces map[string]bool
	log        *slog.Logger
}

func NewCollector(programs *Programs, resolver *device.Resolver, nodeName string, buckets []float64, namespaces []string, log *slog.Logger) *Collector {
	nsFilter := make(map[string]bool, len(namespaces))
	for _, ns := range namespaces {
		nsFilter[ns] = true
	}
	return &Collector{
		programs:   programs,
		resolver:   resolver,
		nodeName:   nodeName,
		buckets:    buckets,
		namespaces: nsFilter,
		log:        log,
	}
}

func (c *Collector) namespaceAllowed(ns string) bool {
	return len(c.namespaces) == 0 || c.namespaces[ns]
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- podBlockDesc
	ch <- systemBlockDesc
	ch <- nfsDesc
	ch <- nfsVfsDesc
	ch <- subsystemDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.programs.mu.RLock()
	defer c.programs.mu.RUnlock()

	c.collectSubsystemGauge(ch)
	if c.programs.BlockHists != nil {
		c.collectBlock(ch)
	}
	if c.programs.NfsHists != nil {
		c.collectNFS(ch)
	}
	if c.programs.NfsKprobeHists != nil {
		c.collectNFSKprobe(ch)
	}
}

func (c *Collector) collectSubsystemGauge(ch chan<- prometheus.Metric) {
	val := 0.0
	if c.programs.BlockActive {
		val = 1.0
	}
	ch <- prometheus.MustNewConstMetric(subsystemDesc, prometheus.GaugeValue, val, "block")

	val = 0.0
	if c.programs.NFSActive {
		val = 1.0
	}
	ch <- prometheus.MustNewConstMetric(subsystemDesc, prometheus.GaugeValue, val, "nfs")

	val = 0.0
	if c.programs.NFSKprobeActive {
		val = 1.0
	}
	ch <- prometheus.MustNewConstMetric(subsystemDesc, prometheus.GaugeValue, val, "nfs_kprobe")
}

type blockHistKey struct {
	Dev uint32
	Op  uint8
	Pad [3]uint8
}

type nfsHistKey struct {
	Dev uint32
	Op  uint8
	Pad [3]uint8
}

type histValue struct {
	Slots [MaxSlots]uint64
}

func (c *Collector) collectBlock(ch chan<- prometheus.Metric) {
	var key blockHistKey
	var val histValue
	iter := c.programs.BlockHists.Iterate()

	for iter.Next(&key, &val) {
		if key.Op > 3 {
			continue
		}
		count, sum, buckets := SlotsToConstHistogram(val.Slots, c.buckets)
		if count == 0 {
			continue
		}

		info, resolved := c.resolver.Lookup(key.Dev)
		if resolved {
			if !c.namespaceAllowed(info.Namespace) {
				continue
			}
			m, err := prometheus.NewConstHistogram(
				podBlockDesc,
				count, sum, buckets,
				c.nodeName, info.Namespace, info.PVCName, info.PodName, opLabels[key.Op],
			)
			if err != nil {
				c.log.Error("creating block histogram metric", "error", err)
				continue
			}
			ch <- m
		} else {
			devName := device.DevToString(key.Dev)
			m, err := prometheus.NewConstHistogram(
				systemBlockDesc,
				count, sum, buckets,
				c.nodeName, devName, opLabels[key.Op],
			)
			if err != nil {
				c.log.Error("creating system block histogram metric", "error", err)
				continue
			}
			ch <- m
		}
	}

	if err := iter.Err(); err != nil {
		c.log.Error("iterating block histogram map", "error", err)
	}
}

func (c *Collector) collectNFS(ch chan<- prometheus.Metric) {
	var key nfsHistKey
	var val histValue
	iter := c.programs.NfsHists.Iterate()

	for iter.Next(&key, &val) {
		if key.Op > 1 {
			continue
		}
		count, sum, buckets := SlotsToConstHistogram(val.Slots, c.buckets)
		if count == 0 {
			continue
		}

		info, resolved := c.resolver.Lookup(key.Dev)
		ns := ""
		pvc := ""
		podName := ""
		if resolved {
			if !c.namespaceAllowed(info.Namespace) {
				continue
			}
			ns = info.Namespace
			pvc = info.PVCName
			podName = info.PodName
		}

		m, err := prometheus.NewConstHistogram(
			nfsDesc,
			count, sum, buckets,
			c.nodeName, ns, pvc, podName, opLabels[key.Op],
		)
		if err != nil {
			c.log.Error("creating NFS histogram metric", "error", err)
			continue
		}
		ch <- m
	}

	if err := iter.Err(); err != nil {
		c.log.Error("iterating NFS histogram map", "error", err)
	}
}

func (c *Collector) collectNFSKprobe(ch chan<- prometheus.Metric) {
	var key nfsHistKey
	var val histValue
	iter := c.programs.NfsKprobeHists.Iterate()

	for iter.Next(&key, &val) {
		if key.Op > 3 {
			continue
		}
		count, sum, buckets := SlotsToConstHistogram(val.Slots, c.buckets)
		if count == 0 {
			continue
		}

		info, resolved := c.resolver.Lookup(key.Dev)
		ns := ""
		pvc := ""
		podName := ""
		if resolved {
			if !c.namespaceAllowed(info.Namespace) {
				continue
			}
			ns = info.Namespace
			pvc = info.PVCName
			podName = info.PodName
		}

		m, err := prometheus.NewConstHistogram(
			nfsVfsDesc,
			count, sum, buckets,
			c.nodeName, ns, pvc, podName, nfsVfsOpLabels[key.Op],
		)
		if err != nil {
			c.log.Error("creating NFS VFS histogram metric", "error", err)
			continue
		}
		ch <- m
	}

	if err := iter.Err(); err != nil {
		c.log.Error("iterating NFS kprobe histogram map", "error", err)
	}
}
