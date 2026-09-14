package linux

import (
	"context"
	"fmt"
	"time"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// LXDCollector collects LXD instances and storage pools (§10.3).
// Uses the LXD REST API via a Unix socket.
type LXDCollector struct {
	hostID string
	client LXDClient
}

// LXDClient abstracts LXD API operations for testability.
type LXDClient interface {
	Connect() error
	Disconnect() error
	ListInstances() ([]LXDInstance, error)
	ListStoragePools() ([]LXDPool, error)
}

// LXDInstance represents a LXD container's collected data.
type LXDInstance struct {
	Project     string
	Name        string
	Description string
	Arch        string
	OSName      string
	IPs         []string
	CPULimit    *int64 // nil = no explicit limit, fall back to host
	MemLimit    *int64 // nil = fall back to host
	RootDiskBytes *int64 // nil = fall back to pool
	BackingPool string
}

// LXDPool represents a LXD storage pool.
type LXDPool struct {
	Name       string
	Driver     string
	TotalBytes int64
	AvailBytes int64
}

// NewLXDCollector creates a new LXD collector.
func NewLXDCollector(hostID string, client LXDClient) *LXDCollector {
	return &LXDCollector{
		hostID: hostID,
		client: client,
	}
}

// Name returns the collector name.
func (c *LXDCollector) Name() string { return "lxd" }

// Collect gathers LXD instance and pool data.
func (c *LXDCollector) Collect(ctx context.Context) (*exporter.CollectionResult, error) {
	now := time.Now()

	if err := c.client.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to LXD: %w", err)
	}
	defer c.client.Disconnect()

	instances, err := c.client.ListInstances()
	if err != nil {
		return nil, fmt.Errorf("listing instances: %w", err)
	}

	pools, err := c.client.ListStoragePools()
	if err != nil {
		return nil, fmt.Errorf("listing storage pools: %w", err)
	}

	var families []exporter.MetricFamily

	families = append(families, c.buildResourceMetrics(instances)...)
	families = append(families, c.buildPoolMetrics(pools)...)

	return &exporter.CollectionResult{
		Metrics:    families,
		Timestamp:  now,
		SourceHost: c.hostID,
	}, nil
}

func (c *LXDCollector) buildResourceMetrics(instances []LXDInstance) []exporter.MetricFamily {
	var infoMetrics, ipMetrics, cpuMetrics, memMetrics, diskMetrics []exporter.Metric

	for _, inst := range instances {
		platformSourceID := fmt.Sprintf("%s/%s", inst.Project, inst.Name)
		inventoryID := shared.StableID(c.hostID, shared.KindLXDContainer, platformSourceID)

		// Resource info (§12.7).
		infoLabels := map[string]string{
			shared.LabelInventoryID:  inventoryID,
			shared.LabelHostID:       c.hostID,
			shared.LabelName:         inst.Name,
			shared.LabelKind:         shared.KindLXDContainer,
			shared.LabelDescription:  shared.CleanDescription(inst.Description),
			shared.LabelGuestOS:      inst.OSName,
			shared.LabelArchitecture: inst.Arch,
		}
		infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})

		// Resource IPs.
		for _, ip := range shared.SelectIPs(inst.Name, shared.FilterIPs(inst.IPs)) {
			ipMetrics = append(ipMetrics, exporter.Metric{
				Labels: map[string]string{
					shared.LabelInventoryID: inventoryID,
					shared.LabelAddress:     ip,
					shared.LabelFamily:      shared.IPFamily(ip),
				},
				Value: 1,
			})
		}

		// CPU: explicit limit or host-capacity fallback (§16.5).
		cpuSource := shared.CapacityHost
		cpuVal := int64(0) // will be filled by host collector data; for metrics we use what's available
		if inst.CPULimit != nil {
			cpuSource = shared.CapacityConfigured
			cpuVal = *inst.CPULimit
		}
		cpuMetrics = append(cpuMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelCapacitySource: cpuSource,
			},
			Value: float64(cpuVal),
		})

		// Memory: explicit limit or host-capacity fallback.
		memSource := shared.CapacityHost
		memVal := int64(0)
		if inst.MemLimit != nil {
			memSource = shared.CapacityConfigured
			memVal = *inst.MemLimit
		}
		memMetrics = append(memMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelCapacitySource: memSource,
			},
			Value: float64(memVal),
		})

		// Disk: explicit root-disk limit or storage-pool fallback.
		diskSource := shared.CapacityStoragePool
		diskVal := int64(0)
		if inst.RootDiskBytes != nil {
			diskSource = shared.CapacityConfigured
			diskVal = *inst.RootDiskBytes
		}
		diskMetrics = append(diskMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelDiskID:        inventoryID + "-root",
				shared.LabelDiskName:      "root",
				shared.LabelCapacitySource: diskSource,
				shared.LabelSourceName:    inst.BackingPool,
			},
			Value: float64(diskVal),
		})
	}

	return []exporter.MetricFamily{
		{Name: shared.MetricResourceInfo, Help: "Resource identity information.", Type: "gauge", Metrics: infoMetrics},
		{Name: shared.MetricResourceIPInfo, Help: "Resource IP addresses.", Type: "gauge", Metrics: ipMetrics},
		{Name: shared.MetricResourceCPUCount, Help: "Resource CPU count.", Type: "gauge", Metrics: cpuMetrics},
		{Name: shared.MetricResourceMemoryBytes, Help: "Resource memory bytes.", Type: "gauge", Metrics: memMetrics},
		{Name: shared.MetricResourceDiskBytes, Help: "Resource disk bytes.", Type: "gauge", Metrics: diskMetrics},
	}
}

func (c *LXDCollector) buildPoolMetrics(pools []LXDPool) []exporter.MetricFamily {
	var infoMetrics, totalMetrics, availMetrics []exporter.Metric

	for _, p := range pools {
		poolID := fmt.Sprintf("%s-%s", c.hostID, p.Name)
		poolType := lxdPoolType(p.Driver)

		infoLabels := map[string]string{
			shared.LabelHostID:   c.hostID,
			shared.LabelPoolID:   poolID,
			shared.LabelPoolName: p.Name,
			shared.LabelPoolType: poolType,
		}
		infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})

		sizeLabels := map[string]string{
			shared.LabelHostID: c.hostID,
			shared.LabelPoolID: poolID,
		}
		totalMetrics = append(totalMetrics, exporter.Metric{Labels: sizeLabels, Value: float64(p.TotalBytes)})
		if p.AvailBytes > 0 {
			availMetrics = append(availMetrics, exporter.Metric{Labels: sizeLabels, Value: float64(p.AvailBytes)})
		}
	}

	return []exporter.MetricFamily{
		{Name: shared.MetricHostStoragePoolInfo, Help: "Storage pool information.", Type: "gauge", Metrics: infoMetrics},
		{Name: shared.MetricHostStoragePoolTotalBytes, Help: "Storage pool total bytes.", Type: "gauge", Metrics: totalMetrics},
		{Name: shared.MetricHostStoragePoolAvailBytes, Help: "Storage pool available bytes.", Type: "gauge", Metrics: availMetrics},
	}
}

func lxdPoolType(driver string) string {
	switch driver {
	case "btrfs":
		return shared.PoolTypeLXDBtrfs
	case "lvm":
		return shared.PoolTypeLXDLVM
	case "zfs":
		return shared.PoolTypeLXDZFS
	case "dir":
		return shared.PoolTypeLXDDir
	default:
		return "lxd-" + driver
	}
}
