package esxi

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// ESXiCollector collects ESXi hosts, VMs, and datastores (§10.4).
// Uses github.com/vmware/govmomi.
type ESXiCollector struct {
	targets    []ESXITargetConfig
	clientFact ESXIClientFactory
}

// ESXITargetConfig holds per-target connection parameters (§8.2).
type ESXITargetConfig struct {
	HostID             string
	HostDescription    string
	Geo                string
	Address            string
	Username           string
	Password           string
	InsecureSkipVerify bool
}

// ESXIClientFactory creates ESXi API clients for testability.
type ESXIClientFactory interface {
	NewClient(ctx context.Context, target ESXITargetConfig) (ESXIClient, error)
}

// ESXIClient abstracts govmomi operations.
type ESXIClient interface {
	About(ctx context.Context) (ESXiHostInfo, error)
	HostSystem(ctx context.Context) (ESXiHostHardware, error)
	Datastores(ctx context.Context) ([]ESXiDatastore, error)
	VirtualMachines(ctx context.Context) ([]ESXiVM, error)
	Logout(ctx context.Context) error
}

// ESXiHostInfo holds ESXi product information.
type ESXiHostInfo struct {
	ProductName string
	Version     string
	Build       string
}

// ESXiHostHardware holds CPU and memory data.
type ESXiHostHardware struct {
	CPUModel       string
	CPUSockets     int
	CPUCores       int
	CPUThreads     int
	CPUUsageRatio  float64
	MemoryTotalBytes int64
	MemoryAvailBytes int64
	IPs            []string
}

// ESXiDatastore holds datastore data.
type ESXiDatastore struct {
	Name       string
	Type       string
	TotalBytes int64
	FreeBytes  int64
}

// ESXiVM holds virtual machine data.
type ESXiVM struct {
	PlatformID   string
	Name         string
	Description  string
	VCPUs        int
	MemoryBytes  int64
	Disks        []ESXiVMDisk
	IPs          []string
	GuestOS      string
	PowerState   string
}

// ESXiVMDisk holds VM disk data.
type ESXiVMDisk struct {
	Name      string
	SizeBytes int64
}

// NewESXiCollector creates a new ESXi collector.
func NewESXiCollector(targets []ESXITargetConfig, factory ESXIClientFactory) *ESXiCollector {
	return &ESXiCollector{
		targets:    targets,
		clientFact: factory,
	}
}

// Name returns the collector name.
func (c *ESXiCollector) Name() string { return "esxi" }

// Collect gathers data from all ESXi targets.
func (c *ESXiCollector) Collect(ctx context.Context) (*exporter.CollectionResult, error) {
	now := time.Now()
	var allFamilies []exporter.MetricFamily

	for _, target := range c.targets {
		client, err := c.clientFact.NewClient(ctx, target)
		if err != nil {
			slog.Warn("esxi target connection failed, skipping", "host_id", target.HostID, "address", target.Address, "error", err)
			continue
		}

		targetFamilies, err := c.collectTarget(ctx, client, target)
		client.Logout(ctx)
		if err != nil {
			slog.Warn("esxi target collection failed, skipping", "host_id", target.HostID, "error", err)
			continue
		}
		allFamilies = append(allFamilies, targetFamilies...)
	}

	return &exporter.CollectionResult{
		Metrics:    allFamilies,
		Timestamp:  now,
		SourceHost: "esxi-collector",
	}, nil
}

func (c *ESXiCollector) collectTarget(ctx context.Context, client ESXIClient, target ESXITargetConfig) ([]exporter.MetricFamily, error) {
	var families []exporter.MetricFamily

	// Host identity.
	info, err := client.About(ctx)
	if err != nil {
		return nil, fmt.Errorf("about: %w", err)
	}

	// Host hardware.
	hw, err := client.HostSystem(ctx)
	if err != nil {
		return nil, fmt.Errorf("host system: %w", err)
	}

	hostLabels := map[string]string{
		shared.LabelHostID:      target.HostID,
		shared.LabelDescription:  shared.CleanDescription(target.HostDescription),
		shared.LabelGeo:          target.Geo,
		shared.LabelPlatform:     shared.PlatformESXi,
		shared.LabelHostname:     target.Address,
		shared.LabelOSName:       info.ProductName,
		shared.LabelOSVersion:    fmt.Sprintf("%s build %s", info.Version, info.Build),
		shared.LabelKernel:       "ESXi",
		shared.LabelArchitecture: "x86_64",
	}

	families = append(families, exporter.MetricFamily{
		Name: shared.MetricHostInfo,
		Help: "Host identity information.",
		Type: "gauge",
		Metrics: []exporter.Metric{{Labels: hostLabels, Value: 1}},
	})

	// Host IPs.
	for _, ip := range shared.FilterIPs(hw.IPs) {
		families = append(families, exporter.MetricFamily{
			Name: shared.MetricHostIPInfo,
			Help: "Host IP addresses.",
			Type: "gauge",
			Metrics: []exporter.Metric{{
				Labels: map[string]string{
					shared.LabelHostID:  target.HostID,
					shared.LabelAddress: ip,
					shared.LabelFamily:  shared.IPFamily(ip),
				},
				Value: 1,
			}},
		})
	}

	// CPU.
	families = append(families, exporter.MetricFamily{
		Name: shared.MetricHostCPUInfo,
		Help: "CPU model information.",
		Type: "gauge",
		Metrics: []exporter.Metric{{
			Labels: map[string]string{
				shared.LabelHostID: target.HostID,
				shared.LabelModel:  hw.CPUModel,
			},
			Value: 1,
		}},
	})
	families = append(families,
		metricGauge(shared.MetricHostCPUSockets, target.HostID, float64(hw.CPUSockets)),
		metricGauge(shared.MetricHostCPUCores, target.HostID, float64(hw.CPUCores)),
		metricGauge(shared.MetricHostCPUThreads, target.HostID, float64(hw.CPUThreads)),
		metricGauge(shared.MetricHostCPUUsageRatio, target.HostID, hw.CPUUsageRatio),
		metricGauge(shared.MetricHostMemoryTotalBytes, target.HostID, float64(hw.MemoryTotalBytes)),
		metricGauge(shared.MetricHostMemoryAvailableBytes, target.HostID, float64(hw.MemoryAvailBytes)),
	)

	// Datastores.
	datastores, err := client.Datastores(ctx)
	if err == nil {
		families = append(families, c.buildDatastoreMetrics(target.HostID, datastores)...)
	}

	// VMs.
	vms, err := client.VirtualMachines(ctx)
	if err == nil {
		families = append(families, c.buildVMMetrics(target.HostID, vms)...)
	}

	return families, nil
}

func (c *ESXiCollector) buildVMMetrics(hostID string, vms []ESXiVM) []exporter.MetricFamily {
	var infoMetrics, ipMetrics, cpuMetrics, memMetrics, diskMetrics []exporter.Metric

	for _, vm := range vms {
		if vm.PowerState != "poweredOn" {
			continue // powered-on VMs only (§10.4)
		}

		inventoryID := shared.StableID(hostID, shared.KindEsxiVM, vm.PlatformID)

		infoLabels := map[string]string{
			shared.LabelInventoryID:  inventoryID,
			shared.LabelHostID:       hostID,
			shared.LabelName:         vm.Name,
			shared.LabelKind:         shared.KindEsxiVM,
			shared.LabelDescription:  shared.CleanDescription(vm.Description),
			shared.LabelGuestOS:      vm.GuestOS,
		}
		infoMetrics = append(infoMetrics, exporter.Metric{Labels: infoLabels, Value: 1})

		for _, ip := range shared.FilterIPs(vm.IPs) {
			ipMetrics = append(ipMetrics, exporter.Metric{
				Labels: map[string]string{
					shared.LabelInventoryID: inventoryID,
					shared.LabelAddress:     ip,
					shared.LabelFamily:      shared.IPFamily(ip),
				},
				Value: 1,
			})
		}

		cpuMetrics = append(cpuMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelCapacitySource: shared.CapacityConfigured,
			},
			Value: float64(vm.VCPUs),
		})

		memMetrics = append(memMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelInventoryID:   inventoryID,
				shared.LabelCapacitySource: shared.CapacityConfigured,
			},
			Value: float64(vm.MemoryBytes),
		})

		for _, disk := range vm.Disks {
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

func (c *ESXiCollector) buildDatastoreMetrics(hostID string, datastores []ESXiDatastore) []exporter.MetricFamily {
	var infoMetrics, totalMetrics, availMetrics []exporter.Metric

	for _, ds := range datastores {
		poolID := fmt.Sprintf("%s-%s", hostID, ds.Name)

		infoMetrics = append(infoMetrics, exporter.Metric{
			Labels: map[string]string{
				shared.LabelHostID:   hostID,
				shared.LabelPoolID:   poolID,
				shared.LabelPoolName: ds.Name,
				shared.LabelPoolType: shared.PoolTypeESXiDS,
			},
			Value: 1,
		})

		sizeLabels := map[string]string{
			shared.LabelHostID: hostID,
			shared.LabelPoolID: poolID,
		}
		totalMetrics = append(totalMetrics, exporter.Metric{Labels: sizeLabels, Value: float64(ds.TotalBytes)})
		availMetrics = append(availMetrics, exporter.Metric{Labels: sizeLabels, Value: float64(ds.FreeBytes)})
	}

	return []exporter.MetricFamily{
		{Name: shared.MetricHostStoragePoolInfo, Help: "Storage pool information.", Type: "gauge", Metrics: infoMetrics},
		{Name: shared.MetricHostStoragePoolTotalBytes, Help: "Storage pool total bytes.", Type: "gauge", Metrics: totalMetrics},
		{Name: shared.MetricHostStoragePoolAvailBytes, Help: "Storage pool available bytes.", Type: "gauge", Metrics: availMetrics},
	}
}

func metricGauge(name, hostID string, value float64) exporter.MetricFamily {
	return exporter.MetricFamily{
		Name: name,
		Help: name,
		Type: "gauge",
		Metrics: []exporter.Metric{{
			Labels: map[string]string{shared.LabelHostID: hostID},
			Value:  value,
		}},
	}
}
