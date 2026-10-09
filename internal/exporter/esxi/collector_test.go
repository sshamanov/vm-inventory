package esxi

import (
	"context"
	"fmt"
	"testing"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

type mockESXIFactory struct{}

func (m mockESXIFactory) NewClient(ctx context.Context, target ESXITargetConfig) (ESXIClient, error) {
	return &mockESXIClient{}, nil
}

type mockESXIClient struct{}

func (m *mockESXIClient) About(ctx context.Context) (ESXiHostInfo, error) {
	return ESXiHostInfo{
		ProductName: "VMware ESXi",
		Version:     "8.0.2",
		Build:       "23305546",
	}, nil
}

func (m *mockESXIClient) HostSystem(ctx context.Context) (ESXiHostHardware, error) {
	return ESXiHostHardware{
		CPUModel:        "Intel Xeon Gold 6248R",
		CPUSockets:      2,
		CPUCores:        48,
		CPUThreads:      96,
		CPUUsageRatio:   0.35,
		MemoryTotalBytes: 384 * 1024 * 1024 * 1024, // 384 GiB
		MemoryAvailBytes: 100 * 1024 * 1024 * 1024,  // 100 GiB
		IPs:              []string{"10.0.0.100", "fe80::1"},
		Disks: []ESXiHostDisk{
			{ID: "naa.6000c29b", Name: "Local NVMe Disk (mpx.vmhba1:C0:T0:L0)", Model: "SAMSUNG MZQL2960HCJR", SizeBytes: 1024204254208},
			{ID: "naa.6000c29c", Name: "Local NVMe Disk (mpx.vmhba1:C0:T1:L0)", Model: "SAMSUNG MZQL2960HCJR", SizeBytes: 1024204254208},
		},
	}, nil
}

func (m *mockESXIClient) Datastores(ctx context.Context) ([]ESXiDatastore, error) {
	return []ESXiDatastore{
		{Name: "datastore1", Type: "VMFS", TotalBytes: 2000000000000, FreeBytes: 800000000000},
		{Name: "images", Type: "NFS", TotalBytes: 0, FreeBytes: 0}, // should be filtered out
	}, nil
}

func (m *mockESXIClient) VirtualMachines(ctx context.Context) ([]ESXiVM, error) {
	return []ESXiVM{
		{
			PlatformID:  "vm-42",
			Name:        "web-vm",
			Description: "Web server VM",
			VCPUs:       8,
			MemoryBytes: 32 * 1024 * 1024 * 1024, // 32 GiB
			Disks:       []ESXiVMDisk{{Name: "Hard disk 1", SizeBytes: 200 * 1024 * 1024 * 1024, DatastoreName: "datastore1"}},
			IPs:         []string{"10.0.0.200"},
			GuestOS:     "Ubuntu Linux (64-bit)",
			PowerState:  "poweredOn",
		},
		{
			PlatformID:  "vm-99",
			Name:        "stopped-vm",
			Description: "This VM is powered off",
			PowerState:  "poweredOff",
			// should be excluded
		},
	}, nil
}

func (m *mockESXIClient) Logout(ctx context.Context) error { return nil }

// TestBuildVMMetricsCapsIPs covers a guest that holds whole address blocks:
// the emitted address series must stay within the bound while retaining the
// management address embedded in the VM name.
func TestBuildVMMetricsCapsIPs(t *testing.T) {
	const mgmt = "203.0.113.5"
	var ips []string
	for i := 1; i <= 40; i++ {
		ips = append(ips, fmt.Sprintf("198.51.100.%d", i))
	}
	ips = append(ips, mgmt)

	c := NewESXiCollector(nil, nil)
	fams := c.buildVMMetrics("esxi-03", []ESXiVM{{
		PlatformID: "vm-8",
		Name:       "loadgen-01_" + mgmt,
		PowerState: "poweredOn",
		IPs:        ips,
	}})

	ipFam := exporter.MetricByName(fams, shared.MetricResourceIPInfo)
	if ipFam == nil {
		t.Fatal("resource IP info not found")
	}
	if len(ipFam.Metrics) != shared.MaxResourceIPs {
		t.Errorf("emitted %d IP series, want %d", len(ipFam.Metrics), shared.MaxResourceIPs)
	}
	if got := ipFam.Metrics[0].Labels[shared.LabelAddress]; got != mgmt {
		t.Errorf("first emitted address = %q, want management address %q", got, mgmt)
	}
}

func TestESXiCollectorName(t *testing.T) {
	c := NewESXiCollector(nil, mockESXIFactory{})
	if c.Name() != "esxi" {
		t.Errorf("Name = %q, want %q", c.Name(), "esxi")
	}
}

func TestESXiCollectorHost(t *testing.T) {
	targets := []ESXITargetConfig{{
		HostID:          "esxi-01",
		HostDescription: "Primary ESXi",
		Geo:             "belgrade",
		Address:         "https://esxi-01.internal",
	}}

	c := NewESXiCollector(targets, mockESXIFactory{})
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Verify host info.
	infoFamily := exporter.MetricByName(result.Metrics, shared.MetricHostInfo)
	if infoFamily == nil {
		t.Fatal("host info not found")
	}
	if len(infoFamily.Metrics) != 1 {
		t.Errorf("expected 1 host info, got %d", len(infoFamily.Metrics))
	}

	// Verify IP filtering (link-local fe80::1 should be excluded).
	ipCount := 0
	for _, mf := range result.Metrics {
		if mf.Name == shared.MetricHostIPInfo {
			ipCount++
		}
	}
	if ipCount != 1 {
		t.Errorf("expected 1 IP metric (link-local excluded), got %d", ipCount)
	}

	// Verify powered-off VM excluded.
	resInfo := exporter.MetricByName(result.Metrics, shared.MetricResourceInfo)
	if resInfo == nil {
		t.Fatal("resource info not found")
	}
	if len(resInfo.Metrics) != 1 {
		t.Errorf("expected 1 VM (powered-off excluded), got %d", len(resInfo.Metrics))
	}

	// Verify datastore.
	poolInfo := exporter.MetricByName(result.Metrics, shared.MetricHostStoragePoolInfo)
	if poolInfo == nil {
		t.Fatal("pool info not found")
	}

	// Verify host disks reach the exporter under the same metric names the Linux
	// collector uses, keyed by canonical name.
	diskInfo := exporter.MetricByName(result.Metrics, shared.MetricHostBlockDeviceInfo)
	if diskInfo == nil {
		t.Fatal("host block device info not found")
	}
	if len(diskInfo.Metrics) != 2 {
		t.Errorf("expected 2 host disks, got %d", len(diskInfo.Metrics))
	}
	if got := diskInfo.Metrics[0].Labels[shared.LabelDeviceID]; got != "naa.6000c29b" {
		t.Errorf("disk device_id = %q, want %q", got, "naa.6000c29b")
	}
	if got := diskInfo.Metrics[0].Labels[shared.LabelModel]; got != "SAMSUNG MZQL2960HCJR" {
		t.Errorf("disk model = %q, want %q", got, "SAMSUNG MZQL2960HCJR")
	}

	diskSize := exporter.MetricByName(result.Metrics, shared.MetricHostBlockDeviceBytes)
	if diskSize == nil {
		t.Fatal("host block device bytes not found")
	}
	if len(diskSize.Metrics) != 2 {
		t.Fatalf("expected 2 host disk sizes, got %d", len(diskSize.Metrics))
	}
	if got := diskSize.Metrics[0].Value; got != 1024204254208 {
		t.Errorf("disk size = %v, want %v", got, 1024204254208)
	}
}
