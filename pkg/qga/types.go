package qga

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

type DiskCounters struct {
	Name           string
	RdQueueLen     uint64 // AvgDiskReadQueueLength: accumulated queue_depth * 100ns ticks
	WrQueueLen     uint64 // AvgDiskWriteQueueLength: accumulated queue_depth * 100ns ticks
	RdOps          uint64 // DiskReadsPerSec: accumulated read operation count (uint32 in WMI)
	WrOps          uint64 // DiskWritesPerSec: accumulated write operation count (uint32 in WMI)
	Timestamp100ns uint64
}

type DiskMetrics struct {
	Name       string
	RdLatSec   float64
	WrLatSec   float64
	RdIOPS     float64
	WrIOPS     float64
	ElapsedSec float64
}

var requiredColumns = []string{
	"name",
	"avgdiskreadqueuelength",
	"avgdiskwritequeuelength",
	"diskreadspersec",
	"diskwritespersec",
	"timestamp_sys100ns",
}

// ParseWMICSV parses CSV output (from PowerShell ConvertTo-Csv or wmic /format:csv)
// into DiskCounters. It uses header-based column mapping (case-insensitive) to handle
// arbitrary column ordering. Rows with Name == "_Total" are skipped.
func ParseWMICSV(data []byte) ([]DiskCounters, error) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	data = bytes.TrimSpace(data)

	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parsing CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("CSV has no data rows (got %d lines)", len(records))
	}

	colMap := make(map[string]int, len(records[0]))
	for i, name := range records[0] {
		colMap[strings.ToLower(strings.TrimSpace(name))] = i
	}

	for _, req := range requiredColumns {
		if _, ok := colMap[req]; !ok {
			return nil, fmt.Errorf("missing required column %q in CSV header", req)
		}
	}

	nameIdx := colMap["name"]
	rdQIdx := colMap["avgdiskreadqueuelength"]
	wrQIdx := colMap["avgdiskwritequeuelength"]
	rdOpsIdx := colMap["diskreadspersec"]
	wrOpsIdx := colMap["diskwritespersec"]
	tsIdx := colMap["timestamp_sys100ns"]

	var results []DiskCounters
	for _, row := range records[1:] {
		maxIdx := max(nameIdx, rdQIdx, wrQIdx, rdOpsIdx, wrOpsIdx, tsIdx)
		if len(row) <= maxIdx {
			continue
		}

		name := strings.TrimSpace(row[nameIdx])
		if name == "" || strings.EqualFold(name, "_Total") {
			continue
		}

		rdQ, err := strconv.ParseUint(strings.TrimSpace(row[rdQIdx]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing AvgDiskReadQueueLength for %s: %w", name, err)
		}
		wrQ, err := strconv.ParseUint(strings.TrimSpace(row[wrQIdx]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing AvgDiskWriteQueueLength for %s: %w", name, err)
		}
		rdOps, err := strconv.ParseUint(strings.TrimSpace(row[rdOpsIdx]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing DiskReadsPerSec for %s: %w", name, err)
		}
		wrOps, err := strconv.ParseUint(strings.TrimSpace(row[wrOpsIdx]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing DiskWritesPerSec for %s: %w", name, err)
		}
		ts, err := strconv.ParseUint(strings.TrimSpace(row[tsIdx]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parsing Timestamp_Sys100NS for %s: %w", name, err)
		}

		results = append(results, DiskCounters{
			Name:           name,
			RdQueueLen:     rdQ,
			WrQueueLen:     wrQ,
			RdOps:          rdOps,
			WrOps:          wrOps,
			Timestamp100ns: ts,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no valid disk rows found in CSV output")
	}

	return results, nil
}

// ComputeMetrics derives average I/O latency and IOPS from two successive
// raw counter snapshots using Little's Law.
//
// The direct AvgDisksecPer{Read,Write} counters are uint32 and overflow
// rapidly under high-latency workloads (e.g. 160 IOPS at 1.6s latency
// wraps the counter ~35 times per minute). Instead we use:
//
//   - AvgDisk{Read,Write}QueueLength (uint64): accumulates queue_depth * 100ns_ticks
//   - Disk{Reads,Writes}PerSec (uint32): accumulates operation count
//
// By Little's Law (L = λ * W):
//
//	avg_queue_depth = delta(QueueLength) / delta(Timestamp_Sys100NS)
//	iops            = delta(Ops) / elapsed_seconds
//	avg_latency     = avg_queue_depth / iops
//	                = delta(QueueLength) / delta(Ops) / 1e7    [seconds]
//
// This is mathematically equivalent to the PERF_AVERAGE_TIMER formula
// for Avg. Disk sec/Write but uses uint64 counters that do not overflow.
func ComputeMetrics(prev, curr DiskCounters) DiskMetrics {
	m := DiskMetrics{Name: curr.Name}

	if curr.Timestamp100ns <= prev.Timestamp100ns {
		return m
	}
	m.ElapsedSec = float64(curr.Timestamp100ns-prev.Timestamp100ns) / 1e7

	if rdOps := curr.RdOps - prev.RdOps; rdOps > 0 {
		m.RdLatSec = float64(curr.RdQueueLen-prev.RdQueueLen) / float64(rdOps) / 1e7
		m.RdIOPS = float64(rdOps) / m.ElapsedSec
	}

	if wrOps := curr.WrOps - prev.WrOps; wrOps > 0 {
		m.WrLatSec = float64(curr.WrQueueLen-prev.WrQueueLen) / float64(wrOps) / 1e7
		m.WrIOPS = float64(wrOps) / m.ElapsedSec
	}

	return m
}
