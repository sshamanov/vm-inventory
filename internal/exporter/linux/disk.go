package linux

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// collectDisks gathers physical block device metrics (§12.4).
func (c *HostCollector) collectDisks() ([]exporter.MetricFamily, error) {
	devices, err := listBlockDevices()
	if err != nil {
		return nil, err
	}

	var infoMetrics, sizeMetrics []exporter.Metric
	for _, dev := range devices {
		labels := map[string]string{
			shared.LabelHostID:   c.hostID,
			shared.LabelDeviceID: dev.name,
		}
		infoLabels := copyLabels(labels)
		infoLabels[shared.LabelDeviceName] = dev.name
		infoLabels[shared.LabelModel] = dev.model

		infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})
		sizeMetrics = append(sizeMetrics, exporter.Metric{Labels: labels, Value: float64(dev.sizeBytes)})
	}

	return []exporter.MetricFamily{
		{
			Name:    shared.MetricHostBlockDeviceInfo,
			Help:    "Physical block device information.",
			Type:    "gauge",
			Metrics: infoMetrics,
		},
		{
			Name:    shared.MetricHostBlockDeviceBytes,
			Help:    "Physical block device size in bytes.",
			Type:    "gauge",
			Metrics: sizeMetrics,
		},
	}, nil
}

// collectFilesystems gathers filesystem metrics (§12.5).
func (c *HostCollector) collectFilesystems() ([]exporter.MetricFamily, error) {
	mounts, err := parseMounts()
	if err != nil {
		return nil, err
	}

	// Deduplicate by filesystem identity.
	seen := make(map[string]struct{})
	var infoMetrics, mountMetrics, totalMetrics, availMetrics []exporter.Metric

	for _, m := range mounts {
		if m.fsType != shared.FSTypeExt4 && m.fsType != shared.FSTypeBtrfs {
			continue
		}
		fsID := m.fsUUID
		if fsID == "" {
			fsID = m.device
		}

		labels := map[string]string{
			shared.LabelHostID:       c.hostID,
			shared.LabelFilesystemID: fsID,
		}

		if _, ok := seen[fsID]; !ok {
			seen[fsID] = struct{}{}
			infoLabels := copyLabels(labels)
			infoLabels[shared.LabelFilesystemType] = m.fsType
			infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})

			totalMetrics = append(totalMetrics, exporter.Metric{Labels: labels, Value: float64(m.totalBytes)})
			availMetrics = append(availMetrics, exporter.Metric{Labels: labels, Value: float64(m.availBytes)})
		}

		mntLabels := copyLabels(labels)
		mntLabels[shared.LabelMountpoint] = m.mountpoint
		mountMetrics = append(mountMetrics, exporter.Metric{Labels: mntLabels, Value: 1})
	}

	return []exporter.MetricFamily{
		{
			Name:    shared.MetricHostFilesystemInfo,
			Help:    "Filesystem information.",
			Type:    "gauge",
			Metrics: infoMetrics,
		},
		{
			Name:    shared.MetricHostFilesystemMountInfo,
			Help:    "Filesystem mountpoint relationship.",
			Type:    "gauge",
			Metrics: mountMetrics,
		},
		{
			Name:    shared.MetricHostFilesystemTotalBytes,
			Help:    "Filesystem total capacity in bytes.",
			Type:    "gauge",
			Metrics: totalMetrics,
		},
		{
			Name:    shared.MetricHostFilesystemAvailBytes,
			Help:    "Filesystem available capacity in bytes.",
			Type:    "gauge",
			Metrics: availMetrics,
		},
	}, nil
}

type blockDevice struct {
	name      string
	model     string
	sizeBytes int64
}

type mountEntry struct {
	device     string
	mountpoint string
	fsType     string
	fsUUID     string
	totalBytes int64
	availBytes int64
}

func listBlockDevices() ([]blockDevice, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, err
	}

	var devices []blockDevice
	for _, entry := range entries {
		name := entry.Name()
		// Skip loop, ram, and other virtual devices.
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") {
			continue
		}

		sizePath := filepath.Join("/sys/block", name, "size")
		sizeData, err := os.ReadFile(sizePath)
		if err != nil {
			continue
		}
		// Size is in 512-byte sectors.
		sizeSectors, err := strconv.ParseInt(strings.TrimSpace(string(sizeData)), 10, 64)
		if err != nil {
			continue
		}

		modelPath := filepath.Join("/sys/block", name, "device/model")
		modelData, _ := os.ReadFile(modelPath)
		model := strings.TrimSpace(string(modelData))

		devices = append(devices, blockDevice{
			name:      name,
			model:     shared.CleanDescription(model),
			sizeBytes: sizeSectors * 512,
		})
	}

	return devices, nil
}

func parseMounts() ([]mountEntry, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mounts []mountEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		device := fields[0]
		mountpoint := fields[1]
		fsType := fields[2]

		if fsType != shared.FSTypeExt4 && fsType != shared.FSTypeBtrfs {
			continue
		}

		// Skip pseudo/container/system mounts.
		if strings.HasPrefix(mountpoint, "/proc/") ||
			strings.HasPrefix(mountpoint, "/sys/") ||
			strings.HasPrefix(mountpoint, "/dev/") ||
			strings.HasPrefix(mountpoint, "/run/") ||
			strings.HasPrefix(mountpoint, "/snap/") ||
			strings.HasPrefix(mountpoint, "/var/lib/docker/") ||
			strings.HasPrefix(mountpoint, "/var/lib/lxc") {
			continue
		}

		mounts = append(mounts, mountEntry{
			device:     device,
			mountpoint: mountpoint,
			fsType:     fsType,
		})
	}
	return mounts, scanner.Err()
}

func copyLabels(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// --- Utility functions used by host.go ---

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func parseOSRelease() (name, version string) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ID=") {
			name = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
		}
		if strings.HasPrefix(line, "VERSION_ID=") {
			version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		}
	}
	if name == "" {
		name = "Linux"
	}
	return
}
