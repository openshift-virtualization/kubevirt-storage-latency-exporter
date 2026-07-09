package qga

import (
	"testing"

	"github.com/openshift-virtualization/kubevirt-metrics-exporter/pkg/qmp"
)

func TestParseDiskIndex(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int
		wantOK bool
	}{
		{"standard", "0 C:", 0, true},
		{"second disk", "1 E:", 1, true},
		{"third disk", "2 F:", 2, true},
		{"no partition", "3", 3, true},
		{"double digit", "10 Z:", 10, true},
		{"empty string", "", 0, false},
		{"non-numeric", "abc", 0, false},
		{"total", "_Total", 0, false},
		{"spaces only", "  ", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseDiskIndex(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ParseDiskIndex(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("ParseDiskIndex(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseGuestGetDisks(t *testing.T) {
	input := []byte(`{"return":[
		{"name":"\\\\.\\PhysicalDrive0","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0,"pci-controller":{"bus":7,"slot":0,"domain":0,"function":0},"dev":"\\\\.\\PhysicalDrive0","target":0}},
		{"name":"\\\\.\\PhysicalDrive1","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0,"pci-controller":{"bus":8,"slot":0,"domain":0,"function":0},"dev":"\\\\.\\PhysicalDrive1","target":0}},
		{"name":"\\\\.\\PhysicalDrive2","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0,"pci-controller":{"bus":9,"slot":0,"domain":0,"function":0},"dev":"\\\\.\\PhysicalDrive2","target":0}}
	]}`)

	disks, err := parseGuestGetDisks(input)
	if err != nil {
		t.Fatalf("parseGuestGetDisks() error = %v", err)
	}

	if len(disks) != 3 {
		t.Fatalf("expected 3 disks, got %d", len(disks))
	}

	expected := []struct {
		driveIndex int
		bus        int
	}{
		{0, 7},
		{1, 8},
		{2, 9},
	}

	for i, exp := range expected {
		if disks[i].DriveIndex != exp.driveIndex {
			t.Errorf("disk[%d].DriveIndex = %d, want %d", i, disks[i].DriveIndex, exp.driveIndex)
		}
		if disks[i].PCIAddr.Bus != exp.bus {
			t.Errorf("disk[%d].PCIAddr.Bus = %d, want %d", i, disks[i].PCIAddr.Bus, exp.bus)
		}
	}
}

func TestParseGuestGetDisks_SkipsPartitions(t *testing.T) {
	input := []byte(`{"return":[
		{"name":"\\\\.\\PhysicalDrive0","partition":false,"address":{"bus-type":"scsi","pci-controller":{"bus":7,"slot":0,"domain":0,"function":0}}},
		{"name":"C:\\","partition":true}
	]}`)

	disks, err := parseGuestGetDisks(input)
	if err != nil {
		t.Fatalf("parseGuestGetDisks() error = %v", err)
	}

	if len(disks) != 1 {
		t.Fatalf("expected 1 disk (partitions skipped), got %d", len(disks))
	}
}

func TestParseGuestGetDisks_NoPCIController(t *testing.T) {
	input := []byte(`{"return":[
		{"name":"\\\\.\\PhysicalDrive0","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0}}
	]}`)

	disks, err := parseGuestGetDisks(input)
	if err != nil {
		t.Fatalf("parseGuestGetDisks() error = %v", err)
	}

	if len(disks) != 0 {
		t.Errorf("expected 0 disks (no PCI controller), got %d", len(disks))
	}
}

func TestParsePhysicalDriveIndex(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   int
		wantOK bool
	}{
		{"drive 0", `\\.\PhysicalDrive0`, 0, true},
		{"drive 1", `\\.\PhysicalDrive1`, 1, true},
		{"drive 10", `\\.\PhysicalDrive10`, 10, true},
		{"no prefix", `PhysicalDrive2`, 2, true},
		{"unrelated", `C:\Windows`, 0, false},
		{"empty", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parsePhysicalDriveIndex(tt.input)
			if ok != tt.wantOK {
				t.Errorf("parsePhysicalDriveIndex(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("parsePhysicalDriveIndex(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

const testDomainXML = `<domain type='kvm'>
  <devices>
    <disk type='block' device='disk'>
      <target dev='vda' bus='virtio'/>
      <alias name='ua-vol-0'/>
      <address type='pci' domain='0x0000' bus='0x07' slot='0x00' function='0x0'/>
    </disk>
    <disk type='block' device='disk'>
      <target dev='vdb' bus='virtio'/>
      <alias name='ua-vol-1'/>
      <address type='pci' domain='0x0000' bus='0x08' slot='0x00' function='0x0'/>
    </disk>
    <disk type='block' device='disk'>
      <target dev='vdc' bus='virtio'/>
      <alias name='ua-vol-2'/>
      <address type='pci' domain='0x0000' bus='0x09' slot='0x00' function='0x0'/>
    </disk>
  </devices>
</domain>`

func TestBuildDiskMapping(t *testing.T) {
	guestDisks := []GuestDisk{
		{Name: `\\.\PhysicalDrive0`, DriveIndex: 0, PCIAddr: qmp.PCIAddr{Domain: 0, Bus: 7, Slot: 0, Function: 0}},
		{Name: `\\.\PhysicalDrive1`, DriveIndex: 1, PCIAddr: qmp.PCIAddr{Domain: 0, Bus: 8, Slot: 0, Function: 0}},
		{Name: `\\.\PhysicalDrive2`, DriveIndex: 2, PCIAddr: qmp.PCIAddr{Domain: 0, Bus: 9, Slot: 0, Function: 0}},
	}

	dm, err := BuildDiskMapping(testDomainXML, guestDisks)
	if err != nil {
		t.Fatalf("BuildDiskMapping() error = %v", err)
	}

	expected := map[int]string{
		0: "vol-0",
		1: "vol-1",
		2: "vol-2",
	}

	if len(dm) != len(expected) {
		t.Fatalf("expected %d mappings, got %d: %v", len(expected), len(dm), dm)
	}

	for idx, wantVol := range expected {
		if gotVol, ok := dm[idx]; !ok {
			t.Errorf("missing drive index %d", idx)
		} else if gotVol != wantVol {
			t.Errorf("drive index %d: got %q, want %q", idx, gotVol, wantVol)
		}
	}
}

func TestBuildDiskMapping_PartialMatch(t *testing.T) {
	guestDisks := []GuestDisk{
		{Name: `\\.\PhysicalDrive0`, DriveIndex: 0, PCIAddr: qmp.PCIAddr{Domain: 0, Bus: 7, Slot: 0, Function: 0}},
		{Name: `\\.\PhysicalDrive1`, DriveIndex: 1, PCIAddr: qmp.PCIAddr{Domain: 0, Bus: 99, Slot: 0, Function: 0}},
	}

	dm, err := BuildDiskMapping(testDomainXML, guestDisks)
	if err != nil {
		t.Fatalf("BuildDiskMapping() error = %v", err)
	}

	if len(dm) != 1 {
		t.Fatalf("expected 1 mapping (partial), got %d: %v", len(dm), dm)
	}
	if dm[0] != "vol-0" {
		t.Errorf("drive 0: got %q, want %q", dm[0], "vol-0")
	}
}

func TestBuildDiskMapping_NoGuestDisks(t *testing.T) {
	dm, err := BuildDiskMapping(testDomainXML, nil)
	if err != nil {
		t.Fatalf("BuildDiskMapping() error = %v", err)
	}
	if len(dm) != 0 {
		t.Errorf("expected empty mapping, got %v", dm)
	}
}
