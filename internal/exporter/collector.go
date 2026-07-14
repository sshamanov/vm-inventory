package exporter

import (
	"context"
	"time"
)

// Collector defines a single collection source (§8).
type Collector interface {
	// Name returns a stable name for this collector (e.g. "host", "libvirt", "lxd", "esxi").
	Name() string
	// Collect performs a complete platform collection and returns typed metric families.
	Collect(ctx context.Context) (*CollectionResult, error)
}

// CollectionResult holds the complete output of one collection run.
type CollectionResult struct {
	Metrics     []MetricFamily
	Timestamp   time.Time // authoritative snapshot timestamp
	SourceHost  string    // host_id of the source
}

// MetricFamily groups related metrics together.
type MetricFamily struct {
	Name    string
	Help    string
	Type    string // "gauge", "counter", "info"
	Metrics []Metric
}

// Metric is a single metric sample with labels and value.
type Metric struct {
	Labels map[string]string
	Value  float64
}

// MetricByName finds a metric family by name.
func MetricByName(families []MetricFamily, name string) *MetricFamily {
	for i := range families {
		if families[i].Name == name {
			return &families[i]
		}
	}
	return nil
}
