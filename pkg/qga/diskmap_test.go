package qga

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift-virtualization/kubevirt-metrics-exporter/pkg/qmp"
)

var _ = Describe("ParseDiskIndex", func() {
	DescribeTable("should parse disk index from name string",
		func(input string, want int, wantOK bool) {
			got, ok := ParseDiskIndex(input)
			Expect(ok).To(Equal(wantOK))
			Expect(got).To(Equal(want))
		},
		Entry("standard", "0 C:", 0, true),
		Entry("second disk", "1 E:", 1, true),
		Entry("third disk", "2 F:", 2, true),
		Entry("no partition", "3", 3, true),
		Entry("double digit", "10 Z:", 10, true),
		Entry("empty string", "", 0, false),
		Entry("non-numeric", "abc", 0, false),
		Entry("total", "_Total", 0, false),
		Entry("spaces only", "  ", 0, false),
	)
})

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

var _ = Describe("parseGuestGetDisks", func() {
	It("should parse physical drive entries", func() {
		input := []byte(`{"return":[
			{"name":"\\\\.\\PhysicalDrive0","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0,"pci-controller":{"bus":7,"slot":0,"domain":0,"function":0},"dev":"\\\\.\\PhysicalDrive0","target":0}},
			{"name":"\\\\.\\PhysicalDrive1","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0,"pci-controller":{"bus":8,"slot":0,"domain":0,"function":0},"dev":"\\\\.\\PhysicalDrive1","target":0}},
			{"name":"\\\\.\\PhysicalDrive2","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0,"pci-controller":{"bus":9,"slot":0,"domain":0,"function":0},"dev":"\\\\.\\PhysicalDrive2","target":0}}
		]}`)

		disks, err := parseGuestGetDisks(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(disks).To(HaveLen(3))
		Expect(disks[0].DriveIndex).To(Equal(0))
		Expect(disks[0].Location.Controller.Bus).To(Equal(7))
		Expect(disks[1].DriveIndex).To(Equal(1))
		Expect(disks[1].Location.Controller.Bus).To(Equal(8))
		Expect(disks[2].DriveIndex).To(Equal(2))
		Expect(disks[2].Location.Controller.Bus).To(Equal(9))
	})

	It("should skip partition entries", func() {
		input := []byte(`{"return":[
			{"name":"\\\\.\\PhysicalDrive0","partition":false,"address":{"bus-type":"scsi","pci-controller":{"bus":7,"slot":0,"domain":0,"function":0}}},
			{"name":"C:\\","partition":true}
		]}`)

		disks, err := parseGuestGetDisks(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(disks).To(HaveLen(1))
	})

	It("should skip entries without PCI controller", func() {
		input := []byte(`{"return":[
			{"name":"\\\\.\\PhysicalDrive0","partition":false,"address":{"bus-type":"scsi","bus":0,"unit":0}}
		]}`)

		disks, err := parseGuestGetDisks(input)
		Expect(err).NotTo(HaveOccurred())
		Expect(disks).To(BeEmpty())
	})
})

var _ = Describe("parsePhysicalDriveIndex", func() {
	DescribeTable("should parse drive index from name",
		func(input string, want int, wantOK bool) {
			got, ok := parsePhysicalDriveIndex(input)
			Expect(ok).To(Equal(wantOK))
			Expect(got).To(Equal(want))
		},
		Entry("drive 0", `\\.\PhysicalDrive0`, 0, true),
		Entry("drive 1", `\\.\PhysicalDrive1`, 1, true),
		Entry("drive 10", `\\.\PhysicalDrive10`, 10, true),
		Entry("no prefix", `PhysicalDrive2`, 2, true),
		Entry("unrelated", `C:\Windows`, 0, false),
		Entry("empty", "", 0, false),
	)
})

var _ = Describe("BuildDiskMapping", func() {
	It("should map drive indices to volume names", func() {
		guestDisks := []GuestDisk{
			{Name: `\\.\PhysicalDrive0`, DriveIndex: 0, Location: qmp.DiskLocation{Controller: qmp.PCIAddr{Domain: 0, Bus: 7, Slot: 0, Function: 0}}},
			{Name: `\\.\PhysicalDrive1`, DriveIndex: 1, Location: qmp.DiskLocation{Controller: qmp.PCIAddr{Domain: 0, Bus: 8, Slot: 0, Function: 0}}},
			{Name: `\\.\PhysicalDrive2`, DriveIndex: 2, Location: qmp.DiskLocation{Controller: qmp.PCIAddr{Domain: 0, Bus: 9, Slot: 0, Function: 0}}},
		}

		dm, err := BuildDiskMapping(testDomainXML, guestDisks)
		Expect(err).NotTo(HaveOccurred())
		Expect(dm).To(HaveLen(3))
		Expect(dm[0]).To(Equal("vol-0"))
		Expect(dm[1]).To(Equal("vol-1"))
		Expect(dm[2]).To(Equal("vol-2"))
	})

	It("should produce a partial mapping when some PCI addresses don't match", func() {
		guestDisks := []GuestDisk{
			{Name: `\\.\PhysicalDrive0`, DriveIndex: 0, Location: qmp.DiskLocation{Controller: qmp.PCIAddr{Domain: 0, Bus: 7, Slot: 0, Function: 0}}},
			{Name: `\\.\PhysicalDrive1`, DriveIndex: 1, Location: qmp.DiskLocation{Controller: qmp.PCIAddr{Domain: 0, Bus: 99, Slot: 0, Function: 0}}},
		}

		dm, err := BuildDiskMapping(testDomainXML, guestDisks)
		Expect(err).NotTo(HaveOccurred())
		Expect(dm).To(HaveLen(1))
		Expect(dm[0]).To(Equal("vol-0"))
	})

	It("should return empty mapping when no guest disks provided", func() {
		dm, err := BuildDiskMapping(testDomainXML, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(dm).To(BeEmpty())
	})

	It("should map SCSI disks using controller PCI address and target", func() {
		const scsiDomainXML = `<domain type='kvm'>
  <devices>
    <controller type='scsi' index='0' model='virtio-scsi'>
      <alias name='scsi0'/>
      <address type='pci' domain='0x0000' bus='0x05' slot='0x00' function='0x0'/>
    </controller>
    <disk type='file' device='disk'>
      <target dev='sda' bus='scsi'/>
      <alias name='ua-scsi-vol-0'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
    <disk type='file' device='disk'>
      <target dev='sdb' bus='scsi'/>
      <alias name='ua-scsi-vol-1'/>
      <address type='drive' controller='0' bus='0' target='1' unit='0'/>
    </disk>
  </devices>
</domain>`

		guestDisks := []GuestDisk{
			{Name: `\\.\PhysicalDrive0`, DriveIndex: 0, Location: qmp.DiskLocation{
				Controller: qmp.PCIAddr{Domain: 0, Bus: 5, Slot: 0, Function: 0},
				Target:     0,
			}},
			{Name: `\\.\PhysicalDrive1`, DriveIndex: 1, Location: qmp.DiskLocation{
				Controller: qmp.PCIAddr{Domain: 0, Bus: 5, Slot: 0, Function: 0},
				Target:     1,
			}},
		}

		dm, err := BuildDiskMapping(scsiDomainXML, guestDisks)
		Expect(err).NotTo(HaveOccurred())
		Expect(dm).To(HaveLen(2))
		Expect(dm[0]).To(Equal("scsi-vol-0"))
		Expect(dm[1]).To(Equal("scsi-vol-1"))
	})

	It("should fall back to serial matching for SATA disks with invalid PCI controller", func() {
		const sataDomainXML = `<domain type='kvm'>
  <devices>
    <controller type='sata' index='0'>
      <address type='pci' domain='0x0000' bus='0x00' slot='0x1f' function='0x2'/>
    </controller>
    <disk type='file' device='disk'>
      <target dev='sda' bus='sata'/>
      <serial>my-serial-123</serial>
      <alias name='ua-sata-disk'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
  </devices>
</domain>`

		guestDisks := []GuestDisk{
			{Name: `\\.\PhysicalDrive0`, DriveIndex: 0, Serial: "my-serial-123", Location: qmp.DiskLocation{
				Controller: qmp.PCIAddr{Domain: -1, Bus: -1, Slot: -1, Function: -1},
			}},
		}

		dm, err := BuildDiskMapping(sataDomainXML, guestDisks)
		Expect(err).NotTo(HaveOccurred())
		Expect(dm).To(HaveLen(1))
		Expect(dm[0]).To(Equal("sata-disk"))
	})
})
