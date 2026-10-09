package shared

import "testing"

func TestStableID(t *testing.T) {
	tests := []struct {
		name             string
		hostID           string
		resourceKind     string
		platformSourceID string
		want             string
	}{
		{
			"libvirt VM",
			"hv-kvm-01",
			KindLibvirtVM,
			"550e8400-e29b-41d4-a716-446655440000",
			"hv-kvm-01:libvirt_vm:550e8400-e29b-41d4-a716-446655440000",
		},
		{
			"ESXi VM",
			"esxi-01",
			KindEsxiVM,
			"vm-42",
			"esxi-01:esxi_vm:vm-42",
		},
		{
			"LXD container",
			"lxd-01",
			KindLXDContainer,
			"default/webapp",
			"lxd-01:lxd_container:default/webapp",
		},
		{
			"empty source ID",
			"host-01",
			KindLibvirtVM,
			"",
			"host-01:libvirt_vm:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StableID(tt.hostID, tt.resourceKind, tt.platformSourceID)
			if got != tt.want {
				t.Errorf("StableID(%q, %q, %q) = %q, want %q",
					tt.hostID, tt.resourceKind, tt.platformSourceID, got, tt.want)
			}
		})
	}
}

func TestHostStableID(t *testing.T) {
	if got := HostStableID("hv-kvm-01"); got != "hv-kvm-01" {
		t.Errorf("HostStableID = %q, want %q", got, "hv-kvm-01")
	}
}
