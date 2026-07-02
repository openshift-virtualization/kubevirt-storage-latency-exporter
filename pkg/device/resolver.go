package device

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

var (
	// Filesystem-mode volumes: .../pods/<pod-uid>/volumes/<plugin>/<pv-name>[/mount]
	kubeletVolumeRe = regexp.MustCompile(
		`.*/pods/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/volumes/[^/]+/([^/]+)(?:/mount)?$`,
	)
	// Block-mode volumes (CSI): .../volumeDevices/publish/<pv-name>/<pod-uid>
	kubeletBlockDeviceRe = regexp.MustCompile(
		`.*/volumeDevices/publish/([^/]+)/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`,
	)
)

type DeviceInfo struct {
	PVCName   string
	PodName   string
	Namespace string
	NodeName  string
	MountPath string
	DevPath   string
	IsNFS     bool
}

const PVCByPVIndexName = "byPV"

func PVCByPVIndexFunc(obj interface{}) ([]string, error) {
	pvc, ok := obj.(*corev1.PersistentVolumeClaim)
	if !ok {
		return nil, nil
	}
	if pvc.Spec.VolumeName == "" {
		return nil, nil
	}
	return []string{pvc.Spec.VolumeName}, nil
}

type Resolver struct {
	mu         sync.RWMutex
	devices    map[uint32]DeviceInfo
	nodeName   string
	interval   time.Duration
	procPath   string
	podStore   cache.Store
	pvcIndexer cache.Indexer
	log        *slog.Logger
}

func NewResolver(nodeName, procPath string, interval time.Duration, podStore cache.Store, pvcIndexer cache.Indexer, log *slog.Logger) *Resolver {
	return &Resolver{
		devices:    make(map[uint32]DeviceInfo),
		nodeName:   nodeName,
		interval:   interval,
		procPath:   procPath,
		podStore:   podStore,
		pvcIndexer: pvcIndexer,
		log:        log,
	}
}

func (r *Resolver) Lookup(dev uint32) (DeviceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.devices[dev]
	return info, ok
}

func (r *Resolver) Run(ctx context.Context) {
	r.scan()
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scan()
		}
	}
}

func (r *Resolver) scan() {
	mountInfoPath := fmt.Sprintf("%s/1/mountinfo", r.procPath)
	mounts, err := ParseMountInfo(mountInfoPath)
	if err != nil {
		r.log.Error("parsing mountinfo", "path", mountInfoPath, "error", err)
		return
	}

	podMetas := r.podMetaMap()

	devices := make(map[uint32]DeviceInfo)

	for _, m := range mounts {
		podUID, pvcName, isBlock := parseKubeletBlockDevicePath(m.MountPoint)
		if !isBlock {
			var ok bool
			podUID, pvcName, ok = parseKubeletVolumePath(m.MountPoint)
			if !ok {
				continue
			}
		}

		dev := MkDev(m.Major, m.Minor)

		if isBlock {
			hostPath := fmt.Sprintf("%s/1/root%s", r.procPath, m.MountPoint)
			blockDev, err := statBlockDevice(hostPath)
			if err != nil {
				r.log.Debug("could not stat block device", "path", hostPath, "error", err)
				continue
			}
			dev = blockDev
		}

		pvcName = r.resolvePVCName(pvcName)

		meta, found := podMetas[podUID]
		if !found {
			continue
		}
		info := DeviceInfo{
			PVCName:   pvcName,
			PodName:   meta.Name,
			Namespace: meta.Namespace,
			NodeName:  r.nodeName,
			MountPath: m.MountPoint,
			DevPath:   m.Source,
			IsNFS:     m.FSType == "nfs" || m.FSType == "nfs4",
		}

		devices[dev] = info
		r.log.Debug("resolved kubelet volume",
			"dev", DevToString(dev),
			"pvc", pvcName,
			"podUID", podUID,
			"namespace", info.Namespace,
			"source", m.Source,
			"fsType", m.FSType,
			"blockDevice", isBlock,
		)
	}

	r.mu.Lock()
	r.devices = devices
	r.mu.Unlock()

	r.log.Debug("device scan complete", "resolved", len(devices))
}

type podMeta struct {
	Name      string
	Namespace string
}

func (r *Resolver) podMetaMap() map[string]podMeta {
	result := make(map[string]podMeta)
	if r.podStore == nil {
		return result
	}
	for _, obj := range r.podStore.List() {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		result[string(pod.UID)] = podMeta{Name: pod.Name, Namespace: pod.Namespace}
	}
	return result
}

func (r *Resolver) resolvePVCName(pvName string) string {
	if r.pvcIndexer == nil {
		return pvName
	}
	items, err := r.pvcIndexer.ByIndex(PVCByPVIndexName, pvName)
	if err != nil || len(items) == 0 {
		return pvName
	}
	pvc, ok := items[0].(*corev1.PersistentVolumeClaim)
	if !ok {
		return pvName
	}
	return pvc.Name
}

func parseKubeletVolumePath(mountPoint string) (podUID, pvcName string, ok bool) {
	if matches := kubeletVolumeRe.FindStringSubmatch(mountPoint); matches != nil {
		return matches[1], matches[2], true
	}
	return "", "", false
}

func parseKubeletBlockDevicePath(mountPoint string) (podUID, pvcName string, ok bool) {
	if matches := kubeletBlockDeviceRe.FindStringSubmatch(mountPoint); matches != nil {
		return matches[2], matches[1], true
	}
	return "", "", false
}

func statBlockDevice(path string) (uint32, error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	if stat.Mode&syscall.S_IFBLK == 0 {
		return 0, fmt.Errorf("%s is not a block device", path)
	}
	// Linux encodes rdev as MKDEV(major, minor):
	// major = (rdev >> 8) & 0xfff
	// minor = (rdev & 0xff) | ((rdev >> 12) & 0xfff00)
	major := uint32((stat.Rdev >> 8) & 0xfff)
	minor := uint32((stat.Rdev & 0xff) | ((stat.Rdev >> 12) & 0xfff00))
	return MkDev(major, minor), nil
}

func MkDev(major, minor uint32) uint32 {
	return (major << 20) | minor
}

func DevToString(dev uint32) string {
	major := dev >> 20
	minor := dev & ((1 << 20) - 1)
	return fmt.Sprintf("%d:%d", major, minor)
}
