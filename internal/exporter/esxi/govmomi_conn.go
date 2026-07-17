package esxi

import (
	"context"
	"fmt"
	"net/url"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/view"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
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
	}, &hw); err != nil {
		return ESXiHostHardware{}, err
	}

	s := hw.Summary
	hwInfo := ESXiHostHardware{
		CPUModel:         s.Hardware.CpuModel,
		CPUSockets:       int(s.Hardware.NumCpuPkgs),
		CPUCores:         int(s.Hardware.NumCpuCores),
		CPUThreads:       int(s.Hardware.NumCpuThreads),
		MemoryTotalBytes: int64(s.Hardware.MemorySize) << 20, // MB to bytes
	}

	cpuTotal := int64(s.Hardware.CpuMhz) * int64(s.Hardware.NumCpuCores)
	if cpuTotal > 0 {
		hwInfo.CPUUsageRatio = float64(s.QuickStats.OverallCpuUsage) / float64(cpuTotal)
	}
	if s.QuickStats.OverallMemoryUsage > 0 {
		hwInfo.MemoryAvailBytes = int64(s.Hardware.MemorySize)<<20 - int64(s.QuickStats.OverallMemoryUsage)<<20
	}

	// Collect host IPs.
	m := view.NewManager(c.client.Client)
	v, err := m.CreateContainerView(ctx, host.Reference(), []string{"ManagedEntity"}, true)
	if err == nil {
		defer v.Destroy(ctx)
		var hss []mo.HostSystem
		err = v.Retrieve(ctx, []string{"HostSystem"}, []string{"config.network.vnic"}, &hss)
		if err == nil {
			for _, hs := range hss {
				if hs.Config != nil && hs.Config.Network != nil {
					for _, vnic := range hs.Config.Network.Vnic {
						hwInfo.IPs = append(hwInfo.IPs, vnic.Spec.Ip.IpAddress)
					}
				}
			}
		}
	}

	return hwInfo, nil
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
		"config.annotation", "guest", "summary.config",
	}, &movms); err != nil {
		return nil, err
	}

	var result []ESXiVM
	for _, mvm := range movms {
		vm := ESXiVM{
			PlatformID:  mvm.Reference().Value,
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
					vm.Disks = append(vm.Disks, ESXiVMDisk{Name: name, SizeBytes: capacity})
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
