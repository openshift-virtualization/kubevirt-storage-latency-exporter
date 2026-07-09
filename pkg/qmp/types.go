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
	virtioRegex = regexp.MustCompile(`^/machine/peripheral/(ua-(.+?)/.+)$`)
	flatRegex   = regexp.MustCompile(`^ua-(.+)$`)
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

// PCIAddr identifies a PCI device by its domain/bus/slot/function.
type PCIAddr struct {
	Domain   int
	Bus      int
	Slot     int
	Function int
}

type xmlDomain struct {
	Devices struct {
		Disks []xmlDisk `xml:"disk"`
	} `xml:"devices"`
}

type xmlDisk struct {
	Device  string   `xml:"device,attr"`
	Alias   xmlAlias `xml:"alias"`
	Address xmlAddr  `xml:"address"`
}

type xmlAlias struct {
	Name string `xml:"name,attr"`
}

type xmlAddr struct {
	Type     string `xml:"type,attr"`
	Domain   string `xml:"domain,attr"`
	Bus      string `xml:"bus,attr"`
	Slot     string `xml:"slot,attr"`
	Function string `xml:"function,attr"`
}

// ParseDiskAddresses extracts PCI addresses for each ua-* aliased disk
// from a libvirt domain XML string.
// Returns a map of PCIAddr -> KubeVirt volume name.
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
