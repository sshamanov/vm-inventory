package prometheus

import (
	"testing"
	"time"

	"vm-inventory/internal/shared"
)

func TestDecodeResourceInfoTitle(t *testing.T) {
	m := MetricResult{
		Metric: map[string]string{
			shared.LabelInventoryID:  "hv1:libvirt_vm:uuid1",
			shared.LabelHostID:       "hv1",
			shared.LabelName:         "web-01",
			shared.LabelTitle:        "Web Frontend",
			shared.LabelKind:         shared.KindLibvirtVM,
			shared.LabelDescription:  "Primary web server",
			shared.LabelGuestOS:      "Ubuntu 22.04",
			shared.LabelArchitecture: "x86_64",
		},
	}

	got := DecodeResourceInfo(m)
	if got.Title != "Web Frontend" {
		t.Errorf("Title = %q, want %q", got.Title, "Web Frontend")
	}
	if got.Name != "web-01" {
		t.Errorf("Name = %q, want %q", got.Name, "web-01")
	}
	if got.GuestOS != "Ubuntu 22.04" {
		t.Errorf("GuestOS = %q, want %q", got.GuestOS, "Ubuntu 22.04")
	}
}

func TestDecodeResourceInfoAbsentTitle(t *testing.T) {
	// A series whose platform has no title concept must decode to an empty title.
	m := MetricResult{Metric: map[string]string{
		shared.LabelName: "vm-ams-1",
		shared.LabelKind: shared.KindEsxiVM,
	}}
	if got := DecodeResourceInfo(m); got.Title != "" {
		t.Errorf("Title = %q, want empty", got.Title)
	}
}

// TestQueryLastSeen pins the shape of the backfill query. timestamp() has to be
// evaluated inside the subquery: last_over_time would report the time of the
// evaluation rather than of the last sample, dating every departed series to
// now (§15.4).
func TestQueryLastSeen(t *testing.T) {
	got := QueryLastSeen(shared.MetricHostInfo, 30*24*time.Hour, 6*time.Hour)
	want := `max_over_time(timestamp(inventory_host_info)[30d:6h])`
	if got != want {
		t.Errorf("QueryLastSeen = %q, want %q", got, want)
	}
}

func TestLastSeenStep(t *testing.T) {
	tests := []struct {
		name     string
		lookback time.Duration
		maxSteps int
		want     time.Duration
	}{
		{"a year is coarsened to fit the step budget", 365 * 24 * time.Hour, 200, 44 * time.Hour},
		{"the retention window's step is the one the passes use",
			366 * 24 * time.Hour, 200, 44 * time.Hour},
		{"a week needs no coarsening", 7 * 24 * time.Hour, 200, time.Hour},
		{"never finer than an hour", time.Hour, 200, time.Hour},
		{"a zero budget still yields a usable step", 30 * 24 * time.Hour, 0, 30 * 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastSeenStep(tt.lookback, tt.maxSteps); got != tt.want {
				t.Errorf("LastSeenStep(%v, %d) = %v, want %v",
					tt.lookback, tt.maxSteps, got, tt.want)
			}
		})
	}
}

// TestLastSeenStepHonoursBudget is the property that matters more than any one
// value: the step must never produce more evaluations than the budget, or the
// query runs past the client's timeout.
func TestLastSeenStepHonoursBudget(t *testing.T) {
	for _, lookback := range []time.Duration{
		24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 366 * 24 * time.Hour,
	} {
		step := LastSeenStep(lookback, 200)
		if steps := lookback / step; steps > 200 {
			t.Errorf("lookback %v at step %v needs %d steps, want <= 200", lookback, step, steps)
		}
	}
}

func TestPromDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Hour, "0s"},
		{time.Hour, "1h"},
		{6 * time.Hour, "6h"},
		{90 * time.Minute, "1h30m"},
		{30 * 24 * time.Hour, "30d"},
		{44 * time.Hour, "1d20h"},
		{500 * time.Millisecond, "1s"}, // below the units this is ever called with
	}

	for _, tt := range tests {
		if got := promDuration(tt.in); got != tt.want {
			t.Errorf("promDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
