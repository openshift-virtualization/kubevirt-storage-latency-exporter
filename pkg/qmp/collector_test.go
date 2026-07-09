package qmp

import (
	"encoding/json"
	"log/slog"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestQMP(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "QMP Suite")
}

const sampleBlockStats = `{
  "return": [
    {
      "qdev": "/machine/peripheral/ua-rootdisk/virtio-backend",
      "device": "",
      "node-name": "libvirt-1-storage",
      "stats": {
        "rd_operations": 1500,
        "wr_operations": 800,
        "flush_operations": 50,
        "rd_total_time_ns": 3660462215,
        "wr_total_time_ns": 1496204645,
        "flush_total_time_ns": 63632998,
        "rd_latency_histogram": {
          "boundaries": [10000000, 100000000, 1000000000],
          "bins": [500, 800, 150, 50]
        },
        "wr_latency_histogram": {
          "boundaries": [10000000, 100000000, 1000000000],
          "bins": [200, 400, 180, 20]
        },
        "flush_latency_histogram": {
          "boundaries": [10000000, 100000000, 1000000000],
          "bins": [30, 15, 4, 1]
        }
      }
    },
    {
      "qdev": "/machine/peripheral-anon/device[0]",
      "device": "",
      "node-name": "libvirt-2-format",
      "stats": {
        "rd_operations": 100,
        "wr_operations": 0
      }
    }
  ]
}`

var _ = Describe("ConvertBuckets", func() {
	It("should produce cumulative buckets from differential bins", func() {
		hist := &LatencyHist{
			Boundaries: []float64{10000000, 100000000, 1000000000},
			Bins:       []uint64{500, 800, 150, 50},
		}

		buckets, count := ConvertBuckets(hist)

		Expect(count).To(Equal(uint64(1500)))
		Expect(buckets).To(HaveLen(3))
		Expect(buckets).To(HaveKeyWithValue(0.01, uint64(500)))
		Expect(buckets).To(HaveKeyWithValue(0.1, uint64(1300)))
		Expect(buckets).To(HaveKeyWithValue(1.0, uint64(1450)))
	})

	It("should handle empty bins", func() {
		hist := &LatencyHist{
			Boundaries: []float64{1000000},
			Bins:       []uint64{0, 0},
		}

		buckets, count := ConvertBuckets(hist)

		Expect(count).To(Equal(uint64(0)))
		Expect(buckets).To(HaveKeyWithValue(0.001, uint64(0)))
	})

	It("should handle a single boundary", func() {
		hist := &LatencyHist{
			Boundaries: []float64{100000000},
			Bins:       []uint64{42, 8},
		}

		buckets, count := ConvertBuckets(hist)

		Expect(count).To(Equal(uint64(50)))
		Expect(buckets).To(HaveLen(1))
		Expect(buckets).To(HaveKeyWithValue(0.1, uint64(42)))
	})

	It("should handle all operations in the overflow bucket", func() {
		hist := &LatencyHist{
			Boundaries: []float64{10000000, 100000000},
			Bins:       []uint64{0, 0, 100},
		}

		buckets, count := ConvertBuckets(hist)

		Expect(count).To(Equal(uint64(100)))
		Expect(buckets).To(HaveKeyWithValue(0.01, uint64(0)))
		Expect(buckets).To(HaveKeyWithValue(0.1, uint64(0)))
	})
})

var _ = Describe("ExtractDiskInfo", func() {
	Context("with virtio qdev path", func() {
		It("should extract alias and device ID", func() {
			alias, deviceID, ok := ExtractDiskInfo("/machine/peripheral/ua-rootdisk/virtio-backend")

			Expect(ok).To(BeTrue())
			Expect(alias).To(Equal("rootdisk"))
			Expect(deviceID).To(Equal("ua-rootdisk/virtio-backend"))
		})
	})

	Context("with SATA flat qdev", func() {
		It("should extract alias and device ID", func() {
			alias, deviceID, ok := ExtractDiskInfo("ua-datadisk-sata")

			Expect(ok).To(BeTrue())
			Expect(alias).To(Equal("datadisk-sata"))
			Expect(deviceID).To(Equal("ua-datadisk-sata"))
		})
	})

	Context("with non-UA qdev", func() {
		It("should not match anonymous devices", func() {
			_, _, ok := ExtractDiskInfo("/machine/peripheral-anon/device[0]")
			Expect(ok).To(BeFalse())
		})

		It("should not match empty strings", func() {
			_, _, ok := ExtractDiskInfo("")
			Expect(ok).To(BeFalse())
		})
	})
})

var _ = Describe("HasHistograms", func() {
	It("should return true when histogram is present", func() {
		dev := &BlockDevice{
			Stats: BlockStats{
				RdLatencyHistogram: &LatencyHist{},
			},
		}
		Expect(HasHistograms(dev)).To(BeTrue())
	})

	It("should return false when no histogram is present", func() {
		dev := &BlockDevice{
			Stats: BlockStats{},
		}
		Expect(HasHistograms(dev)).To(BeFalse())
	})
})

var _ = Describe("ParseBlockStats", func() {
	var resp BlockStatsResponse

	BeforeEach(func() {
		err := json.Unmarshal([]byte(sampleBlockStats), &resp)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should parse all devices", func() {
		Expect(resp.Return).To(HaveLen(2))
	})

	It("should parse device metadata", func() {
		dev := resp.Return[0]
		Expect(dev.QDev).To(Equal("/machine/peripheral/ua-rootdisk/virtio-backend"))
		Expect(dev.Stats.RdOperations).To(Equal(uint64(1500)))
		Expect(dev.Stats.WrOperations).To(Equal(uint64(800)))
		Expect(dev.Stats.FlushOperations).To(Equal(uint64(50)))
	})

	It("should parse histograms for devices that have them", func() {
		Expect(resp.Return[0].Stats.RdLatencyHistogram).NotTo(BeNil())
		Expect(resp.Return[0].Stats.WrLatencyHistogram).NotTo(BeNil())
		Expect(resp.Return[0].Stats.FlushLatencyHistogram).NotTo(BeNil())
	})

	It("should leave histograms nil for devices without them", func() {
		Expect(resp.Return[1].Stats.RdLatencyHistogram).To(BeNil())
	})

	It("should parse true latency sums", func() {
		stats := resp.Return[0].Stats
		Expect(stats.RdTotalTimeNs).To(Equal(uint64(3660462215)))
		Expect(stats.WrTotalTimeNs).To(Equal(uint64(1496204645)))
		Expect(stats.FlushTotalTimeNs).To(Equal(uint64(63632998)))
	})
})

var _ = Describe("Collector", func() {
	var c *Collector

	BeforeEach(func() {
		c = NewCollector(PollerConfig{NodeName: "test-node"}, nil, nil, slog.Default())
	})

	collectMetrics := func() []prometheus.Metric {
		ch := make(chan prometheus.Metric, 100)
		go func() {
			c.Collect(ch)
			close(ch)
		}()
		var metrics []prometheus.Metric
		for m := range ch {
			metrics = append(metrics, m)
		}
		return metrics
	}

	It("should emit scrape errors and last poll timestamp with no results", func() {
		c.Update(nil, 3, 1234567890.0)
		metrics := collectMetrics()
		Expect(metrics).To(HaveLen(2))
	})

	Context("with VMI results", func() {
		BeforeEach(func() {
			results := []VMIResult{
				{
					Namespace: "default",
					VMI:       "test-vm",
					Node:      "node-1",
					Devices: []DeviceResult{
						{
							DiskAlias: "rootdisk",
							Stats: BlockStats{
								RdTotalTimeNs:    5000000000,
								WrTotalTimeNs:    2000000000,
								FlushTotalTimeNs: 100000000,
								RdLatencyHistogram: &LatencyHist{
									Boundaries: []float64{10000000, 100000000},
									Bins:       []uint64{80, 15, 5},
								},
								WrLatencyHistogram: &LatencyHist{
									Boundaries: []float64{10000000, 100000000},
									Bins:       []uint64{40, 8, 2},
								},
								FlushLatencyHistogram: &LatencyHist{
									Boundaries: []float64{10000000, 100000000},
									Bins:       []uint64{10, 3, 0},
								},
							},
						},
					},
				},
			}
			c.Update(results, 0, 1234567890.0)
		})

		It("should emit histogram metrics for each operation", func() {
			metrics := collectMetrics()
			Expect(len(metrics)).To(BeNumerically(">=", 5))
		})

		It("should emit correct metric values", func() {
			metrics := collectMetrics()

			var histMetrics []prometheus.Metric
			for _, m := range metrics {
				desc := m.Desc().String()
				if containsString(desc, "kubevirt_vmi_storage_io_latency_seconds") {
					histMetrics = append(histMetrics, m)
				}
			}

			Expect(histMetrics).To(HaveLen(3))

			for _, m := range histMetrics {
				d := &dto.Metric{}
				err := m.Write(d)
				Expect(err).NotTo(HaveOccurred())

				labels := map[string]string{}
				for _, lp := range d.Label {
					labels[lp.GetName()] = lp.GetValue()
				}

				Expect(labels).To(HaveKeyWithValue("namespace", "default"))
				Expect(labels).To(HaveKeyWithValue("vmi", "test-vm"))
				Expect(labels).To(HaveKeyWithValue("node", "node-1"))
				Expect(labels).To(HaveKeyWithValue("drive", "rootdisk"))
				Expect(labels).To(HaveKey("operation"))

				h := d.GetHistogram()
				Expect(h).NotTo(BeNil())

				switch labels["operation"] {
				case "read":
					Expect(h.GetSampleCount()).To(Equal(uint64(100)))
					Expect(h.GetSampleSum()).To(BeNumerically("~", 5.0, 0.001))
				case "write":
					Expect(h.GetSampleCount()).To(Equal(uint64(50)))
					Expect(h.GetSampleSum()).To(BeNumerically("~", 2.0, 0.001))
				case "flush":
					Expect(h.GetSampleCount()).To(Equal(uint64(13)))
					Expect(h.GetSampleSum()).To(BeNumerically("~", 0.1, 0.001))
				}
			}
		})
	})

	It("should accumulate scrape errors across updates", func() {
		c.Update(nil, 3, 100.0)
		c.Update(nil, 2, 200.0)

		metrics := collectMetrics()
		for _, m := range metrics {
			if containsString(m.Desc().String(), "scrape_errors_total") {
				d := &dto.Metric{}
				Expect(m.Write(d)).To(Succeed())
				Expect(d.GetCounter().GetValue()).To(Equal(5.0))
			}
		}
	})
})

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

var _ = Describe("IsVirtioBlk", func() {
	It("should return true for virtio-blk-pci", func() {
		dev := &VirtioDevice{Name: "virtio-blk-pci", Path: "/machine/peripheral/ua-rootdisk/virtio-backend"}
		Expect(IsVirtioBlk(dev)).To(BeTrue())
	})

	It("should return true for virtio-blk variants", func() {
		dev := &VirtioDevice{Name: "virtio-blk-ccw", Path: "/some/path"}
		Expect(IsVirtioBlk(dev)).To(BeTrue())
	})

	It("should return false for virtio-scsi", func() {
		dev := &VirtioDevice{Name: "virtio-scsi-pci", Path: "/some/path"}
		Expect(IsVirtioBlk(dev)).To(BeFalse())
	})

	It("should return false for virtio-net", func() {
		dev := &VirtioDevice{Name: "virtio-net-pci", Path: "/some/path"}
		Expect(IsVirtioBlk(dev)).To(BeFalse())
	})

	It("should return false for empty name", func() {
		dev := &VirtioDevice{Name: "", Path: "/some/path"}
		Expect(IsVirtioBlk(dev)).To(BeFalse())
	})
})

var _ = Describe("VirtioDevice parsing", func() {
	It("should unmarshal x-query-virtio response", func() {
		data := `[
			{"path": "/machine/peripheral/ua-rootdisk/virtio-backend", "name": "virtio-blk-pci"},
			{"path": "/machine/peripheral/ua-net0/virtio-backend", "name": "virtio-net-pci"}
		]`
		var devices []VirtioDevice
		err := json.Unmarshal([]byte(data), &devices)
		Expect(err).NotTo(HaveOccurred())
		Expect(devices).To(HaveLen(2))
		Expect(devices[0].Name).To(Equal("virtio-blk-pci"))
		Expect(devices[0].Path).To(Equal("/machine/peripheral/ua-rootdisk/virtio-backend"))
	})
})

var _ = Describe("VirtioStatus parsing", func() {
	It("should unmarshal x-query-virtio-status response", func() {
		data := `{"name": "virtio-blk-pci", "num-vqs": 4}`
		var status VirtioStatus
		err := json.Unmarshal([]byte(data), &status)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Name).To(Equal("virtio-blk-pci"))
		Expect(status.NumVqs).To(Equal(4))
	})
})

var _ = Describe("VirtQueueStatus parsing", func() {
	It("should unmarshal x-query-virtio-queue-status response", func() {
		data := `{"name": "virtio-blk-pci", "queue-index": 2, "inuse": 42, "vring-num": 256}`
		var status VirtQueueStatus
		err := json.Unmarshal([]byte(data), &status)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Name).To(Equal("virtio-blk-pci"))
		Expect(status.QueueIdx).To(Equal(2))
		Expect(status.Inuse).To(Equal(uint32(42)))
		Expect(status.VringNum).To(Equal(uint32(256)))
	})
})

var _ = Describe("Collector virtqueue metrics", func() {
	var c *Collector

	BeforeEach(func() {
		c = NewCollector(PollerConfig{NodeName: "test-node"}, nil, nil, slog.Default())
	})

	collectMetrics := func() []prometheus.Metric {
		ch := make(chan prometheus.Metric, 100)
		go func() {
			c.Collect(ch)
			close(ch)
		}()
		var metrics []prometheus.Metric
		for m := range ch {
			metrics = append(metrics, m)
		}
		return metrics
	}

	Context("with virtqueue results", func() {
		BeforeEach(func() {
			results := []VMIResult{
				{
					Namespace: "default",
					VMI:       "test-vm",
					Node:      "node-1",
					Virtqueues: []VirtqueueResult{
						{DiskAlias: "rootdisk", PVC: "my-pvc", Queue: 0, Inuse: 10, VringNum: 256},
						{DiskAlias: "rootdisk", PVC: "my-pvc", Queue: 1, Inuse: 5, VringNum: 256},
					},
				},
			}
			c.Update(results, 0, 1234567890.0)
		})

		It("should emit inuse and size gauges for each queue", func() {
			metrics := collectMetrics()

			var inuseMetrics, sizeMetrics []prometheus.Metric
			for _, m := range metrics {
				desc := m.Desc().String()
				if containsString(desc, "storage_queue_inuse") {
					inuseMetrics = append(inuseMetrics, m)
				}
				if containsString(desc, "storage_queue_size") {
					sizeMetrics = append(sizeMetrics, m)
				}
			}

			Expect(inuseMetrics).To(HaveLen(2))
			Expect(sizeMetrics).To(HaveLen(2))
		})

		It("should emit correct labels and values", func() {
			metrics := collectMetrics()

			for _, m := range metrics {
				desc := m.Desc().String()
				if !containsString(desc, "storage_queue_inuse") {
					continue
				}

				d := &dto.Metric{}
				err := m.Write(d)
				Expect(err).NotTo(HaveOccurred())

				labels := map[string]string{}
				for _, lp := range d.Label {
					labels[lp.GetName()] = lp.GetValue()
				}

				Expect(labels).To(HaveKeyWithValue("namespace", "default"))
				Expect(labels).To(HaveKeyWithValue("vmi", "test-vm"))
				Expect(labels).To(HaveKeyWithValue("node", "node-1"))
				Expect(labels).To(HaveKeyWithValue("drive", "rootdisk"))
				Expect(labels).To(HaveKeyWithValue("persistentvolumeclaim", "my-pvc"))

				switch labels["queue"] {
				case "0":
					Expect(d.GetGauge().GetValue()).To(Equal(10.0))
				case "1":
					Expect(d.GetGauge().GetValue()).To(Equal(5.0))
				}
			}
		})
	})

	It("should emit no virtqueue metrics when virtqueues list is empty", func() {
		results := []VMIResult{
			{
				Namespace:  "default",
				VMI:        "test-vm",
				Node:       "node-1",
				Virtqueues: nil,
			},
		}
		c.Update(results, 0, 1234567890.0)

		metrics := collectMetrics()
		for _, m := range metrics {
			desc := m.Desc().String()
			Expect(containsString(desc, "storage_queue")).To(BeFalse())
		}
	})

	It("should handle zero inuse correctly", func() {
		results := []VMIResult{
			{
				Namespace: "default",
				VMI:       "test-vm",
				Node:      "node-1",
				Virtqueues: []VirtqueueResult{
					{DiskAlias: "rootdisk", PVC: "", Queue: 0, Inuse: 0, VringNum: 256},
				},
			},
		}
		c.Update(results, 0, 1234567890.0)

		metrics := collectMetrics()
		for _, m := range metrics {
			desc := m.Desc().String()
			if containsString(desc, "storage_queue_inuse") {
				d := &dto.Metric{}
				Expect(m.Write(d)).To(Succeed())
				Expect(d.GetGauge().GetValue()).To(Equal(0.0))
			}
			if containsString(desc, "storage_queue_size") {
				d := &dto.Metric{}
				Expect(m.Write(d)).To(Succeed())
				Expect(d.GetGauge().GetValue()).To(Equal(256.0))
			}
		}
	})
})

var _ = Describe("matchesLabelFilter", func() {
	It("should match when all labels present", func() {
		labels := map[string]string{"app": "myapp", "env": "prod"}
		Expect(matchesLabelFilter(labels, "app=myapp,env=prod")).To(BeTrue())
	})

	It("should match single label", func() {
		labels := map[string]string{"app": "myapp", "env": "prod"}
		Expect(matchesLabelFilter(labels, "app=myapp")).To(BeTrue())
	})

	It("should not match when label value differs", func() {
		labels := map[string]string{"app": "myapp", "env": "staging"}
		Expect(matchesLabelFilter(labels, "env=prod")).To(BeFalse())
	})

	It("should not match when label missing", func() {
		labels := map[string]string{"app": "myapp"}
		Expect(matchesLabelFilter(labels, "env=prod")).To(BeFalse())
	})

	It("should match empty filter", func() {
		labels := map[string]string{"app": "myapp"}
		Expect(matchesLabelFilter(labels, "")).To(BeTrue())
	})
})
