package qmp

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	virtioRegex         = regexp.MustCompile(`^/machine/peripheral/(ua-(.+?)/.+)$`)
	flatRegex           = regexp.MustCompile(`^ua-(.+)$`)
	peripheralNameRegex = regexp.MustCompile(`^/machine/peripheral/([^/]+)/`)
)

type BlockStatsResponse struct {
	Return []BlockDevice `json:"return"`
}

type BlockDevice struct {
	QDev    string       `json:"qdev"`
	Stats   BlockStats   `json:"stats"`
	Backing *BlockDevice `json:"backing,omitempty"`
}

func (d *BlockDevice) EffectiveQDev() string {
	if d.QDev != "" {
		return d.QDev
	}
	if d.Backing != nil {
		return d.Backing.EffectiveQDev()
	}
	return ""
}

type BlockStats struct {
	RdOperations          uint64       `json:"rd_operations"`
	WrOperations          uint64       `json:"wr_operations"`
	FlushOperations       uint64       `json:"flush_operations"`
	RdTotalTimeNs         uint64       `json:"rd_total_time_ns"`
	WrTotalTimeNs         uint64       `json:"wr_total_time_ns"`
	FlushTotalTimeNs      uint64       `json:"flush_total_time_ns"`
	RdLatencyHistogram    *LatencyHist `json:"rd_latency_histogram,omitempty"`
	WrLatencyHistogram    *LatencyHist `json:"wr_latency_histogram,omitempty"`
	FlushLatencyHistogram *LatencyHist `json:"flush_latency_histogram,omitempty"`
}

type LatencyHist struct {
	Boundaries []float64 `json:"boundaries"`
	Bins       []uint64  `json:"bins"`
}

func ParseBlockStats(data []byte) (*BlockStatsResponse, error) {
	var resp BlockStatsResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing blockstats: %w", err)
	}
	return &resp, nil
}

func ExtractDiskInfo(qdev string) (string, string, bool) {
	if m := virtioRegex.FindStringSubmatch(qdev); len(m) >= 3 {
		return m[2], m[1], true
	}
	if m := flatRegex.FindStringSubmatch(qdev); len(m) >= 2 {
		return m[1], qdev, true
	}
	return "", "", false
}

func HasHistograms(dev *BlockDevice) bool {
	return dev.Stats.RdLatencyHistogram != nil
}

type VirtioDevice struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type VirtioStatus struct {
	Name   string `json:"name"`
	NumVqs int    `json:"num-vqs"`
}

type VirtQueueStatus struct {
	Name     string `json:"name"`
	QueueIdx int    `json:"queue-index"`
	Inuse    uint32 `json:"inuse"`
	VringNum uint32 `json:"vring-num"`
}

func IsVirtioBlk(dev *VirtioDevice) bool {
	return strings.HasPrefix(dev.Name, "virtio-blk")
}

func IsVirtioScsi(dev *VirtioDevice) bool {
	return strings.HasPrefix(dev.Name, "virtio-scsi")
}

// ExtractControllerName returns the peripheral name from a QEMU device path,
// stripping any "ua-" prefix so the result is a clean controller identifier.
func ExtractControllerName(path string) (string, bool) {
	m := peripheralNameRegex.FindStringSubmatch(path)
	if len(m) < 2 {
		return "", false
	}
	return strings.TrimPrefix(m[1], "ua-"), true
}

// PCIAddr identifies a PCI device by its domain/bus/slot/function.
type PCIAddr struct {
	Domain   int
	Bus      int
	Slot     int
	Function int
}

// DiskLocation uniquely identifies a disk from the guest's perspective.
// For virtio-blk: Controller is the disk's own PCI address (Bus/Target/Unit = 0).
// For SATA/SCSI: Controller is the AHCI/SCSI controller's PCI address,
// and Bus/Target/Unit identify the disk on that controller.
type DiskLocation struct {
	Controller PCIAddr
	Bus        int
	Target     int
	Unit       int
}

type xmlDomain struct {
	Devices struct {
		Disks       []xmlDisk       `xml:"disk"`
		Controllers []xmlController `xml:"controller"`
	} `xml:"devices"`
}

type xmlController struct {
	Type    string  `xml:"type,attr"`
	Index   string  `xml:"index,attr"`
	Address xmlAddr `xml:"address"`
}

type xmlDisk struct {
	Device  string    `xml:"device,attr"`
	Serial  string    `xml:"serial"`
	Target  xmlTarget `xml:"target"`
	Alias   xmlAlias  `xml:"alias"`
	Address xmlAddr   `xml:"address"`
}

type xmlTarget struct {
	Bus string `xml:"bus,attr"`
}

type xmlAlias struct {
	Name string `xml:"name,attr"`
}

type xmlAddr struct {
	Type       string `xml:"type,attr"`
	Domain     string `xml:"domain,attr"`
	Bus        string `xml:"bus,attr"`
	Slot       string `xml:"slot,attr"`
	Function   string `xml:"function,attr"`
	Controller string `xml:"controller,attr"`
	Target     string `xml:"target,attr"`
	Unit       string `xml:"unit,attr"`
}

// ParseDiskAddresses extracts PCI addresses for each ua-* aliased disk
// from a libvirt domain XML string. Only handles virtio-blk (type="pci").
// Deprecated: use ParseDiskLocations for full SATA/SCSI/virtio-blk support.
func ParseDiskAddresses(domainXML string) (map[PCIAddr]string, error) {
	var dom xmlDomain
	if err := xml.Unmarshal([]byte(domainXML), &dom); err != nil {
		return nil, fmt.Errorf("parsing domain XML: %w", err)
	}

	result := make(map[PCIAddr]string)
	for _, d := range dom.Devices.Disks {
		if d.Device != "disk" {
			continue
		}
		if d.Address.Type != "pci" {
			continue
		}
		volName := strings.TrimPrefix(d.Alias.Name, "ua-")
		if volName == d.Alias.Name || volName == "" {
			continue
		}

		addr, err := parseHexAddr(d.Address)
		if err != nil {
			continue
		}
		result[addr] = volName
	}
	return result, nil
}

// ParseDiskLocations extracts disk locations for each ua-* aliased disk
// from a libvirt domain XML string. Handles:
//   - virtio-blk (address type="pci"): controller = disk PCI addr, bus/target/unit = 0
//   - SATA/SCSI (address type="drive"): controller = looked up from <controller> elements
func ParseDiskLocations(domainXML string) (map[DiskLocation]string, error) {
	var dom xmlDomain
	if err := xml.Unmarshal([]byte(domainXML), &dom); err != nil {
		return nil, fmt.Errorf("parsing domain XML: %w", err)
	}

	// Build controller index → PCI address map for sata/scsi controllers.
	controllerPCI := make(map[string]PCIAddr) // key: "sata:0", "scsi:0", etc.
	for _, ctrl := range dom.Devices.Controllers {
		if ctrl.Address.Type != "pci" {
			continue
		}
		addr, err := parseHexAddr(ctrl.Address)
		if err != nil {
			continue
		}
		key := ctrl.Type + ":" + ctrl.Index
		controllerPCI[key] = addr
	}

	result := make(map[DiskLocation]string)
	for _, d := range dom.Devices.Disks {
		if d.Device != "disk" {
			continue
		}
		volName := strings.TrimPrefix(d.Alias.Name, "ua-")
		if volName == d.Alias.Name || volName == "" {
			continue
		}

		switch d.Address.Type {
		case "pci":
			addr, err := parseHexAddr(d.Address)
			if err != nil {
				continue
			}
			result[DiskLocation{Controller: addr}] = volName

		case "drive":
			ctrlIdx := d.Address.Controller
			if ctrlIdx == "" {
				ctrlIdx = "0"
			}
			ctrlType := d.Target.Bus
			if ctrlType == "" {
				continue
			}
			ctrlPCI, found := controllerPCI[ctrlType+":"+ctrlIdx]
			if !found {
				continue
			}
			bus, _ := strconv.Atoi(d.Address.Bus)
			target, _ := strconv.Atoi(d.Address.Target)
			unit, _ := strconv.Atoi(d.Address.Unit)
			result[DiskLocation{
				Controller: ctrlPCI,
				Bus:        bus,
				Target:     target,
				Unit:       unit,
			}] = volName
		}
	}
	return result, nil
}

// ParseDiskSerials extracts serial→volumeName mappings from domain XML.
// Only disks with both a ua-* alias and a non-empty <serial> element are included.
func ParseDiskSerials(domainXML string) (map[string]string, error) {
	var dom xmlDomain
	if err := xml.Unmarshal([]byte(domainXML), &dom); err != nil {
		return nil, fmt.Errorf("parsing domain XML: %w", err)
	}

	result := make(map[string]string)
	for _, d := range dom.Devices.Disks {
		if d.Device != "disk" || d.Serial == "" {
			continue
		}
		volName := strings.TrimPrefix(d.Alias.Name, "ua-")
		if volName == d.Alias.Name || volName == "" {
			continue
		}
		result[d.Serial] = volName
	}
	return result, nil
}

func parseHexAddr(a xmlAddr) (PCIAddr, error) {
	domain, err := strconv.ParseInt(strings.TrimPrefix(a.Domain, "0x"), 16, 32)
	if err != nil {
		return PCIAddr{}, err
	}
	bus, err := strconv.ParseInt(strings.TrimPrefix(a.Bus, "0x"), 16, 32)
	if err != nil {
		return PCIAddr{}, err
	}
	slot, err := strconv.ParseInt(strings.TrimPrefix(a.Slot, "0x"), 16, 32)
	if err != nil {
		return PCIAddr{}, err
	}
	fn, err := strconv.ParseInt(strings.TrimPrefix(a.Function, "0x"), 16, 32)
	if err != nil {
		return PCIAddr{}, err
	}
	return PCIAddr{Domain: int(domain), Bus: int(bus), Slot: int(slot), Function: int(fn)}, nil
}
