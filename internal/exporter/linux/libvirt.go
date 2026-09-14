package linux

import (
	"context"
	"fmt"
	"time"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// LibvirtCollector collects KVM/libvirt domains and storage pools (§10.2).
// Uses github.com/digitalocean/go-libvirt (pure Go, no CGO).
type LibvirtCollector struct {
	hostID string
	conn   LibvirtConnection
}

// LibvirtConnection abstracts go-libvirt operations for testability.
type LibvirtConnection interface {
	Connect() error
	Disconnect() error
	ListDomains(ctx context.Context) ([]LibvirtDomain, error)
	ListStoragePools(ctx context.Context) ([]LibvirtPool, error)
}

// LibvirtDomain represents a libvirt domain's collected data.
type LibvirtDomain struct {
	UUID        string
	Name        string
	Title       string
	Description string
	VCPUs       int
	MemoryBytes int64
	Disks       []LibvirtDisk
	IPs         []string
	GuestOS     string
	OSArch      string
}

// LibvirtDisk represents a domain's virtual disk.
type LibvirtDisk struct {
	Name     string
	SizeBytes int64
}

// LibvirtPool represents a libvirt storage pool.
type LibvirtPool struct {
	UUID        string
	Name        string
	PoolType    string
	TotalBytes  int64
	AvailBytes  int64
}

// NewLibvirtCollector creates a new libvirt collector.
// conn will be a go-libvirt connection in production, or a mock in tests.
func NewLibvirtCollector(hostID string, conn LibvirtConnection) *LibvirtCollector {
	return &LibvirtCollector{
		hostID: hostID,
		conn:   conn,
	}
}

// Name returns the collector name.
func (c *LibvirtCollector) Name() string { return "libvirt" }

// Collect gathers libvirt domain and pool data.
func (c *LibvirtCollector) Collect(ctx context.Context) (*exporter.CollectionResult, error) {
	now := time.Now()

	if err := c.conn.Connect(); err != nil {
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer c.conn.Disconnect()

	domains, err := c.conn.ListDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}

	pools, err := c.conn.ListStoragePools(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing storage pools: %w", err)
	}

	var families []exporter.MetricFamily

	// Resource metrics for VMs (§12.7).
	families = append(families, c.buildResourceMetrics(domains)...)

	// Storage pool metrics (§12.6).
	families = append(families, c.buildPoolMetrics(pools)...)

	return &exporter.CollectionResult{
		Metrics:    families,
		Timestamp:  now,
		SourceHost: c.hostID,
	}, nil
}

func (c *LibvirtCollector) buildResourceMetrics(domains []LibvirtDomain) []exporter.MetricFamily {
	var infoMetrics, ipMetrics, cpuMetrics, memMetrics, diskMetrics []exporter.Metric

	for _, d := range domains {
		inventoryID := shared.StableID(c.hostID, shared.KindLibvirtVM, d.UUID)

		// Resource info (§12.7).
		infoLabels := map[string]string{
			shared.LabelInventoryID:  inventoryID,
			shared.LabelHostID:       c.hostID,
			shared.LabelName:         d.Name,
			shared.LabelTitle:        shared.CleanDescription(d.Title),
			shared.LabelKind:         shared.KindLibvirtVM,
			shared.LabelDescription:  shared.CleanDescription(d.Description),
			shared.LabelGuestOS:      d.GuestOS,
			shared.LabelArchitecture: d.OSArch,
		}
		infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})

		// Resource IPs.
		for _, ip := range shared.SelectIPs(d.Name, shared.FilterIPs(d.IPs)) {
			ipMetrics = append(ipMetrics, exporter.Metric{
				Labels: map[string]string{
					shared.LabelInventoryID: inventoryID,
					shared.LabelAddress:     ip,
					shared.LabelFamily:      shared.IPFamily(ip),
				},
				Value: 1,
			})
		}

		// CPU count.
		cpuMetrics = append(cpuMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelCapacitySource: shared.CapacityConfigured,
			},
			Value: float64(d.VCPUs),
		})

		// Memory.
		memMetrics = append(memMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelCapacitySource: shared.CapacityConfigured,
			},
			Value: float64(d.MemoryBytes),
		})

		// Disks.
		for _, disk := range d.Disks {
			diskID := fmt.Sprintf("%s-%s", inventoryID, disk.Name)
			diskMetrics = append(diskMetrics, exporter.Metric{
				Labels: map[string]string{
					shared.LabelInventoryID:   inventoryID,
					shared.LabelDiskID:        diskID,
					shared.LabelDiskName:      disk.Name,
					shared.LabelCapacitySource: shared.CapacityConfigured,
				},
				Value: float64(disk.SizeBytes),
			})
		}
	}

	return []exporter.MetricFamily{
		{Name: shared.MetricResourceInfo, Help: "Resource identity information.", Type: "gauge", Metrics: infoMetrics},
		{Name: shared.MetricResourceIPInfo, Help: "Resource IP addresses.", Type: "gauge", Metrics: ipMetrics},
		{Name: shared.MetricResourceCPUCount, Help: "Resource CPU count.", Type: "gauge", Metrics: cpuMetrics},
		{Name: shared.MetricResourceMemoryBytes, Help: "Resource memory bytes.", Type: "gauge", Metrics: memMetrics},
		{Name: shared.MetricResourceDiskBytes, Help: "Resource disk bytes.", Type: "gauge", Metrics: diskMetrics},
	}
}

func (c *LibvirtCollector) buildPoolMetrics(pools []LibvirtPool) []exporter.MetricFamily {
	var infoMetrics, totalMetrics, availMetrics []exporter.Metric

	for _, p := range pools {
		poolID := fmt.Sprintf("%s-%s", c.hostID, p.UUID)
		poolType := poolTypeString(p.PoolType)

		infoLabels := map[string]string{
			shared.LabelHostID:  c.hostID,
			shared.LabelPoolID:  poolID,
			shared.LabelPoolName: p.Name,
			shared.LabelPoolType: poolType,
		}
		infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})

		sizeLabels := map[string]string{
			shared.LabelHostID: c.hostID,
			shared.LabelPoolID: poolID,
		}
		totalMetrics = append(totalMetrics, exporter.Metric{Labels: sizeLabels, Value: float64(p.TotalBytes)})
		availMetrics = append(availMetrics, exporter.Metric{Labels: sizeLabels, Value: float64(p.AvailBytes)})
	}

	return []exporter.MetricFamily{
		{Name: shared.MetricHostStoragePoolInfo, Help: "Storage pool information.", Type: "gauge", Metrics: infoMetrics},
		{Name: shared.MetricHostStoragePoolTotalBytes, Help: "Storage pool total bytes.", Type: "gauge", Metrics: totalMetrics},
		{Name: shared.MetricHostStoragePoolAvailBytes, Help: "Storage pool available bytes.", Type: "gauge", Metrics: availMetrics},
	}
}

// poolTypeString maps internal pool types to metric label values.
func poolTypeString(t string) string {
	switch t {
	case "logical":
		return shared.PoolTypeLibvirtLVM
	default:
		return "libvirt-" + t
	}
}
