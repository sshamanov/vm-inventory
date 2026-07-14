package exporter

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"vm-inventory/internal/shared"
)

// AtomicGatherer implements prometheus.Gatherer and provides atomic
// swap of the underlying metric registry (§9.3: no half-data publication).
type AtomicGatherer struct {
	mu       sync.RWMutex
	registry *prometheus.Registry
}

// NewAtomicGatherer creates an empty AtomicGatherer.
func NewAtomicGatherer() *AtomicGatherer {
	return &AtomicGatherer{
		registry: prometheus.NewRegistry(),
	}
}

// Gather implements prometheus.Gatherer.
func (g *AtomicGatherer) Gather() ([]*dto.MetricFamily, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.registry.Gather()
}

// Swap atomically replaces the registry. The old registry is discarded.
func (g *AtomicGatherer) Swap(newReg *prometheus.Registry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.registry = newReg
}

// SnapshotManager manages the lifecycle of collector snapshots per §9.
type SnapshotManager struct {
	snapshots    map[string]*collectorSnapshot // keyed by collector name
	mu           sync.RWMutex
	exporterHost string
}

type collectorSnapshot struct {
	registry      *prometheus.Registry
	timestamp     time.Time
	expiresAt     time.Time
	valid         bool
	skippedRuns   atomic.Int64
	running       atomic.Bool
}

// NewSnapshotManager creates a SnapshotManager for the given exporter host.
func NewSnapshotManager(exporterHost string) *SnapshotManager {
	return &SnapshotManager{
		snapshots:    make(map[string]*collectorSnapshot),
		exporterHost: exporterHost,
	}
}

// TryCollect attempts to start a collection run for the given collector.
// Returns false if a previous run is still in progress (§9.4: no overlap).
func (m *SnapshotManager) TryCollect(collectorName string) bool {
	m.mu.Lock()
	snap, ok := m.snapshots[collectorName]
	if !ok {
		snap = &collectorSnapshot{
			registry:  prometheus.NewRegistry(),
			timestamp: time.Time{},
			valid:     false,
		}
		m.snapshots[collectorName] = snap
	}
	m.mu.Unlock()

	if snap.running.CompareAndSwap(false, true) {
		return true
	}
	snap.skippedRuns.Add(1)
	return false
}

// CommitSnapshot replaces the collector's registry with a new one containing
// the collection results. Called after a successful collection.
// sourceHost is the host being collected (host_id or ESXi target host_id).
func (m *SnapshotManager) CommitSnapshot(
	collectorName, sourceHost string,
	result *CollectionResult,
	maxAge time.Duration,
) error {
	m.mu.Lock()
	snap := m.snapshots[collectorName]
	m.mu.Unlock()

	reg := prometheus.NewRegistry()

	// Register all metric families.
	for _, mf := range result.Metrics {
		if err := registerMetricFamily(reg, mf); err != nil {
			return fmt.Errorf("registering %s: %w", mf.Name, err)
		}
	}

	// Register collector health metrics.
	healthReg := prometheus.NewRegistry()
	registerCollectorHealth(healthReg, m.exporterHost, collectorName, sourceHost, result.Timestamp, maxAge, true)
	// Merge health into the registry by gathering and re-registering.
	healthFamilies, _ := healthReg.Gather()
	for _, mf := range healthFamilies {
		// Simple approach: register as untyped (info-style gauge).
		for _, m := range mf.Metric {
			g := prometheus.NewGauge(prometheus.GaugeOpts{
				Name: mf.GetName(),
				Help: mf.GetHelp(),
				ConstLabels: labelPairsToMap(m.Label),
			})
			g.Set(m.GetGauge().GetValue())
			reg.MustRegister(g)
		}
	}

	snap.registry = reg
	snap.timestamp = result.Timestamp
	snap.expiresAt = result.Timestamp.Add(maxAge)
	snap.valid = true

	snap.running.Store(false)
	return nil
}

// FailSnapshot marks a collector as failed while keeping the previous snapshot (§9.3).
func (m *SnapshotManager) FailSnapshot(collectorName, sourceHost string) {
	m.mu.RLock()
	snap, ok := m.snapshots[collectorName]
	m.mu.RUnlock()
	if !ok {
		return
	}

	// Build a health-only registry showing failure.
	reg := prometheus.NewRegistry()
	registerCollectorHealth(reg, m.exporterHost, collectorName, sourceHost, snap.timestamp, time.Duration(0), false)
	snap.registry = reg // swap to show failed health only
	snap.running.Store(false)
}

// GetSnapshot returns the current registry and whether it's valid.
func (m *SnapshotManager) GetSnapshot(collectorName string) (*prometheus.Registry, bool) {
	m.mu.RLock()
	snap, ok := m.snapshots[collectorName]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	// Check expiry (§9.3: stop exposing inventory after max_snapshot_age).
	now := time.Now()
	if snap.expiresAt.Before(now) && snap.valid {
		// Expired: keep health metrics but remove inventory.
		reg := prometheus.NewRegistry()
		registerCollectorHealth(reg, m.exporterHost, collectorName, "", snap.timestamp, time.Duration(0), false)
		return reg, false
	}
	return snap.registry, snap.valid
}

// registerMetricFamily converts a typed MetricFamily to a Prometheus metric and registers it.
func registerMetricFamily(reg *prometheus.Registry, mf MetricFamily) error {
	switch mf.Type {
	case "gauge":
		for _, m := range mf.Metrics {
			g := prometheus.NewGauge(prometheus.GaugeOpts{
				Name:        mf.Name,
				Help:        mf.Help,
				ConstLabels: m.Labels,
			})
			g.Set(m.Value)
			reg.MustRegister(g)
		}
	case "counter":
		for _, m := range mf.Metrics {
			c := prometheus.NewCounter(prometheus.CounterOpts{
				Name:        mf.Name,
				Help:        mf.Help,
				ConstLabels: m.Labels,
			})
			c.Add(m.Value)
			reg.MustRegister(c)
		}
	default:
		// Treat unknown types as gauge (info-style).
		for _, m := range mf.Metrics {
			g := prometheus.NewGauge(prometheus.GaugeOpts{
				Name:        mf.Name,
				Help:        mf.Help,
				ConstLabels: m.Labels,
			})
			g.Set(m.Value)
			reg.MustRegister(g)
		}
	}
	return nil
}

func registerCollectorHealth(reg *prometheus.Registry, exporterHost, collector, sourceHost string, snapshotTime time.Time, maxAge time.Duration, up bool) {
	labels := map[string]string{
		shared.LabelExporterHost: exporterHost,
		shared.LabelCollector:    collector,
		shared.LabelSourceHost:   sourceHost,
	}

	upVal := 0.0
	if up {
		upVal = 1.0
	}

	upGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        shared.MetricCollectorUp,
		Help:        "Whether the collector completed its last collection successfully.",
		ConstLabels: labels,
	})
	upGauge.Set(upVal)
	reg.MustRegister(upGauge)

	if snapshotTime.IsZero() {
		return
	}

	lastSuccess := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        shared.MetricCollectorLastSuccess,
		Help:        "Timestamp of the last successful collection.",
		ConstLabels: labels,
	})
	lastSuccess.Set(float64(snapshotTime.Unix()))
	reg.MustRegister(lastSuccess)

	snapTS := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        shared.MetricCollectorSnapshotTS,
		Help:        "The authoritative snapshot timestamp for the current cached data.",
		ConstLabels: labels,
	})
	snapTS.Set(float64(snapshotTime.Unix()))
	reg.MustRegister(snapTS)

	if maxAge > 0 {
		expiry := prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        shared.MetricCollectorSnapshotExpiry,
			Help:        "Timestamp when the current snapshot expires.",
			ConstLabels: labels,
		})
		expiry.Set(float64(snapshotTime.Add(maxAge).Unix()))
		reg.MustRegister(expiry)
	}

	validVal := 0.0
	if up {
		validVal = 1.0
	}
	valid := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        shared.MetricCollectorSnapshotValid,
		Help:        "Whether the current snapshot is valid (1) or expired/failed (0).",
		ConstLabels: labels,
	})
	valid.Set(validVal)
	reg.MustRegister(valid)
}

func labelPairsToMap(pairs []*dto.LabelPair) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if p.Name != nil && p.Value != nil {
			m[p.GetName()] = p.GetValue()
		}
	}
	return m
}
