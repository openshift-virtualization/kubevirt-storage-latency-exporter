package qga

import "github.com/prometheus/client_golang/prometheus"

var (
	latencyAvgDesc = prometheus.NewDesc(
		"kubevirt_storage_guest_io_latency_avg_seconds",
		"Average guest-side I/O latency per disk via QEMU Guest Agent (Windows PDH raw counters, derived via Little's Law)",
		[]string{"namespace", "vmi", "node", "disk", "drive", "operation", "persistentvolumeclaim"},
		nil,
	)

	iopsDesc = prometheus.NewDesc(
		"kubevirt_storage_guest_io_operations_per_second",
		"Guest-side I/O operations per second per disk via QEMU Guest Agent (Windows PDH raw counters)",
		[]string{"namespace", "vmi", "node", "disk", "drive", "operation", "persistentvolumeclaim"},
		nil,
	)

	qgaScrapeErrorsDesc = prometheus.NewDesc(
		"kubevirt_storage_qga_scrape_errors_total",
		"Total number of errors encountered during QGA scrape cycles",
		nil, nil,
	)

	qgaLastPollDesc = prometheus.NewDesc(
		"kubevirt_storage_qga_last_poll_timestamp_seconds",
		"Unix timestamp of the last QGA poll cycle",
		nil, nil,
	)
)
