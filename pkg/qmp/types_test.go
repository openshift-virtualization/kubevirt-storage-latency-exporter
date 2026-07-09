package qmp

import (
	"testing"
)

const testDomainXML = `<domain type='kvm'>
  <name>default_my-vm</name>
  <devices>
    <disk type='block' device='disk' model='virtio-non-transitional'>
      <driver name='qemu' type='raw'/>
      <source dev='/dev/vol-0'/>
      <target dev='vda' bus='virtio'/>
      <alias name='ua-vol-0'/>
      <address type='pci' domain='0x0000' bus='0x07' slot='0x00' function='0x0'/>
    </disk>
    <disk type='block' device='disk' model='virtio-non-transitional'>
      <driver name='qemu' type='raw'/>
      <source dev='/dev/vol-1'/>
      <target dev='vdb' bus='virtio'/>
      <alias name='ua-vol-1'/>
      <address type='pci' domain='0x0000' bus='0x08' slot='0x00' function='0x0'/>
    </disk>
    <disk type='block' device='disk' model='virtio-non-transitional'>
      <driver name='qemu' type='raw'/>
      <source dev='/dev/vol-2'/>
      <target dev='vdc' bus='virtio'/>
      <alias name='ua-vol-2'/>
      <address type='pci' domain='0x0000' bus='0x09' slot='0x00' function='0x0'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='/var/run/kubevirt-private/vmi-disks/cd-rom/disk.img'/>
      <target dev='sda' bus='sata'/>
      <alias name='ua-cd-rom'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
    <controller type='scsi' index='0' model='virtio-non-transitional'>
      <alias name='scsi0'/>
      <address type='pci' domain='0x0000' bus='0x05' slot='0x00' function='0x0'/>
    </controller>
  </devices>
</domain>`

func TestParseDiskAddresses(t *testing.T) {
	result, err := ParseDiskAddresses(testDomainXML)
	if err != nil {
		t.Fatalf("ParseDiskAddresses() error = %v", err)
	}

	expected := map[PCIAddr]string{
		{Domain: 0, Bus: 7, Slot: 0, Function: 0}: "vol-0",
		{Domain: 0, Bus: 8, Slot: 0, Function: 0}: "vol-1",
		{Domain: 0, Bus: 9, Slot: 0, Function: 0}: "vol-2",
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(result), result)
	}

	for addr, wantName := range expected {
		if gotName, ok := result[addr]; !ok {
			t.Errorf("missing PCI addr %+v", addr)
		} else if gotName != wantName {
			t.Errorf("PCI addr %+v: got %q, want %q", addr, gotName, wantName)
		}
	}
}

func TestParseDiskAddresses_SkipsCDROM(t *testing.T) {
	result, err := ParseDiskAddresses(testDomainXML)
	if err != nil {
		t.Fatalf("ParseDiskAddresses() error = %v", err)
	}

	for _, name := range result {
		if name == "cd-rom" {
			t.Error("cd-rom should be skipped (device='cdrom')")
		}
	}
}

func TestParseDiskAddresses_SkipsNonPCI(t *testing.T) {
	xml := `<domain><devices>
    <disk type='block' device='disk'>
      <alias name='ua-sata-disk'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
  </devices></domain>`

	result, err := ParseDiskAddresses(xml)
	if err != nil {
		t.Fatalf("ParseDiskAddresses() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entries for non-PCI address, got %d", len(result))
	}
}

func TestParseDiskAddresses_SkipsNonUAAlias(t *testing.T) {
	xml := `<domain><devices>
    <disk type='block' device='disk'>
      <alias name='virtio-disk0'/>
      <address type='pci' domain='0x0000' bus='0x05' slot='0x00' function='0x0'/>
    </disk>
  </devices></domain>`

	result, err := ParseDiskAddresses(xml)
	if err != nil {
		t.Fatalf("ParseDiskAddresses() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entries for non-ua alias, got %d", len(result))
	}
}

func TestParseDiskAddresses_EmptyXML(t *testing.T) {
	result, err := ParseDiskAddresses("<domain><devices></devices></domain>")
	if err != nil {
		t.Fatalf("ParseDiskAddresses() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result))
	}
}
