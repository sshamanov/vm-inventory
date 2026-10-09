package linux

import (
	"context"
	"testing"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/shared"
)

// mockLibvirtConn implements LibvirtConnection for testing.
type mockLibvirtConn struct {
	domains []LibvirtDomain
	pools   []LibvirtPool
}

func (m *mockLibvirtConn) Connect() error                                    { return nil }
func (m *mockLibvirtConn) Disconnect() error                                  { return nil }
func (m *mockLibvirtConn) ListDomains(ctx context.Context) ([]LibvirtDomain, error) { return m.domains, nil }
func (m *mockLibvirtConn) ListStoragePools(ctx context.Context) ([]LibvirtPool, error) { return m.pools, nil }

func TestLibvirtCollectorName(t *testing.T) {
	c := NewLibvirtCollector("hv-01", &mockLibvirtConn{})
	if c.Name() != "libvirt" {
		t.Errorf("Name = %q, want %q", c.Name(), "libvirt")
	}
}

func TestLibvirtCollectorDomains(t *testing.T) {
	mock := &mockLibvirtConn{
		domains: []LibvirtDomain{
			{
				UUID:        "550e8400-e29b-41d4-a716-446655440000",
				Name:        "web-server",
				Title:       "Web Frontend",
				Description: "Primary web server",
				VCPUs:       4,
				MemoryBytes: 8 * 1024 * 1024 * 1024, // 8 GiB
				Disks: []LibvirtDisk{
					{Name: "vda", SizeBytes: 50 * 1024 * 1024 * 1024}, // 50 GiB
				},
				IPs:     []string{"192.168.1.10"},
				GuestOS: "Ubuntu 22.04",
				OSArch:  "x86_64",
			},
			{
				UUID:        "660e8400-e29b-41d4-a716-446655440001",
				Name:        "db-server",
				Description: "", // empty description
				VCPUs:       2,
				MemoryBytes: 4 * 1024 * 1024 * 1024, // 4 GiB
				Disks:       []LibvirtDisk{},
				IPs:         []string{}, // no IPs available
				GuestOS:     "",          // unknown OS
				OSArch:      "",
			},
		},
	}

	c := NewLibvirtCollector("hv-kvm-01", mock)
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Verify resource info for both domains.
	infoFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceInfo)
	if infoFamily == nil {
		t.Fatal("resource info not found")
	}
	if len(infoFamily.Metrics) != 2 {
		t.Errorf("expected 2 resource info metrics, got %d", len(infoFamily.Metrics))
	}

	// Verify first domain.
	m1 := infoFamily.Metrics[0]
	if m1.Labels[shared.LabelName] != "web-server" {
		t.Errorf("domain name = %q", m1.Labels[shared.LabelName])
	}
	if m1.Labels[shared.LabelTitle] != "Web Frontend" {
		t.Errorf("domain title = %q", m1.Labels[shared.LabelTitle])
	}
	if m1.Labels[shared.LabelGuestOS] != "Ubuntu 22.04" {
		t.Errorf("guest OS = %q", m1.Labels[shared.LabelGuestOS])
	}
	// Second domain has no title — label must be present but empty (§12.7).
	if m2 := infoFamily.Metrics[1]; m2.Labels[shared.LabelTitle] != "" {
		t.Errorf("absent title should be empty, got %q", m2.Labels[shared.LabelTitle])
	}

	// Verify CPU count.
	cpuFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceCPUCount)
	if cpuFamily == nil {
		t.Fatal("resource CPU count not found")
	}
	if len(cpuFamily.Metrics) != 2 {
		t.Errorf("expected 2 CPU metrics, got %d", len(cpuFamily.Metrics))
	}

	// Verify memory.
	memFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceMemoryBytes)
	if memFamily == nil {
		t.Fatal("resource memory not found")
	}

	// Verify IPs.
	ipFamily := exporter.MetricByName(result.Metrics, shared.MetricResourceIPInfo)
	if ipFamily == nil {
		t.Fatal("resource IP info not found")
	}
	if len(ipFamily.Metrics) != 1 {
		t.Errorf("expected 1 IP metric, got %d", len(ipFamily.Metrics)) // second domain has no valid IPs
	}
}

func TestLibvirtCollectorPools(t *testing.T) {
	mock := &mockLibvirtConn{
		pools: []LibvirtPool{
			{
				UUID:       "pool-uuid-1",
				Name:       "vm-pool",
				PoolType:   "logical",
				TotalBytes: 500 * 1024 * 1024 * 1024, // 500 GiB
				AvailBytes: 200 * 1024 * 1024 * 1024, // 200 GiB
			},
			{
				UUID:       "pool-uuid-2",
				Name:       "default",
				PoolType:   "dir",
				TotalBytes: 100 * 1024 * 1024 * 1024, // 100 GiB
				AvailBytes: 50 * 1024 * 1024 * 1024,  // 50 GiB
			},
		},
	}

	c := NewLibvirtCollector("hv-kvm-01", mock)
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	poolInfo := exporter.MetricByName(result.Metrics, shared.MetricHostStoragePoolInfo)
	if poolInfo == nil {
		t.Fatal("storage pool info not found")
	}
	if len(poolInfo.Metrics) != 2 {
		t.Errorf("expected 2 pool info metrics, got %d", len(poolInfo.Metrics))
	}

	// Check that both pool types are present.
	types := map[string]bool{}
	for _, m := range poolInfo.Metrics {
		types[m.Labels[shared.LabelPoolType]] = true
	}
	if !types[shared.PoolTypeLibvirtLVM] {
		t.Error("logical pool type not found")
	}
	if !types["libvirt-dir"] {
		t.Error("dir pool type not found")
	}

	poolTotal := exporter.MetricByName(result.Metrics, shared.MetricHostStoragePoolTotalBytes)
	if poolTotal == nil {
		t.Fatal("pool total bytes not found")
	}
	if len(poolTotal.Metrics) != 2 {
		t.Errorf("expected 2 pool total metrics, got %d", len(poolTotal.Metrics))
	}
}

func TestLibvirtStableID(t *testing.T) {
	id := shared.StableID("hv-kvm-01", shared.KindLibvirtVM, "test-uuid-123")
	expected := "hv-kvm-01:libvirt_vm:test-uuid-123"
	if id != expected {
		t.Errorf("stable ID = %q, want %q", id, expected)
	}
}
