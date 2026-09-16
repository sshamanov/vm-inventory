package normalizer

import (
	"fmt"
	"sort"
	"time"

	"strings"

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

// BuildUISnapshot builds the UI snapshot for one view window (§15.1, §17.2).
// The window decides how far back the snapshot looks; which of the instances it
// finds count as current is fixed at UILivenessWindow, so asking for a wider
// view never makes a departed instance read as live.
func (n *Normalizer) BuildUISnapshot(window time.Duration) *shared.NormalizedInventory {
	now := time.Now().Truncate(time.Second) // stable within same second

	hostsByGeo := n.idx.GetHostsByGeo(now, window)
	resByHost := n.idx.GetResourcesByHost(now, window)

	geos := n.buildGeos(hostsByGeo, resByHost, now)

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

	geos := n.buildGeos(hostsByGeo, resByHost, now)
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

			// A host that has stopped reporting keeps its last memory reading in
			// the index, so usage has to be checked against the freshness window
			// rather than the view window: rendering a retired host's last known
			// free memory as a live gauge is exactly the reading §15.3 forbids.
			fresh := index.IsUsageFresh(obs.LastSeen, now, shared.UILivenessWindow)

			totalMem := int64(obs.MemoryTotal)
			memory := shared.MemoryInfo{TotalBytes: totalMem}
			if fresh {
				availMem := int64(obs.MemoryAvail)
				usedMem := totalMem - availMem
				if usedMem < 0 {
					usedMem = 0
				}
				memory.AvailableBytes = &availMem
				memory.UsedBytes = &usedMem
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
				Memory:           memory,
				ObservationState: index.ObservationState(obs.LastSeen, now, shared.UILivenessWindow),
			}

			if fresh && obs.CPUUsage > 0 {
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
				pool := shared.HugepagePool{
					PageSizeBytes: ps,
					TotalBytes:    int64(total),
				}
				if fresh {
					pool.FreeBytes = int64Ptr(int64(obs.HugepagesFree[pageSize]))
				}
				hpTotal += int64(total)
				hpFree += int64(obs.HugepagesFree[pageSize])
				hugepages = append(hugepages, pool)
			}
			host.Memory.HugepagesTotalBytes = hpTotal
			if fresh && hpFree > 0 {
				host.Memory.HugepagesFreeBytes = &hpFree
			}
			host.Memory.Hugepages = hugepages

			// Merge block devices — group by 8% capacity tolerance (§16.3).
			host.Disks = groupDisks(obs.BlockDevices)

			// Merge storage pools by type.
			// KVM host: show LVM pools (skip filesystems).
			// ESXi host: show datastores (skip filesystems).
			// LXD host: show LXD pools as combined filesystem usage.
			// Plain Linux: show filesystems only.
			hasPools := len(obs.StoragePools) > 0
			for _, p := range obs.StoragePools {
				// Skip network-backed libvirt pools (NFS, iSCSI, SCSI) — only
				// local pools are shown.
				if isNetworkPool(p.PoolType) {
					continue
				}
				// Skip libvirt's auto-created "default" pool — it cannot be
				// removed but is not used when LVM pools are configured.
				if p.PoolName == "default" && strings.HasPrefix(p.PoolType, "libvirt-") {
					continue
				}
				pool := shared.StoragePool{
					PoolID:     p.PoolID,
					PoolName:   p.PoolName,
					PoolType:   p.PoolType,
					TotalBytes: int64(p.TotalBytes),
				}
				if fresh {
					pool.AvailableBytes = int64Ptr(int64(p.AvailBytes))
				}
				host.StoragePools = append(host.StoragePools, pool)
			}
			// Only show filesystems when there are no platform storage pools.
			if !hasPools {
				host.Filesystems = dedupFilesystems(obs.Filesystems, fresh)
			}

			if !fresh {
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
			// An instance whose platform source id changed keeps running under
			// the new identity while the old one is left behind as a departed
			// series (§15.1). Drawing both would report a departure that never
			// happened, so a retired resource is dropped where a current one of
			// the same kind and name is present on the same host. A resource
			// that moved to another host still shows as retired where it left.
			current := make(map[string]bool, len(resources))
			for _, res := range resources {
				if index.IsUsageFresh(res.LastSeen, now, shared.UILivenessWindow) {
					current[resourceKey(res.Record)] = true
				}
			}
			for _, res := range resources {
				fresh := index.IsUsageFresh(res.LastSeen, now, shared.UILivenessWindow)
				if !fresh && current[resourceKey(res.Record)] {
					continue
				}
				switch res.Record.Kind {
				case shared.KindLibvirtVM, shared.KindEsxiVM:
					var ips []string
					for _, ip := range res.IPs {
						ips = append(ips, ip.Address)
					}
					ips = shared.SelectIPs(res.Record.Name, shared.FilterIPs(ips))
					vm := shared.VMResource{
						InventoryID:      res.StableID,
						HostID:           res.Record.HostID,
						Name:             res.Record.Name,
						Title:            res.Record.Title,
						Kind:             res.Record.Kind,
						Geo:              geoName,
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
						ObservationState: index.ObservationState(res.LastSeen, now, shared.UILivenessWindow),
					}
					if res.Record.Kind == shared.KindLibvirtVM {
						vm.Platform = "KVM"
					} else {
						vm.Platform = "ESXi"
					}
					if !fresh {
						vm.LastSeen = &res.LastSeen
					}
					geo.VirtualMachines = append(geo.VirtualMachines, vm)

				case shared.KindLXDContainer:
					var ips []string
					for _, ip := range res.IPs {
						ips = append(ips, ip.Address)
					}
					ips = shared.SelectIPs(res.Record.Name, shared.FilterIPs(ips))
					container := shared.LXDContainer{
						InventoryID:      res.StableID,
						HostID:           res.Record.HostID,
						Name:             res.Record.Name,
						Kind:             shared.KindLXDContainer,
						Geo:              geoName,
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
						ObservationState: index.ObservationState(res.LastSeen, now, shared.UILivenessWindow),
					}
					if !fresh {
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

// resourceKey identifies a resource by what survives a re-labelling: the host it
// sits on (implicit — resources are grouped per host), its kind and its name.
func resourceKey(rec prometheus.ResourceInfoRecord) string {
	return rec.Kind + "\x00" + rec.Name
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

// dedupFilesystems merges filesystem records by filesystem_id (§16.4). Capacities
// survive a host going away; the free space reading does not, so it is dropped
// for a host outside the freshness window.
func dedupFilesystems(records []prometheus.FilesystemRecord, fresh bool) []shared.Filesystem {
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
		if fresh && r.AvailBytes > 0 {
			fs.AvailableBytes = int64Ptr(int64(r.AvailBytes))
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

// isNetworkPool returns true if poolType is a network-backed libvirt pool
// that should not appear as local storage (NFS, iSCSI, SCSI).
func isNetworkPool(poolType string) bool {
	return strings.HasPrefix(poolType, "libvirt-") &&
		(poolType == "libvirt-netfs" || poolType == "libvirt-iscsi" || poolType == "libvirt-scsi")
}
