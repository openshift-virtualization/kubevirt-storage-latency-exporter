package qmp

import (
	"context"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
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

var _ = Describe("ParseDiskAddresses", func() {
	It("should parse PCI-addressed ua- disks", func() {
		result, err := ParseDiskAddresses(testDomainXML)
		Expect(err).NotTo(HaveOccurred())

		expected := map[PCIAddr]string{
			{Domain: 0, Bus: 7, Slot: 0, Function: 0}: "vol-0",
			{Domain: 0, Bus: 8, Slot: 0, Function: 0}: "vol-1",
			{Domain: 0, Bus: 9, Slot: 0, Function: 0}: "vol-2",
		}
		Expect(result).To(Equal(expected))
	})

	It("should skip cdrom devices", func() {
		result, err := ParseDiskAddresses(testDomainXML)
		Expect(err).NotTo(HaveOccurred())
		for _, name := range result {
			Expect(name).NotTo(Equal("cd-rom"))
		}
	})

	It("should skip disks with non-PCI addresses", func() {
		xml := `<domain><devices>
    <disk type='block' device='disk'>
      <alias name='ua-sata-disk'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
  </devices></domain>`

		result, err := ParseDiskAddresses(xml)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeEmpty())
	})

	It("should skip disks without ua- alias", func() {
		xml := `<domain><devices>
    <disk type='block' device='disk'>
      <alias name='virtio-disk0'/>
      <address type='pci' domain='0x0000' bus='0x05' slot='0x00' function='0x0'/>
    </disk>
  </devices></domain>`

		result, err := ParseDiskAddresses(xml)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeEmpty())
	})

	It("should return empty map for empty devices", func() {
		result, err := ParseDiskAddresses("<domain><devices></devices></domain>")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("ParseDiskLocations", func() {
	It("should parse virtio-blk disks", func() {
		result, err := ParseDiskLocations(testDomainXML)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(3))

		loc := DiskLocation{Controller: PCIAddr{Domain: 0, Bus: 7, Slot: 0, Function: 0}}
		Expect(result[loc]).To(Equal("vol-0"))
	})

	It("should parse SATA disks using controller PCI address", func() {
		const sataDomainXML = `<domain type='kvm'>
  <devices>
    <controller type='sata' index='0'>
      <alias name='sata0'/>
      <address type='pci' domain='0x0000' bus='0x00' slot='0x1f' function='0x2'/>
    </controller>
    <disk type='file' device='disk'>
      <target dev='sda' bus='sata'/>
      <alias name='ua-rootdisk'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
    <disk type='file' device='disk'>
      <target dev='sdb' bus='sata'/>
      <alias name='ua-datadisk'/>
      <address type='drive' controller='0' bus='0' target='0' unit='1'/>
    </disk>
  </devices>
</domain>`

		result, err := ParseDiskLocations(sataDomainXML)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(2))

		controllerPCI := PCIAddr{Domain: 0, Bus: 0, Slot: 31, Function: 2}
		loc0 := DiskLocation{Controller: controllerPCI, Bus: 0, Target: 0, Unit: 0}
		Expect(result[loc0]).To(Equal("rootdisk"))

		loc1 := DiskLocation{Controller: controllerPCI, Bus: 0, Target: 0, Unit: 1}
		Expect(result[loc1]).To(Equal("datadisk"))
	})

	It("should parse mixed virtio-blk, SCSI, and SATA disks", func() {
		const mixedDomainXML = `<domain type='kvm'>
  <devices>
    <controller type='scsi' index='0' model='virtio-scsi'>
      <address type='pci' domain='0x0000' bus='0x06' slot='0x00' function='0x0'/>
    </controller>
    <controller type='sata' index='0'>
      <address type='pci' domain='0x0000' bus='0x00' slot='0x1f' function='0x2'/>
    </controller>
    <disk type='block' device='disk'>
      <target dev='vda' bus='virtio'/>
      <alias name='ua-rootdisk'/>
      <address type='pci' domain='0x0000' bus='0x08' slot='0x00' function='0x0'/>
    </disk>
    <disk type='block' device='disk'>
      <target dev='vdb' bus='virtio'/>
      <alias name='ua-dddd'/>
      <address type='pci' domain='0x0000' bus='0x09' slot='0x00' function='0x0'/>
    </disk>
    <disk type='file' device='disk'>
      <target dev='sda' bus='scsi'/>
      <alias name='ua-scsi-vol'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
    <disk type='file' device='disk'>
      <target dev='sdb' bus='sata'/>
      <alias name='ua-sata-vol'/>
      <address type='drive' controller='0' bus='0' target='0' unit='1'/>
    </disk>
  </devices>
</domain>`

		result, err := ParseDiskLocations(mixedDomainXML)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(HaveLen(4))

		rootLoc := DiskLocation{Controller: PCIAddr{Domain: 0, Bus: 8, Slot: 0, Function: 0}}
		Expect(result[rootLoc]).To(Equal("rootdisk"))

		ddddLoc := DiskLocation{Controller: PCIAddr{Domain: 0, Bus: 9, Slot: 0, Function: 0}}
		Expect(result[ddddLoc]).To(Equal("dddd"))

		scsiLoc := DiskLocation{Controller: PCIAddr{Domain: 0, Bus: 6, Slot: 0, Function: 0}, Target: 0, Unit: 0}
		Expect(result[scsiLoc]).To(Equal("scsi-vol"))

		sataLoc := DiskLocation{Controller: PCIAddr{Domain: 0, Bus: 0, Slot: 31, Function: 2}, Target: 0, Unit: 1}
		Expect(result[sataLoc]).To(Equal("sata-vol"))
	})
})

var _ = Describe("FetchPVCMap", func() {
	It("should extract PVC claim names from VMI volumeStatus", func() {
		vmi := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "kubevirt.io/v1",
				"kind":       "VirtualMachineInstance",
				"metadata": map[string]interface{}{
					"name":      "test-vm",
					"namespace": "default",
				},
				"status": map[string]interface{}{
					"volumeStatus": []interface{}{
						map[string]interface{}{
							"name": "rootdisk",
							"persistentVolumeClaimInfo": map[string]interface{}{
								"claimName": "root-pvc",
							},
						},
						map[string]interface{}{
							"name": "datadisk",
							"persistentVolumeClaimInfo": map[string]interface{}{
								"claimName": "data-pvc",
							},
						},
						map[string]interface{}{
							"name":   "cloudinit",
							"reason": "no pvc",
						},
					},
				},
			},
		}

		scheme := runtime.NewScheme()
		client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
			map[schema.GroupVersionResource]string{
				vmiResource: "VirtualMachineInstanceList",
			},
			vmi,
		)

		result := FetchPVCMap(context.Background(), client, "default", "test-vm", slog.Default())
		Expect(result).To(HaveLen(2))
		Expect(result["rootdisk"]).To(Equal("root-pvc"))
		Expect(result["datadisk"]).To(Equal("data-pvc"))
	})

	It("should return empty map for nil client", func() {
		result := FetchPVCMap(context.Background(), nil, "default", "test-vm", slog.Default())
		Expect(result).To(BeEmpty())
	})

	It("should return empty map when VMI is not found", func() {
		scheme := runtime.NewScheme()
		client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
			map[schema.GroupVersionResource]string{
				vmiResource: "VirtualMachineInstanceList",
			},
		)

		result := FetchPVCMap(context.Background(), client, "default", "nonexistent", slog.Default())
		Expect(result).To(BeEmpty())
	})
})
