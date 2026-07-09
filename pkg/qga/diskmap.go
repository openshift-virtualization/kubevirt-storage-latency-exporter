package qga

import (
	"strconv"
	"strings"

	"github.com/openshift-virtualization/kubevirt-metrics-exporter/pkg/qmp"
)

// BuildDiskMapping correlates guest PhysicalDrive indices with KubeVirt
// volume names by joining on PCI address.
//
// domainXML is the libvirt domain XML (from DomainGetXMLDesc).
// guestDisks comes from the guest-get-disks QGA command.
//
// Returns driveIndex -> volumeName (e.g. 0 -> "vol-0", 1 -> "vol-1").
func BuildDiskMapping(domainXML string, guestDisks []GuestDisk) (map[int]string, error) {
	hostMap, err := qmp.ParseDiskAddresses(domainXML)
	if err != nil {
		return nil, err
	}

	result := make(map[int]string, len(guestDisks))
	for _, gd := range guestDisks {
		if volName, ok := hostMap[gd.PCIAddr]; ok {
			result[gd.DriveIndex] = volName
		}
	}
	return result, nil
}

// ParseDiskIndex extracts the leading integer from a Windows PhysicalDisk
// name like "1 E:" -> 1, or "0 C:" -> 0.
func ParseDiskIndex(name string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(name), " ", 2)
	if len(parts) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	return n, true
}
