package index

import (
	"sync"
	"time"

	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

// ObservationIndex is an in-memory store of hosts and resources (§14.2).
type ObservationIndex struct {
	mu        sync.RWMutex
	hosts     map[string]*HostObservation
	resources map[string]*ResourceObservation
}

// HostObservation tracks a host's latest observation with joined metric data.
type HostObservation struct {
	Record      prometheus.HostInfoRecord
	LastSeen    time.Time
	RefreshID   string
	IPs         []prometheus.HostIPRecord
	CPUSockets  float64
	CPUCores    float64
	CPUThreads  float64
	CPUModel    string
	CPUUsage    float64
	MemoryTotal float64
	MemoryAvail float64
}

// ResourceObservation tracks a resource's latest observation.
type ResourceObservation struct {
	StableID    string
	Record      prometheus.ResourceInfoRecord
	LastSeen    time.Time
	CPUCount    float64
	MemoryBytes float64
	DiskBytes   float64
	IPs         []prometheus.HostIPRecord
}

// NewObservationIndex creates an empty index.
func NewObservationIndex() *ObservationIndex {
	return &ObservationIndex{
		hosts:     make(map[string]*HostObservation),
		resources: make(map[string]*ResourceObservation),
	}
}

// UpsertHost adds or updates a host observation.
func (idx *ObservationIndex) UpsertHost(record prometheus.HostInfoRecord, lastSeen time.Time) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	existing, ok := idx.hosts[record.HostID]
	if ok && !lastSeen.After(existing.LastSeen) {
		return // only update if newer
	}

	idx.hosts[record.HostID] = &HostObservation{
		Record:    record,
		LastSeen:  lastSeen,
		RefreshID: lastSeen.Format(time.RFC3339Nano),
	}
}

// UpsertResource adds or updates a resource observation.
func (idx *ObservationIndex) UpsertResource(record prometheus.ResourceInfoRecord, lastSeen time.Time) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	stableID := shared.StableID(record.HostID, record.Kind, record.InventoryID)
	existing, ok := idx.resources[stableID]
	if ok && !lastSeen.After(existing.LastSeen) {
		return
	}

	idx.resources[stableID] = &ResourceObservation{
		StableID: stableID,
		Record:   record,
		LastSeen: lastSeen,
	}
}

// GetHostsByGeo returns all hosts within a given liveness window, grouped by geo.
func (idx *ObservationIndex) GetHostsByGeo(now time.Time, window time.Duration) map[string][]*HostObservation {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	geoMap := make(map[string][]*HostObservation)
	for _, obs := range idx.hosts {
		if now.Sub(obs.LastSeen) <= window {
			geo := obs.Record.Geo
			if geo == "" {
				geo = shared.DefaultGeo
			}
			geoMap[geo] = append(geoMap[geo], obs)
		}
	}
	return geoMap
}

// GetResourcesByHost returns all resources within the liveness window, grouped by host ID.
func (idx *ObservationIndex) GetResourcesByHost(now time.Time, window time.Duration) map[string][]*ResourceObservation {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	hostMap := make(map[string][]*ResourceObservation)
	for _, obs := range idx.resources {
		if now.Sub(obs.LastSeen) <= window {
			hostMap[obs.Record.HostID] = append(hostMap[obs.Record.HostID], obs)
		}
	}
	return hostMap
}

// ObservationState returns "current" or "retained" based on the liveness window (§5.4, §15).
func ObservationState(lastSeen, now time.Time, window time.Duration) string {
	if now.Sub(lastSeen) <= window {
		return shared.ObservationCurrent
	}
	return shared.ObservationRetained
}

// IsUsageFresh returns true if the observation is within the specified freshness window (§15.3).
func IsUsageFresh(lastSeen, now time.Time, window time.Duration) bool {
	return now.Sub(lastSeen) <= window
}

// Prune removes observations older than the given window.
func (idx *ObservationIndex) Prune(window time.Duration) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	cutoff := time.Now().Add(-window)
	for id, obs := range idx.hosts {
		if obs.LastSeen.Before(cutoff) {
			delete(idx.hosts, id)
		}
	}
	for id, obs := range idx.resources {
		if obs.LastSeen.Before(cutoff) {
			delete(idx.resources, id)
		}
	}
}

// UpdateHostField applies fn to a host, creating a placeholder if needed.
func (idx *ObservationIndex) UpdateHostField(hostID string, fn func(*HostObservation), lastSeen time.Time) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	h, ok := idx.hosts[hostID]
	if !ok {
		h = &HostObservation{Record: prometheus.HostInfoRecord{HostID: hostID}}
		idx.hosts[hostID] = h
	}
	if lastSeen.After(h.LastSeen) {
		h.LastSeen = lastSeen
	}
	fn(h)
}

// UpdateResourceField applies fn to the resource identified by inventoryID,
// creating a placeholder if it doesn't exist yet.
func (idx *ObservationIndex) UpdateResourceField(inventoryID string, fn func(*ResourceObservation), lastSeen time.Time) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	r, ok := idx.resources[inventoryID]
	if !ok {
		r = &ResourceObservation{StableID: inventoryID}
		idx.resources[inventoryID] = r
	}
	if lastSeen.After(r.LastSeen) {
		r.LastSeen = lastSeen
	}
	fn(r)
}

// HostCount returns the total number of hosts in the index.
func (idx *ObservationIndex) HostCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.hosts)
}

// ResourceCount returns the total number of resources in the index.
func (idx *ObservationIndex) ResourceCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.resources)
}
