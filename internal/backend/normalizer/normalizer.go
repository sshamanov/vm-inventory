package normalizer

import (
	"fmt"
	"sort"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

// Normalizer builds the normalized inventory snapshot from the observation index.
type Normalizer struct {
	idx *index.ObservationIndex
}

// New creates a new Normalizer.
func New(idx *index.ObservationIndex) *Normalizer {
	return &Normalizer{idx: idx}
}

// BuildUISnapshot builds the 1-hour UI snapshot (§15.1, §17.2).
func (n *Normalizer) BuildUISnapshot() *shared.NormalizedInventory {
	now := time.Now()

	hostsByGeo := n.idx.GetHostsByGeo(now, shared.UILivenessWindow)
	resByHost := n.idx.GetResourcesByHost(now, shared.UILivenessWindow)

	geos := n.buildGeos(hostsByGeo, resByHost, now, shared.UILivenessWindow)

	// Sort geos case-insensitively.
	sort.Slice(geos, func(i, j int) bool {
		return sortStringsInsensitive(geos[i].Name, geos[j].Name)
	})

	return &shared.NormalizedInventory{
		SchemaVersion: 1,
		GeneratedAt:   now,
		Geos:          geos,
	}
}

// BuildConfluenceSnapshot builds the 8-hour Confluence snapshot (§15.2).
func (n *Normalizer) BuildConfluenceSnapshot() *shared.NormalizedInventory {
	now := time.Now()

	hostsByGeo := n.idx.GetHostsByGeo(now, shared.ConfluenceLivenessWindow)
	resByHost := n.idx.GetResourcesByHost(now, shared.ConfluenceLivenessWindow)

	geos := n.buildGeos(hostsByGeo, resByHost, now, shared.ConfluenceLivenessWindow)
	sort.Slice(geos, func(i, j int) bool {
		return sortStringsInsensitive(geos[i].Name, geos[j].Name)
	})

	return &shared.NormalizedInventory{
		SchemaVersion: 1,
		GeneratedAt:   now,
		Geos:          geos,
	}
}

func (n *Normalizer) buildGeos(
	hostsByGeo map[string][]*index.HostObservation,
	resByHost map[string][]*index.ResourceObservation,
	now time.Time,
	window time.Duration,
) []shared.Geo {
	var geos []shared.Geo

	for geoName, hostObs := range hostsByGeo {
		geo := shared.Geo{Name: geoName}

		for _, obs := range hostObs {
			// Collect host IPs.
			var hostIPs []string
			for _, ip := range obs.IPs {
				hostIPs = append(hostIPs, ip.Address)
			}

		totalMem := int64(obs.MemoryTotal)
			availMem := int64(obs.MemoryAvail)
			usedMem := totalMem - availMem
			if usedMem < 0 {
				usedMem = 0
			}
			host := shared.Host{
				ID:               obs.Record.HostID,
				Description:      obs.Record.Description,
				Platform:         obs.Record.Platform,
				Geo:              geoName,
				Hostname:         obs.Record.Hostname,
				OSName:           obs.Record.OSName,
				OSVersion:        obs.Record.OSVersion,
				Kernel:           obs.Record.Kernel,
				Architecture:     obs.Record.Architecture,
				IPs:              shared.FilterIPs(hostIPs),
				CPU: shared.CPUInfo{
					Model:   obs.CPUModel,
					Sockets: int(obs.CPUSockets),
					Cores:   int(obs.CPUCores),
					Threads: int(obs.CPUThreads),
				},
				Memory: shared.MemoryInfo{
					TotalBytes:     totalMem,
					AvailableBytes: &availMem,
					UsedBytes:      &usedMem,
				},
				ObservationState: index.ObservationState(obs.LastSeen, now, window),
			}

			if obs.CPUUsage > 0 && index.IsUsageFresh(obs.LastSeen, now, shared.UILivenessWindow) {
				usage := obs.CPUUsage
				threads := float64(obs.CPUThreads)
				if threads > 0 {
					used := threads * usage
					free := threads - used
					host.CPU.UsageRatio = &usage
					host.CPU.UsedThreadEquiv = &used
					host.CPU.FreeThreadEquiv = &free
				}
			}

			// Merge hugepages.
			var hugepages []shared.HugepagePool
			var hpTotal, hpFree int64
			for pageSize, total := range obs.HugepagesTotal {
				var ps int64
				fmt.Sscanf(pageSize, "%d", &ps)
				free := obs.HugepagesFree[pageSize]
				hpTotal += int64(total)
				hpFree += int64(free)
				hugepages = append(hugepages, shared.HugepagePool{
					PageSizeBytes: ps,
					TotalBytes:    int64(total),
					FreeBytes:     &[]int64{int64(free)}[0],
				})
			}
			host.Memory.HugepagesTotalBytes = hpTotal
			if hpFree > 0 {
				host.Memory.HugepagesFreeBytes = &hpFree
			}
			host.Memory.Hugepages = hugepages

			// Merge block devices — group by 8% capacity tolerance (§16.3).
			host.Disks = groupDisks(obs.BlockDevices)

			// Merge filesystems — deduplicate by ID.
			host.Filesystems = dedupFilesystems(obs.Filesystems, now, shared.UILivenessWindow)

			// Merge storage pools (LVM for KVM, datastores for ESXi, dir/btrfs/zfs for LXD).
			// Skip the libvirt "default" pool.
			for _, p := range obs.StoragePools {
				if p.PoolName == "default" {
					continue
				}
				avail := int64(p.AvailBytes)
				host.StoragePools = append(host.StoragePools, shared.StoragePool{
					PoolID:         p.PoolID,
					PoolName:       p.PoolName,
					PoolType:       p.PoolType,
					TotalBytes:     int64(p.TotalBytes),
					AvailableBytes: &avail,
				})
			}

			if !index.IsUsageFresh(obs.LastSeen, now, shared.UILivenessWindow) {
				host.LastSeen = &obs.LastSeen
			}

			geo.Hosts = append(geo.Hosts, host)
		}

		// Sort hosts by ID.
		sort.Slice(geo.Hosts, func(i, j int) bool {
			return geo.Hosts[i].ID < geo.Hosts[j].ID
		})

		// Collect resources belonging to hosts in this geo.
		for _, host := range geo.Hosts {
			resources := resByHost[host.ID]
			for _, res := range resources {
				switch res.Record.Kind {
				case shared.KindLibvirtVM, shared.KindEsxiVM:
					var ips []string
					for _, ip := range res.IPs {
						ips = append(ips, ip.Address)
					}
					vm := shared.VMResource{
						InventoryID:      res.StableID,
						HostID:           res.Record.HostID,
						Name:             res.Record.Name,
						Kind:             res.Record.Kind,
						Description:      res.Record.Description,
						GuestOS:          res.Record.GuestOS,
						Architecture:     res.Record.Architecture,
						CPUCount:         int64(res.CPUCount),
						MemoryBytes:      int64(res.MemoryBytes),
						DiskTotalBytes:   int64(res.DiskBytes),
						IPs:              ips,
						CapacitySourceCPU:    shared.CapacityConfigured,
						CapacitySourceRAM:    shared.CapacityConfigured,
						CapacitySourceDisk:   shared.CapacityConfigured,
						ObservationState: index.ObservationState(res.LastSeen, now, window),
					}
					if res.Record.Kind == shared.KindLibvirtVM {
						vm.Platform = "KVM"
					} else {
						vm.Platform = "ESXi"
					}
					if !index.IsUsageFresh(res.LastSeen, now, shared.UILivenessWindow) {
						vm.LastSeen = &res.LastSeen
					}
					geo.VirtualMachines = append(geo.VirtualMachines, vm)

				case shared.KindLXDContainer:
					var ips []string
					for _, ip := range res.IPs {
						ips = append(ips, ip.Address)
					}
					container := shared.LXDContainer{
						InventoryID:      res.StableID,
						HostID:           res.Record.HostID,
						Name:             res.Record.Name,
						Kind:             shared.KindLXDContainer,
						Description:      res.Record.Description,
						GuestOS:          res.Record.GuestOS,
						Architecture:     res.Record.Architecture,
						CPUCount:         int64(res.CPUCount),
						MemoryBytes:      int64(res.MemoryBytes),
						RootDiskBytes:    int64(res.DiskBytes),
						IPs:              ips,
						CapacitySourceCPU:    shared.CapacityConfigured,
						CapacitySourceRAM:    shared.CapacityConfigured,
						CapacitySourceDisk:   shared.CapacityConfigured,
						ObservationState: index.ObservationState(res.LastSeen, now, window),
					}
					if !index.IsUsageFresh(res.LastSeen, now, shared.UILivenessWindow) {
						container.LastSeen = &res.LastSeen
					}
					geo.LXDContainers = append(geo.LXDContainers, container)
				}
			}
		}

		geos = append(geos, geo)
	}

	return geos
}

func sortStringsInsensitive(a, b string) bool {
	aLower, bLower := toLower(a), toLower(b)
	if aLower != bLower {
		return aLower < bLower
	}
	return a < b
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

func int64Ptr(v int64) *int64 { return &v }

// groupDisks groups block devices by 8% capacity tolerance (§16.3).
func groupDisks(devices []prometheus.BlockDeviceRecord) []shared.DiskGroup {
	if len(devices) == 0 {
		return nil
	}
	// Sort by size ascending.
	sort.Slice(devices, func(i, j int) bool { return devices[i].SizeBytes < devices[j].SizeBytes })

	var groups []shared.DiskGroup
	groupRef := devices[0].SizeBytes
	count := 1
	for i := 1; i < len(devices); i++ {
		diff := devices[i].SizeBytes - groupRef
		if diff < 0 {
			diff = -diff
		}
		if float64(diff)/float64(groupRef) <= 0.08 {
			count++
			if devices[i].SizeBytes < groupRef {
				groupRef = devices[i].SizeBytes
			}
		} else {
			groups = append(groups, shared.DiskGroup{SizeBytes: int64(groupRef), Count: count})
			groupRef = devices[i].SizeBytes
			count = 1
		}
	}
	groups = append(groups, shared.DiskGroup{SizeBytes: int64(groupRef), Count: count})
	return groups
}

// dedupFilesystems merges filesystem records by filesystem_id (§16.4).
func dedupFilesystems(records []prometheus.FilesystemRecord, now time.Time, window time.Duration) []shared.Filesystem {
	byID := make(map[string]*shared.Filesystem)
	var order []string
	for _, r := range records {
		fs, ok := byID[r.FilesystemID]
		if !ok {
			fs = &shared.Filesystem{
				FilesystemID:   r.FilesystemID,
				FilesystemType: r.FilesystemType,
			}
			byID[r.FilesystemID] = fs
			order = append(order, r.FilesystemID)
		}
		if r.TotalBytes > 0 {
			fs.TotalBytes = int64(r.TotalBytes)
		}
		if r.AvailBytes > 0 {
			avail := int64(r.AvailBytes)
			fs.AvailableBytes = &avail
		}
		if r.Mountpoint != "" {
			fs.Mountpoints = append(fs.Mountpoints, r.Mountpoint)
		}
	}
	var result []shared.Filesystem
	for _, id := range order {
		result = append(result, *byID[id])
	}
	return result
}

// MergeIPs attaches IP records to hosts.
func MergeIPs(host *shared.Host, ips []prometheus.HostIPRecord) {
	var filtered []string
	for _, ip := range ips {
		if ip.HostID == host.ID {
			filtered = append(filtered, ip.Address)
		}
	}
	host.IPs = shared.FilterIPs(filtered)
}
