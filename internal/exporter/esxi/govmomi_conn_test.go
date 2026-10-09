package esxi

import (
	"testing"

	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// managedEntity builds the embedded object that carries the reference. Self
// lives on ExtensibleManagedObject, and promoted fields cannot be set directly
// in a struct literal, so each embedding level must be spelled out.
func managedEntity(ref string) mo.ManagedEntity {
	return mo.ManagedEntity{
		ExtensibleManagedObject: mo.ExtensibleManagedObject{
			Self: types.ManagedObjectReference{Type: "VirtualMachine", Value: ref},
		},
	}
}

// TestESXiPlatformIDPrefersBIOSUUID pins the identity rule from §11.4: the BIOS
// UUID is the identity, and the ManagedObjectReference is only a fallback. A
// missing BIOS UUID must not yield an empty source ID, which would be identical
// for every such VM and collapse them into one inventory record.
func TestESXiPlatformIDPrefersBIOSUUID(t *testing.T) {
	const ref = "vm-8"
	const biosUUID = "564d1a2b-3c4d-5e6f-7a8b-9c0d1e2f3a4b"

	tests := []struct {
		name string
		vm   mo.VirtualMachine
		want string
	}{
		{
			name: "bios uuid present",
			vm: mo.VirtualMachine{
				ManagedEntity: managedEntity(ref),
				Config:                  &types.VirtualMachineConfigInfo{Uuid: biosUUID},
			},
			want: biosUUID,
		},
		{
			name: "config absent falls back to reference",
			vm:   mo.VirtualMachine{ManagedEntity: managedEntity(ref)},
			want: ref,
		},
		{
			name: "empty bios uuid falls back to reference",
			vm: mo.VirtualMachine{
				ManagedEntity: managedEntity(ref),
				Config:                  &types.VirtualMachineConfigInfo{},
			},
			want: ref,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := esxiPlatformID(tt.vm); got != tt.want {
				t.Errorf("esxiPlatformID = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestHostDisks covers the LUN list a host hands back: it carries cdroms,
// enclosures and an unsized virtual media device alongside disks, disk sizes
// arrive as block counts rather than bytes, and a LUN with no canonical name
// cannot be identified across polls.
func TestHostDisks(t *testing.T) {
	disk := func(canonical, device, model string, blockSize int32, block int64) types.BaseScsiLun {
		return &types.HostScsiDisk{
			ScsiLun: types.ScsiLun{
				HostDevice: types.HostDevice{DeviceName: device, DeviceType: "disk"},
				CanonicalName: canonical,
				Model:         model,
			},
			Capacity: types.HostDiskDimensionsLba{BlockSize: blockSize, Block: block},
		}
	}

	tests := []struct {
		name string
		luns []types.BaseScsiLun
		want []ESXiHostDisk
	}{
		{
			name: "no storage device",
			luns: nil,
			want: nil,
		},
		{
			name: "disk size comes from block size times block count",
			luns: []types.BaseScsiLun{disk("naa.6000c29b", "Local NVMe Disk (mpx.vmhba1:C0:T0:L0)", "SAMSUNG MZQL2960HCJR", 512, 2000398934)},
			want: []ESXiHostDisk{{
				ID:        "naa.6000c29b",
				Name:      "Local NVMe Disk (mpx.vmhba1:C0:T0:L0)",
				Model:     "SAMSUNG MZQL2960HCJR",
				SizeBytes: 1024204254208,
			}},
		},
		{
			name: "cdrom on the same bus is not a disk",
			luns: []types.BaseScsiLun{
				&types.ScsiLun{CanonicalName: "mpx.vmhba32:C0:T0:L0", Vendor: "NECVMWar", Model: "VMware IDE CDR10"},
				disk("naa.6000c29b", "Local VMware Disk (mpx.vmhba0:C0:T0:L0)", "Virtual disk", 512, 1954109440),
			},
			want: []ESXiHostDisk{{
				ID:        "naa.6000c29b",
				Name:      "Local VMware Disk (mpx.vmhba0:C0:T0:L0)",
				Model:     "Virtual disk",
				SizeBytes: 1000504033280,
			}},
		},
		{
			name: "lun without a canonical name is dropped",
			luns: []types.BaseScsiLun{disk("", "Local VMware Disk", "Virtual disk", 512, 100)},
			want: nil,
		},
		{
			name: "unsized virtual media device is not a disk",
			luns: []types.BaseScsiLun{
				disk("mpx.vmhba32:C0:T0:L1", "/vmfs/devices/disks/mpx.vmhba32:C0:T0:L1", "Mass Storage Fun", 512, 0),
				disk("naa.6000c29b", "/vmfs/devices/disks/naa.6000c29b", "PERC H730 Mini  ", 512, 2000398934),
			},
			want: []ESXiHostDisk{{
				ID:        "naa.6000c29b",
				Name:      "/vmfs/devices/disks/naa.6000c29b",
				Model:     "PERC H730 Mini",
				SizeBytes: 1024204254208,
			}},
		},
		{
			name: "duplicate canonical name is counted once",
			luns: []types.BaseScsiLun{
				disk("naa.6000c29b", "Local VMware Disk (mpx.vmhba0:C0:T0:L0)", "Virtual disk", 512, 100),
				disk("naa.6000c29b", "Local VMware Disk (mpx.vmhba1:C0:T0:L0)", "Virtual disk", 512, 100),
			},
			want: []ESXiHostDisk{{
				ID:        "naa.6000c29b",
				Name:      "Local VMware Disk (mpx.vmhba0:C0:T0:L0)",
				Model:     "Virtual disk",
				SizeBytes: 51200,
			}},
		},
		{
			name: "missing device name falls back to the canonical name",
			luns: []types.BaseScsiLun{disk("naa.6000c29b", "", "Virtual disk", 512, 100)},
			want: []ESXiHostDisk{{
				ID:        "naa.6000c29b",
				Name:      "naa.6000c29b",
				Model:     "Virtual disk",
				SizeBytes: 51200,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hw := mo.HostSystem{}
			if tt.luns != nil {
				hw.Config = &types.HostConfigInfo{
					StorageDevice: &types.HostStorageDeviceInfo{ScsiLun: tt.luns},
				}
			}
			got := hostDisks(hw)
			if len(got) != len(tt.want) {
				t.Fatalf("hostDisks returned %d disks, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("disk %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
