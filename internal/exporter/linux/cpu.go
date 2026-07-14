package linux

import (
	"os"
	"strconv"
	"strings"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// collectCPU gathers all CPU-related metrics (§12.2).
func (c *HostCollector) collectCPU() ([]exporter.MetricFamily, error) {
	model := c.cpuModel()
	sockets, cores, threads := c.cpuTopology()
	usageRatio := c.cpuUsageRatio()

	families := []exporter.MetricFamily{
		{
			Name: shared.MetricHostCPUInfo,
			Help: "CPU model information.",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{
					shared.LabelHostID: c.hostID,
					shared.LabelModel:  model,
				},
				Value: 1,
			}},
		},
		{
			Name: shared.MetricHostCPUSockets,
			Help: "Number of physical CPU sockets.",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{shared.LabelHostID: c.hostID},
				Value:  float64(sockets),
			}},
		},
		{
			Name: shared.MetricHostCPUCores,
			Help: "Number of physical CPU cores.",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{shared.LabelHostID: c.hostID},
				Value:  float64(cores),
			}},
		},
		{
			Name: shared.MetricHostCPUThreads,
			Help: "Number of hardware threads.",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{shared.LabelHostID: c.hostID},
				Value:  float64(threads),
			}},
		},
	}

	if usageRatio >= 0 {
		families = append(families, exporter.MetricFamily{
			Name: shared.MetricHostCPUUsageRatio,
			Help: "Current CPU usage ratio (0-1).",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{shared.LabelHostID: c.hostID},
				Value:  usageRatio,
			}},
		})
	}

	return families, nil
}

// cpuModel reads the CPU model name from /proc/cpuinfo.
func (c *HostCollector) cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				model := strings.TrimSpace(parts[1])
				return shared.CleanDescription(model)
			}
		}
	}
	return ""
}

// cpuTopology counts sockets, cores, and threads from /proc/cpuinfo.
func (c *HostCollector) cpuTopology() (sockets, cores, threads int) {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0, 0, 0
	}

	physicalIDs := make(map[string]struct{})
	coreIDs := make(map[string]struct{})
	threadCount := 0

	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "processor"):
			threadCount++
		case strings.HasPrefix(line, "physical id"):
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				physicalIDs[strings.TrimSpace(parts[1])] = struct{}{}
			}
		case strings.HasPrefix(line, "core id"):
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				coreIDs[strings.TrimSpace(parts[1])] = struct{}{}
			}
		}
	}

	sockets = len(physicalIDs)
	cores = len(coreIDs)
	threads = threadCount

	if sockets == 0 {
		sockets = 1
	}
	if cores == 0 {
		cores = threadCount
	}
	if threads == 0 {
		threads = cores
	}

	return
}

// cpuUsageRatio computes a simple point-in-time CPU usage ratio from /proc/stat.
// Returns -1 if unavailable.
func (c *HostCollector) cpuUsageRatio() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return -1
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return -1
		}
		// Fields: cpu user nice system idle iowait irq softirq ...
		var total, idle float64
		for i, f := range fields[1:] {
			val, err := strconv.ParseFloat(f, 64)
			if err != nil {
				continue
			}
			total += val
			if i == 3 || i == 4 { // idle + iowait
				idle += val
			}
		}
		if total == 0 {
			return -1
		}
		return 1.0 - (idle / total)
	}
	return -1
}
