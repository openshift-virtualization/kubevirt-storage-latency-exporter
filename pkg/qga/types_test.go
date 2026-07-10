package qga

import (
	"math"
	"testing"
)

const realWMICOutput = `Node,AvgDiskReadQueueLength,AvgDiskWriteQueueLength,DiskReadsPerSec,DiskWritesPerSec,Name,Timestamp_Sys100NS
WIN-L5SM2M3JFBO,336223056,161462865009,42946,752955,0 C:,134280977011680000
WIN-L5SM2M3JFBO,63287115,9573783964885,63,610780,1 E:,134280977011680000
WIN-L5SM2M3JFBO,434565,256605565134,42,1678184,2 F:,134280977011680000
WIN-L5SM2M3JFBO,399944736,9991852395028,43051,3041919,_Total,134280977011680000
`

func TestParseWMICSV_RealOutput(t *testing.T) {
	counters, err := ParseWMICSV([]byte(realWMICOutput))
	if err != nil {
		t.Fatalf("ParseWMICSV() error = %v", err)
	}

	if len(counters) != 3 {
		t.Fatalf("expected 3 disks (excluding _Total), got %d", len(counters))
	}

	expected := map[string]DiskCounters{
		"0 C:": {Name: "0 C:", RdQueueLen: 336223056, WrQueueLen: 161462865009, RdOps: 42946, WrOps: 752955, Timestamp100ns: 134280977011680000},
		"1 E:": {Name: "1 E:", RdQueueLen: 63287115, WrQueueLen: 9573783964885, RdOps: 63, WrOps: 610780, Timestamp100ns: 134280977011680000},
		"2 F:": {Name: "2 F:", RdQueueLen: 434565, WrQueueLen: 256605565134, RdOps: 42, WrOps: 1678184, Timestamp100ns: 134280977011680000},
	}

	for _, dc := range counters {
		exp, ok := expected[dc.Name]
		if !ok {
			t.Errorf("unexpected disk %q", dc.Name)
			continue
		}
		if dc != exp {
			t.Errorf("disk %q:\n  got  %+v\n  want %+v", dc.Name, dc, exp)
		}
	}
}

func TestParseWMICSV_SkipsTotal(t *testing.T) {
	counters, err := ParseWMICSV([]byte(realWMICOutput))
	if err != nil {
		t.Fatalf("ParseWMICSV() error = %v", err)
	}

	for _, dc := range counters {
		if dc.Name == "_Total" {
			t.Error("_Total row should be skipped")
		}
	}
}

func TestParseWMICSV_CaseInsensitiveHeaders(t *testing.T) {
	input := `NODE,AVGDISKREADQUEUELENGTH,AVGDISKWRITEQUEUELENGTH,DISKREADSPERSEC,DISKWRITESPERSEC,NAME,TIMESTAMP_SYS100NS
HOST1,1000,2000,10,20,0 C:,100000000
`
	counters, err := ParseWMICSV([]byte(input))
	if err != nil {
		t.Fatalf("ParseWMICSV() error = %v", err)
	}
	if len(counters) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(counters))
	}
	if counters[0].RdQueueLen != 1000 || counters[0].WrQueueLen != 2000 {
		t.Errorf("unexpected values: %+v", counters[0])
	}
}

func TestParseWMICSV_DifferentColumnOrder(t *testing.T) {
	input := `Node,Name,Timestamp_Sys100NS,DiskWritesPerSec,AvgDiskWriteQueueLength,DiskReadsPerSec,AvgDiskReadQueueLength
HOST1,0 C:,100000000,20,2000,10,1000
`
	counters, err := ParseWMICSV([]byte(input))
	if err != nil {
		t.Fatalf("ParseWMICSV() error = %v", err)
	}
	if len(counters) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(counters))
	}
	dc := counters[0]
	if dc.Name != "0 C:" || dc.RdQueueLen != 1000 || dc.WrQueueLen != 2000 || dc.RdOps != 10 || dc.WrOps != 20 || dc.Timestamp100ns != 100000000 {
		t.Errorf("unexpected counters: %+v", dc)
	}
}

func TestParseWMICSV_WindowsLineEndings(t *testing.T) {
	input := "Node,Name,AvgDiskReadQueueLength,AvgDiskWriteQueueLength,DiskReadsPerSec,DiskWritesPerSec,Timestamp_Sys100NS\r\nHOST1,0 C:,1000,2000,10,20,100000000\r\n"
	counters, err := ParseWMICSV([]byte(input))
	if err != nil {
		t.Fatalf("ParseWMICSV() error = %v", err)
	}
	if len(counters) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(counters))
	}
}

func TestParseWMICSV_MissingColumn(t *testing.T) {
	input := `Node,Name,AvgDiskReadQueueLength,DiskReadsPerSec
HOST1,0 C:,1000,10
`
	_, err := ParseWMICSV([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing columns")
	}
}

func TestParseWMICSV_NoDataRows(t *testing.T) {
	input := `Node,Name,AvgDiskReadQueueLength,AvgDiskWriteQueueLength,DiskReadsPerSec,DiskWritesPerSec,Timestamp_Sys100NS
`
	_, err := ParseWMICSV([]byte(input))
	if err == nil {
		t.Fatal("expected error for no data rows")
	}
}

func TestParseWMICSV_OnlyTotal(t *testing.T) {
	input := `Node,Name,AvgDiskReadQueueLength,AvgDiskWriteQueueLength,DiskReadsPerSec,DiskWritesPerSec,Timestamp_Sys100NS
HOST1,_Total,1000,2000,10,20,100000000
`
	_, err := ParseWMICSV([]byte(input))
	if err == nil {
		t.Fatal("expected error when only _Total rows exist")
	}
}

func TestParseWMICSV_EmptyInput(t *testing.T) {
	_, err := ParseWMICSV([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseWMICSV_PowerShellFormat(t *testing.T) {
	input := `"Name","AvgDiskReadQueueLength","AvgDiskWriteQueueLength","DiskReadsPerSec","DiskWritesPerSec","Timestamp_Sys100NS"
"0 C:","336223056","161462865009","42946","752955","134280977011680000"
"1 E:","63287115","9573783964885","63","610780","134280977011680000"
"_Total","399944736","9991852395028","43051","3041919","134280977011680000"
`
	counters, err := ParseWMICSV([]byte(input))
	if err != nil {
		t.Fatalf("ParseWMICSV() error = %v", err)
	}

	if len(counters) != 2 {
		t.Fatalf("expected 2 disks (Total excluded), got %d", len(counters))
	}

	if counters[0].Name != "0 C:" {
		t.Errorf("expected Name='0 C:', got %q", counters[0].Name)
	}
	if counters[0].RdQueueLen != 336223056 {
		t.Errorf("expected RdQueueLen=336223056, got %d", counters[0].RdQueueLen)
	}
	if counters[1].WrQueueLen != 9573783964885 {
		t.Errorf("expected WrQueueLen=9573783964885, got %d", counters[1].WrQueueLen)
	}
}

func TestComputeMetrics_Basic(t *testing.T) {
	prev := DiskCounters{
		Name: "0 C:", RdQueueLen: 10000000, WrQueueLen: 20000000,
		RdOps: 100, WrOps: 200,
		Timestamp100ns: 100000000000,
	}
	curr := DiskCounters{
		Name: "0 C:", RdQueueLen: 11000000, WrQueueLen: 24000000,
		RdOps: 200, WrOps: 400,
		Timestamp100ns: 100050000000,
	}

	m := ComputeMetrics(prev, curr)

	// elapsed = 5.0 seconds
	if math.Abs(m.ElapsedSec-5.0) > 0.001 {
		t.Errorf("ElapsedSec = %f, want 5.0", m.ElapsedSec)
	}

	// rd_latency = delta(RdQueueLen) / delta(RdOps) / 1e7
	//            = 1000000 / 100 / 1e7 = 0.001s = 1ms
	expectedRdLat := 1000000.0 / 100.0 / 1e7
	if math.Abs(m.RdLatSec-expectedRdLat) > 1e-12 {
		t.Errorf("RdLatSec = %e, want %e", m.RdLatSec, expectedRdLat)
	}

	// rd_iops = 100 / 5.0 = 20
	if math.Abs(m.RdIOPS-20.0) > 0.001 {
		t.Errorf("RdIOPS = %f, want 20.0", m.RdIOPS)
	}

	// wr_latency = 4000000 / 200 / 1e7 = 0.002s = 2ms
	expectedWrLat := 4000000.0 / 200.0 / 1e7
	if math.Abs(m.WrLatSec-expectedWrLat) > 1e-12 {
		t.Errorf("WrLatSec = %e, want %e", m.WrLatSec, expectedWrLat)
	}

	// wr_iops = 200 / 5.0 = 40
	if math.Abs(m.WrIOPS-40.0) > 0.001 {
		t.Errorf("WrIOPS = %f, want 40.0", m.WrIOPS)
	}
}

func TestComputeMetrics_RealWorldHighLatency(t *testing.T) {
	// Verified real data from Windows VM running fio at ~160 IOPS / ~1.6s latency.
	// Two samples ~42 seconds apart.
	prev := DiskCounters{
		Name: "1 E:", RdQueueLen: 63287115, WrQueueLen: 9573783964885,
		RdOps: 63, WrOps: 610780,
		Timestamp100ns: 134280977011680000,
	}
	curr := DiskCounters{
		Name: "1 E:", RdQueueLen: 63287115, WrQueueLen: 9684684916507,
		RdOps: 63, WrOps: 617717,
		Timestamp100ns: 134280977445284998,
	}

	m := ComputeMetrics(prev, curr)

	// elapsed ≈ 43.36s
	if m.ElapsedSec < 43.0 || m.ElapsedSec > 44.0 {
		t.Errorf("ElapsedSec = %f, want ~43.36", m.ElapsedSec)
	}

	// wr_latency ≈ 1.599s (verified against typeperf output of 1.600826)
	if m.WrLatSec < 1.5 || m.WrLatSec > 1.7 {
		t.Errorf("WrLatSec = %f, want ~1.6", m.WrLatSec)
	}

	// wr_iops ≈ 160
	if m.WrIOPS < 155.0 || m.WrIOPS > 165.0 {
		t.Errorf("WrIOPS = %f, want ~160", m.WrIOPS)
	}

	// no read activity
	if m.RdIOPS != 0 || m.RdLatSec != 0 {
		t.Errorf("expected zero read metrics, got RdIOPS=%f RdLatSec=%f", m.RdIOPS, m.RdLatSec)
	}
}

func TestComputeMetrics_NoActivity(t *testing.T) {
	prev := DiskCounters{
		Name: "0 C:", RdQueueLen: 1000, WrQueueLen: 2000,
		RdOps: 100, WrOps: 200,
		Timestamp100ns: 100000000000,
	}
	curr := DiskCounters{
		Name: "0 C:", RdQueueLen: 1000, WrQueueLen: 2000,
		RdOps: 100, WrOps: 200,
		Timestamp100ns: 100050000000,
	}

	m := ComputeMetrics(prev, curr)

	if m.RdLatSec != 0 || m.WrLatSec != 0 || m.RdIOPS != 0 || m.WrIOPS != 0 {
		t.Errorf("expected zero metrics for no activity, got %+v", m)
	}
	if math.Abs(m.ElapsedSec-5.0) > 0.001 {
		t.Errorf("ElapsedSec = %f, want 5.0", m.ElapsedSec)
	}
}

func TestComputeMetrics_SameTimestamp(t *testing.T) {
	prev := DiskCounters{Name: "0 C:", Timestamp100ns: 100000000000}
	curr := DiskCounters{Name: "0 C:", Timestamp100ns: 100000000000}

	m := ComputeMetrics(prev, curr)

	if m.ElapsedSec != 0 || m.RdIOPS != 0 || m.WrIOPS != 0 {
		t.Errorf("expected zero metrics for same timestamp, got %+v", m)
	}
}

func TestComputeMetrics_ReadOnly(t *testing.T) {
	prev := DiskCounters{
		Name: "1 E:", RdQueueLen: 0, RdOps: 0,
		WrQueueLen: 0, WrOps: 0,
		Timestamp100ns: 100000000000,
	}
	curr := DiskCounters{
		Name: "1 E:", RdQueueLen: 500000, RdOps: 50,
		WrQueueLen: 0, WrOps: 0,
		Timestamp100ns: 100100000000,
	}

	m := ComputeMetrics(prev, curr)

	if m.RdIOPS == 0 {
		t.Error("expected non-zero RdIOPS")
	}
	if m.WrIOPS != 0 || m.WrLatSec != 0 {
		t.Errorf("expected zero write metrics, got WrIOPS=%f WrLatSec=%f", m.WrIOPS, m.WrLatSec)
	}
}
