package history

import (
	"strconv"
	"testing"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

func TestPasses(t *testing.T) {
	tests := []struct {
		name      string
		retention time.Duration
		want      []Pass
	}{
		{
			name:      "the retention window is covered finely then coarsely",
			retention: shared.RetentionWindow,
			want: []Pass{
				{Lookback: 7 * 24 * time.Hour, Step: time.Hour},
				{Lookback: 366 * 24 * time.Hour, Step: 44 * time.Hour},
			},
		},
		{
			name:      "a short retention needs only one pass",
			retention: 3 * 24 * time.Hour,
			want:      []Pass{{Lookback: 3 * 24 * time.Hour, Step: time.Hour}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Passes(tt.retention)
			if len(got) != len(tt.want) {
				t.Fatalf("Passes = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Passes[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestLastSeenValueAsTime covers the value Prometheus returns for the query:
// seconds with a fractional part.
func TestLastSeenValueAsTime(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want time.Time
	}{
		{"whole seconds", 1757932800, time.Unix(1757932800, 0).UTC()},
		{"fractional seconds are kept", 1757932800.25, time.Unix(1757932800, 250000000).UTC()},
		{"absence is not 1970", 0, time.Time{}},
		{"a negative is not a time", -1, time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unixSeconds(tt.in); !got.Equal(tt.want) {
				t.Errorf("unixSeconds(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// secondsAgo renders a sample time the way Prometheus reports one: Unix
// seconds as a string inside the value tuple.
func secondsAgo(d time.Duration) string {
	return strconv.FormatInt(time.Now().Add(-d).Unix(), 10)
}

func TestDatedByLastSeenOrdersOldestFirst(t *testing.T) {
	results := []prometheus.MetricResult{
		{Value: []interface{}{nil, secondsAgo(3 * time.Hour)}},
		{Value: []interface{}{nil, secondsAgo(30 * time.Hour)}},
		{Value: []interface{}{nil, secondsAgo(12 * time.Hour)}},
	}

	got := datedByLastSeen(results)
	wantOrder := []int{1, 2, 0}
	if len(got) != len(wantOrder) {
		t.Fatalf("dated = %v, want %d entries", got, len(wantOrder))
	}
	for i, want := range wantOrder {
		if got[i].pos != want {
			t.Errorf("dated[%d].pos = %d, want %d", i, got[i].pos, want)
		}
	}
}

func TestDatedByLastSeenDropsValuelessSeries(t *testing.T) {
	got := datedByLastSeen([]prometheus.MetricResult{
		{Value: []interface{}{nil, "0"}},
		{Metric: map[string]string{shared.LabelHostID: "hv1"}}, // no value at all
		{Value: []interface{}{nil, secondsAgo(time.Hour)}},
	})
	if len(got) != 1 {
		t.Fatalf("dated = %v, want only the series carrying a time", got)
	}
}

// TestApplyHostsKeepsTheNewestSeriesLabels covers the case that makes backfill
// order-sensitive: a host_id whose series was re-labelled by an upgrade owns two
// historical series, and the observation must keep the newer one's labels.
func TestApplyHostsKeepsTheNewestSeriesLabels(t *testing.T) {
	idx := index.NewObservationIndex()
	results := []prometheus.MetricResult{
		{
			Metric: map[string]string{shared.LabelHostID: "hv1", shared.LabelKernel: "6.8"},
			Value:  []interface{}{nil, secondsAgo(2 * time.Hour)}, // newer, listed first
		},
		{
			Metric: map[string]string{shared.LabelHostID: "hv1", shared.LabelKernel: "5.15"},
			Value:  []interface{}{nil, secondsAgo(30 * 24 * time.Hour)}, // older
		},
	}

	if got := applyHosts(idx)(results); got != 2 {
		t.Fatalf("applied = %d, want both observations recorded", got)
	}

	hosts := idx.GetHostsByGeo(time.Now(), shared.RetentionWindow)[shared.DefaultGeo]
	if len(hosts) != 1 {
		t.Fatalf("hosts = %d, want 1 identity", len(hosts))
	}
	if got := hosts[0].Record.Kernel; got != "6.8" {
		t.Errorf("Kernel = %q, want the newest series' label", got)
	}
}

func TestApplyResourcesRecordsRetiredInstances(t *testing.T) {
	idx := index.NewObservationIndex()
	results := []prometheus.MetricResult{{
		Metric: map[string]string{
			shared.LabelInventoryID: "hv1:libvirt_vm:uuid1",
			shared.LabelHostID:      "hv1",
			shared.LabelName:        "ldap",
			shared.LabelKind:        shared.KindLibvirtVM,
		},
		Value: []interface{}{nil, secondsAgo(20 * 24 * time.Hour)},
	}}

	if got := applyResources(idx)(results); got != 1 {
		t.Fatalf("applied = %d, want 1", got)
	}
	byHost := idx.GetResourcesByHost(time.Now(), shared.RetentionWindow)
	if len(byHost["hv1"]) != 1 {
		t.Fatalf("resources for hv1 = %d, want 1", len(byHost["hv1"]))
	}
	res := byHost["hv1"][0]
	if res.StableID != "hv1:libvirt_vm:uuid1" || res.Record.Name != "ldap" {
		t.Errorf("recorded %q/%q, want the identity from the labels", res.StableID, res.Record.Name)
	}
}
