package linux

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// memInfo holds parsed /proc/meminfo values.
type memInfo struct {
	memTotal     int64
	memAvailable int64
	hugepages    []hugepageEntry
}

type hugepageEntry struct {
	pageSizeKB int64
	total      int64
	free       int64
}

// collectMemory gathers all memory-related metrics (§12.3).
func (c *HostCollector) collectMemory() ([]exporter.MetricFamily, error) {
	info, err := parseMemInfo()
	if err != nil {
		return nil, err
	}

	families := []exporter.MetricFamily{
		{
			Name: shared.MetricHostMemoryTotalBytes,
			Help: "Total physical memory in bytes.",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{shared.LabelHostID: c.hostID},
				Value:  float64(info.memTotal * 1024),
			}},
		},
		{
			Name: shared.MetricHostMemoryAvailableBytes,
			Help: "Available memory in bytes (MemAvailable).",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{shared.LabelHostID: c.hostID},
				Value:  float64(info.memAvailable * 1024),
			}},
		},
	}

	for _, hp := range info.hugepages {
		pageSizeBytes := hp.pageSizeKB * 1024
		hpLabels := map[string]string{
			shared.LabelHostID:        c.hostID,
			shared.LabelPageSizeBytes: strconv.FormatInt(pageSizeBytes, 10),
		}

		families = append(families,
			exporter.MetricFamily{
				Name: shared.MetricHostHugepagesTotalBytes,
				Help: "Total hugepage memory in bytes for this page size.",
				Type: "gauge",
				Metrics: []exporter.Metric{{
					Labels: hpLabels,
					Value:  float64(hp.total * 1024),
				}},
			},
			exporter.MetricFamily{
				Name: shared.MetricHostHugepagesFreeBytes,
				Help: "Free hugepage memory in bytes for this page size.",
				Type: "gauge",
				Metrics: []exporter.Metric{{
					Labels: hpLabels,
					Value:  float64(hp.free * 1024),
				}},
			},
		)
	}

	return families, nil
}

// parseMemInfo reads and parses /proc/meminfo.
func parseMemInfo() (*memInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info := &memInfo{}
	hugepageMap := make(map[int64]*hugepageEntry)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)
		val, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			continue
		}

		switch key {
		case "MemTotal":
			info.memTotal = val
		case "MemAvailable":
			info.memAvailable = val
		default:
			// Parse hugepage entries like "HugePages_Total" and "Hugepagesize".
			if strings.HasPrefix(key, "HugePages_") {
				suffix := strings.TrimPrefix(key, "HugePages_")
				switch suffix {
				case "Total":
					// Default hugepage size: read Hugepagesize separately.
					ensureHugepageEntry(hugepageMap, 0).total = val
				case "Free":
					ensureHugepageEntry(hugepageMap, 0).free = val
				}
			} else if key == "Hugepagesize" {
				ensureHugepageEntry(hugepageMap, 0).pageSizeKB = val
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	for _, hp := range hugepageMap {
		if hp.pageSizeKB > 0 {
			info.hugepages = append(info.hugepages, *hp)
		}
	}

	return info, nil
}

func ensureHugepageEntry(m map[int64]*hugepageEntry, key int64) *hugepageEntry {
	if e, ok := m[key]; ok {
		return e
	}
	e := &hugepageEntry{}
	m[key] = e
	return e
}
