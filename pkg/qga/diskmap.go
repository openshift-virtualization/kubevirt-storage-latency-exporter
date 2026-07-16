package qga

import (
	"strconv"
	"strings"

	"github.com/openshift-virtualization/kubevirt-metrics-exporter/pkg/qmp"
)

// BuildDiskMapping correlates guest PhysicalDrive indices with KubeVirt
// volume names by joining on DiskLocation (controller PCI address + bus/target/unit),
// falling back to serial number matching for disks that can't be correlated
// by location (e.g. SATA disks where the guest agent reports invalid addresses).
//
// domainXML is the libvirt domain XML (from DomainGetXMLDesc).
// guestDisks comes from the guest-get-disks QGA command.
//
// Returns driveIndex -> volumeName (e.g. 0 -> "vol-0", 1 -> "vol-1").
func BuildDiskMapping(domainXML string, guestDisks []GuestDisk) (map[int]string, error) {
	hostMap, err := qmp.ParseDiskLocations(domainXML)
	if err != nil {
		return nil, err
	}

	result := make(map[int]string, len(guestDisks))
	for _, gd := range guestDisks {
		if volName, ok := hostMap[gd.Location]; ok {
			result[gd.DriveIndex] = volName
		}
	}

	// Fall back to serial matching for disks not mapped by location.
	serialMap, err := qmp.ParseDiskSerials(domainXML)
	if err == nil && len(serialMap) > 0 {
		for _, gd := range guestDisks {
			if _, already := result[gd.DriveIndex]; already {
				continue
			}
			if gd.Serial == "" {
				continue
			}
			if volName, ok := serialMap[gd.Serial]; ok {
				result[gd.DriveIndex] = volName
			}
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
