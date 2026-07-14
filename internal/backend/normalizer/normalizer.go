package normalizer

import (
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
				ObservationState: index.ObservationState(obs.LastSeen, now, window),
			}

			if !index.IsUsageFresh(obs.LastSeen, now, shared.UILivenessWindow) {
				host.LastSeen = &obs.LastSeen
			}

			// Host IPs are collected separately by the decoder.
			// Here we merge them from resource data.

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
					vm := shared.VMResource{
						InventoryID:      res.StableID,
						HostID:           res.Record.HostID,
						Name:             res.Record.Name,
						Kind:             res.Record.Kind,
						Description:      res.Record.Description,
						GuestOS:          res.Record.GuestOS,
						Architecture:     res.Record.Architecture,
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
					container := shared.LXDContainer{
						InventoryID:      res.StableID,
						HostID:           res.Record.HostID,
						Name:             res.Record.Name,
						Kind:             shared.KindLXDContainer,
						Description:      res.Record.Description,
						GuestOS:          res.Record.GuestOS,
						Architecture:     res.Record.Architecture,
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
