package esxi

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	"vm-inventory/internal/shared"
)

// govmomiFactory creates real ESXi API clients via govmomi.
type govmomiFactory struct{}

// NewGovmomiFactory returns a real ESXi client factory.
func NewGovmomiFactory() ESXIClientFactory {
	return &govmomiFactory{}
}

func (f *govmomiFactory) NewClient(ctx context.Context, target ESXITargetConfig) (ESXIClient, error) {
	u, err := url.Parse(target.Address)
	if err != nil {
		return nil, fmt.Errorf("parsing ESXi URL: %w", err)
	}
	if u.Path == "" {
		u.Path = "/sdk"
	}
	u.User = url.UserPassword(target.Username, target.Password)

	vimClient, err := govmomi.NewClient(ctx, u, target.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("connecting to ESXi: %w", err)
	}

	return &govmomiClient{client: vimClient, target: target}, nil
}

type govmomiClient struct {
	client *govmomi.Client
	target ESXITargetConfig
}

func (c *govmomiClient) About(ctx context.Context) (ESXiHostInfo, error) {
	about := c.client.ServiceContent.About
	return ESXiHostInfo{
		ProductName: about.FullName,
		Version:     about.Version,
		Build:       about.Build,
	}, nil
}

func (c *govmomiClient) HostSystem(ctx context.Context) (ESXiHostHardware, error) {
	finder := find.NewFinder(c.client.Client, false)
	dcs, err := finder.DatacenterList(ctx, "*")
	if err != nil || len(dcs) == 0 {
		return ESXiHostHardware{}, fmt.Errorf("no datacenter found")
	}
	finder.SetDatacenter(dcs[0])

	hosts, err := finder.HostSystemList(ctx, "*")
	if err != nil || len(hosts) == 0 {
		return ESXiHostHardware{}, fmt.Errorf("no host found")
	}
	host := hosts[0]

	var hw mo.HostSystem
	if err := host.Properties(ctx, host.Reference(), []string{
		"summary.hardware", "summary.quickStats", "config.network",
		"config.storageDevice.scsiLun",
	}, &hw); err != nil {
		return ESXiHostHardware{}, err
	}

	s := hw.Summary
	hwInfo := ESXiHostHardware{
		CPUModel:         s.Hardware.CpuModel,
		CPUSockets:       int(s.Hardware.NumCpuPkgs),
		CPUCores:         int(s.Hardware.NumCpuCores),
		CPUThreads:       int(s.Hardware.NumCpuThreads),
		MemoryTotalBytes: int64(s.Hardware.MemorySize), // bytes (API returns bytes since vSphere 6.0)
	}

	cpuTotal := int64(s.Hardware.CpuMhz) * int64(s.Hardware.NumCpuCores)
	if cpuTotal > 0 {
		hwInfo.CPUUsageRatio = float64(s.QuickStats.OverallCpuUsage) / float64(cpuTotal)
	}
	if s.QuickStats.OverallMemoryUsage > 0 {
		// MemorySize is bytes, OverallMemoryUsage is MB (§10.4).
		hwInfo.MemoryAvailBytes = int64(s.Hardware.MemorySize) - int64(s.QuickStats.OverallMemoryUsage)<<20
	}

	// Collect host IPs from vmkernel interfaces (already retrieved via config.network).
	if hw.Config != nil && hw.Config.Network != nil {
		for _, vnic := range hw.Config.Network.Vnic {
			if vnic.Spec.Ip != nil {
				hwInfo.IPs = append(hwInfo.IPs, vnic.Spec.Ip.IpAddress)
			}
		}
	}

	hwInfo.Disks = hostDisks(hw)

	return hwInfo, nil
}

// hostDisks maps the host's SCSI LUN list to disks. The list holds everything
// on the SCSI bus, so the concrete HostScsiDisk type is what separates a disk
// from a cdrom, a tape or an enclosure. A disk reached over several paths
// appears once, under one canonical name, but the guard keeps the metric from
// double counting a LUN if the host reports it twice.
func hostDisks(hw mo.HostSystem) []ESXiHostDisk {
	if hw.Config == nil || hw.Config.StorageDevice == nil {
		return nil
	}

	var disks []ESXiHostDisk
	seen := make(map[string]struct{})
	for _, lun := range hw.Config.StorageDevice.ScsiLun {
		disk, ok := lun.(*types.HostScsiDisk)
		if !ok || disk.CanonicalName == "" {
			continue
		}
		// A LUN the host cannot size is not a disk. The virtual media device a
		// management controller exposes on the SCSI bus is typed as a disk and
		// reports zero blocks; on the card it would read as a disk of no bytes.
		sizeBytes := int64(disk.Capacity.BlockSize) * disk.Capacity.Block
		if sizeBytes == 0 {
			continue
		}
		if _, dup := seen[disk.CanonicalName]; dup {
			continue
		}
		seen[disk.CanonicalName] = struct{}{}

		// DeviceName is the host's own label for the device and may be absent;
		// the canonical name is a poor name to read but never missing. Models
		// arrive space padded ("PERC H730 Mini  ").
		name := shared.CleanDescription(disk.DeviceName)
		if name == "" {
			name = disk.CanonicalName
		}

		disks = append(disks, ESXiHostDisk{
			ID:        disk.CanonicalName,
			Name:      name,
			Model:     shared.CleanDescription(disk.Model),
			SizeBytes: sizeBytes,
		})
	}
	return disks
}

func (c *govmomiClient) Datastores(ctx context.Context) ([]ESXiDatastore, error) {
	finder := find.NewFinder(c.client.Client, false)
	dcs, err := finder.DatacenterList(ctx, "*")
	if err != nil || len(dcs) == 0 {
		return nil, fmt.Errorf("no datacenter found")
	}
	finder.SetDatacenter(dcs[0])

	dss, err := finder.DatastoreList(ctx, "*")
	if err != nil {
		return nil, err
	}

	var datastores []ESXiDatastore
	for _, ds := range dss {
		var info mo.Datastore
		if err := ds.Properties(ctx, ds.Reference(), []string{"summary"}, &info); err != nil {
			continue
		}
		s := info.Summary
			// Only collect local VMFS datastores; skip NFS, vSAN, VVol, etc.
			if s.Type != "VMFS" {
				continue
			}
		datastores = append(datastores, ESXiDatastore{
			Name:       s.Name,
			Type:       s.Type,
			TotalBytes: s.Capacity,
			FreeBytes:  s.FreeSpace,
		})
	}
	return datastores, nil
}

func (c *govmomiClient) VirtualMachines(ctx context.Context) ([]ESXiVM, error) {
	finder := find.NewFinder(c.client.Client, false)
	dcs, err := finder.DatacenterList(ctx, "*")
	if err != nil || len(dcs) == 0 {
		return nil, fmt.Errorf("no datacenter found")
	}
	finder.SetDatacenter(dcs[0])

	vms, err := finder.VirtualMachineList(ctx, "*")
	if err != nil {
		return nil, err
	}

	pc := property.DefaultCollector(c.client.Client)
	var movms []mo.VirtualMachine
	refs := make([]types.ManagedObjectReference, len(vms))
	for i, vm := range vms {
		refs[i] = vm.Reference()
	}
	if err := pc.Retrieve(ctx, refs, []string{
		"name", "runtime.powerState", "config.hardware",
		"config.annotation", "config.uuid", "guest", "summary.config",
	}, &movms); err != nil {
		return nil, err
	}

	var result []ESXiVM
	for _, mvm := range movms {
		vm := ESXiVM{
			PlatformID:  esxiPlatformID(mvm),
			Name:        mvm.Name,
			PowerState:  string(mvm.Runtime.PowerState),
			Description: mvm.Config.Annotation,
		}

		if mvm.Config != nil && mvm.Config.Hardware.NumCPU > 0 {
			vm.VCPUs = int(mvm.Config.Hardware.NumCPU)
			vm.MemoryBytes = int64(mvm.Config.Hardware.MemoryMB) << 20
		}

		// Guest OS.
		if mvm.Guest != nil {
			vm.GuestOS = mvm.Guest.GuestFullName
			for _, nic := range mvm.Guest.Net {
				for _, ip := range nic.IpAddress {
					vm.IPs = append(vm.IPs, ip)
				}
			}
		}
		if vm.GuestOS == "" && mvm.Summary.Config.GuestFullName != "" {
			vm.GuestOS = mvm.Summary.Config.GuestFullName
		}

		// Disks.
		if mvm.Config != nil {
			for _, dev := range mvm.Config.Hardware.Device {
				if disk, ok := dev.(*types.VirtualDisk); ok {
					capacity := disk.CapacityInBytes
					name := disk.DeviceInfo.GetDescription().Label
					dsName := extractDatastoreName(disk)
					vm.Disks = append(vm.Disks, ESXiVMDisk{Name: name, SizeBytes: capacity, DatastoreName: dsName})
				}
			}
		}

		result = append(result, vm)
	}
	return result, nil
}

func (c *govmomiClient) Logout(ctx context.Context) error {
	return c.client.Logout(ctx)
}

// esxiPlatformID returns a VM's durable identity (§11.4): the BIOS UUID stored
// in the VMX as uuid.bios, which survives rename, reboot and re-registration.
// The ManagedObjectReference is only a fallback. It is allocated from a small
// sequential pool and released on delete, so a newly created VM can inherit a
// retired VM's reference — and with it its inventory_id, silently collapsing two
// different machines into one record. A missing BIOS UUID is still worse to
// leave empty than to fill with the reference: an empty source ID is identical
// for every such VM, which collides outright.
func esxiPlatformID(mvm mo.VirtualMachine) string {
	if mvm.Config != nil && mvm.Config.Uuid != "" {
		return mvm.Config.Uuid
	}
	return mvm.Reference().Value
}

// extractDatastoreName parses the datastore name from a VirtualDisk backing filename.
// Backing filenames are formatted as "[datastore1] path/to/disk.vmdk".
func extractDatastoreName(disk *types.VirtualDisk) string {
	if disk.VirtualDevice.Backing == nil {
		return ""
	}
	// Try the most common backing type first.
	if backing, ok := disk.VirtualDevice.Backing.(*types.VirtualDiskFlatVer2BackingInfo); ok {
		return parseDatastoreName(backing.FileName)
	}
	// Fall back to generic file backing info.
	if backing, ok := disk.VirtualDevice.Backing.(*types.VirtualDeviceFileBackingInfo); ok {
		return parseDatastoreName(backing.FileName)
	}
	return ""
}

// parseDatastoreName extracts "datastore1" from "[datastore1] path/to/disk.vmdk".
func parseDatastoreName(fileName string) string {
	closeBracket := strings.IndexByte(fileName, ']')
	if closeBracket < 0 {
		return ""
	}
	if strings.HasPrefix(fileName, "[") && closeBracket > 1 {
		return fileName[1:closeBracket]
	}
	return ""
}
