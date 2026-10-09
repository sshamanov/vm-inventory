package linux

import (
	"context"
	"testing"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

type mockLXDClient struct {
	instances []LXDInstance
	pools     []LXDPool
}

func (m *mockLXDClient) Connect() error                       { return nil }
func (m *mockLXDClient) Disconnect() error                     { return nil }
func (m *mockLXDClient) ListInstances() ([]LXDInstance, error) { return m.instances, nil }
func (m *mockLXDClient) ListStoragePools() ([]LXDPool, error) { return m.pools, nil }

func TestLXDCollectorName(t *testing.T) {
	c := NewLXDCollector("hv-01", &mockLXDClient{})
	if c.Name() != "lxd" {
		t.Errorf("Name = %q, want %q", c.Name(), "lxd")
	}
}

func TestLXDCollectorInstances(t *testing.T) {
	cpulimit := int64(4)
	memlimit := int64(2 * 1024 * 1024 * 1024) // 2 GiB

	mock := &mockLXDClient{
		instances: []LXDInstance{
			{
				Project:     "default",
				Name:        "webapp",
				Description: "Web application container",
				Arch:        "x86_64",
				OSName:      "Ubuntu 22.04",
				IPs:         []string{"10.0.0.50"},
				CPULimit:    &cpulimit,
				MemLimit:    &memlimit,
				BackingPool: "lxd-pool",
			},
			{
				Project:     "default",
				Name:        "unlimited",
				Description: "Container with no explicit limits",
				CPULimit:    nil, // falls back to host capacity
				MemLimit:    nil, // falls back to host capacity
				BackingPool: "lxd-pool",
			},
		},
	}

	c := NewLXDCollector("lxd-01", mock)
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	infoFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceInfo)
	if infoFamily == nil {
		t.Fatal("resource info not found")
	}
	if len(infoFamily.Metrics) != 2 {
		t.Errorf("expected 2 resource info metrics, got %d", len(infoFamily.Metrics))
	}

	// Verify capacity source with explicit limit.
	cpuFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceCPUCount)
	if cpuFamily == nil {
		t.Fatal("CPU count not found")
	}
	// First instance has configured CPU.
	if cpuFamily.Metrics[0].Labels[shared.LabelCapacitySource] != shared.CapacityConfigured {
		t.Errorf("CPU source = %q, want %q",
			cpuFamily.Metrics[0].Labels[shared.LabelCapacitySource], shared.CapacityConfigured)
	}
	// Second instance has host-capacity (no limit set).
	if cpuFamily.Metrics[1].Labels[shared.LabelCapacitySource] != shared.CapacityHost {
		t.Errorf("unlimited CPU source = %q, want %q",
			cpuFamily.Metrics[1].Labels[shared.LabelCapacitySource], shared.CapacityHost)
	}
}

// TestLXDCollectorIdentityPrefersInstanceUUID asserts the emitted inventory_id
// is built from volatile.uuid, so a renamed instance keeps its identity, and
// that instances without the key still fall back to project/name.
func TestLXDCollectorIdentityPrefersInstanceUUID(t *testing.T) {
	const uuid = "6b4b1c8e-0f52-4d5a-9a3e-2f3c9d1e7a55"
	mock := &mockLXDClient{
		instances: []LXDInstance{
			{Project: "default", Name: "build-03", InstanceUUID: uuid},
			{Project: "default", Name: "legacy"},
		},
	}

	result, err := NewLXDCollector("lxd-01", mock).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	infoFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceInfo)
	if infoFamily == nil {
		t.Fatal("resource info not found")
	}
	if len(infoFamily.Metrics) != 2 {
		t.Fatalf("expected 2 resource info metrics, got %d", len(infoFamily.Metrics))
	}

	want := shared.StableID("lxd-01", shared.KindLXDContainer, uuid)
	if got := infoFamily.Metrics[0].Labels[shared.LabelInventoryID]; got != want {
		t.Errorf("inventory_id = %q, want %q", got, want)
	}

	wantLegacy := shared.StableID("lxd-01", shared.KindLXDContainer, "default/legacy")
	if got := infoFamily.Metrics[1].Labels[shared.LabelInventoryID]; got != wantLegacy {
		t.Errorf("inventory_id = %q, want %q", got, wantLegacy)
	}
}

func TestLXDCollectorPools(t *testing.T) {
	mock := &mockLXDClient{
		pools: []LXDPool{
			{Name: "lxd-pool", Driver: "zfs", TotalBytes: 1000000000000, AvailBytes: 500000000000},
		},
	}

	c := NewLXDCollector("lxd-01", mock)
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	poolInfo := exporter.MetricByName(result.Metrics, shared.MetricHostStoragePoolInfo)
	if poolInfo == nil {
		t.Fatal("storage pool info not found")
	}
	if poolInfo.Metrics[0].Labels[shared.LabelPoolType] != shared.PoolTypeLXDZFS {
		t.Errorf("pool type = %q, want %q",
			poolInfo.Metrics[0].Labels[shared.LabelPoolType], shared.PoolTypeLXDZFS)
	}
}
