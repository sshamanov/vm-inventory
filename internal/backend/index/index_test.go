package index

import (
	"testing"
	"time"

	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

func TestBackfillHostCreatesAndAdvances(t *testing.T) {
	idx := NewObservationIndex()
	older := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	newer := older.Add(48 * time.Hour)

	if !idx.BackfillHost(prometheus.HostInfoRecord{HostID: "hv1", Kernel: "5.15"}, older) {
		t.Fatal("backfill of an unknown host reported no change")
	}
	// A newer observation of the same host advances both the reading and the
	// time it was last seen.
	if !idx.BackfillHost(prometheus.HostInfoRecord{HostID: "hv1", Kernel: "6.8"}, newer) {
		t.Fatal("backfill of a newer observation reported no change")
	}

	got := idx.hosts["hv1"]
	if got == nil {
		t.Fatal("host was not recorded")
	}
	if got.Record.Kernel != "6.8" {
		t.Errorf("Kernel = %q, want the newer reading", got.Record.Kernel)
	}
	if !got.LastSeen.Equal(newer) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen, newer)
	}
}

// TestBackfillHostRefusesToRegress is the guard that lets the backfill run
// beside the live refresh: its record carries the labels of whichever series
// was live back then, so an older result applied over a running host would
// misreport it.
func TestBackfillHostRefusesToRegress(t *testing.T) {
	idx := NewObservationIndex()
	live := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Kernel: "6.8"}, live)

	if idx.BackfillHost(prometheus.HostInfoRecord{HostID: "hv1", Kernel: "5.15"}, live.Add(-30*24*time.Hour)) {
		t.Fatal("backfill replaced a newer observation with an older one")
	}

	got := idx.hosts["hv1"]
	if got.Record.Kernel != "6.8" {
		t.Errorf("Kernel = %q, want the live reading kept", got.Record.Kernel)
	}
	if !got.LastSeen.Equal(live) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen, live)
	}
}

// TestBackfillHostKeepsDetailFields matters because a host already known from
// live metrics has joined detail (devices, pools) that the identity record
// alone would not restore.
func TestBackfillHostKeepsDetailFields(t *testing.T) {
	idx := NewObservationIndex()
	live := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1"}, live)
	idx.UpdateHostField("hv1", func(h *HostObservation) {
		h.BlockDevices = []prometheus.BlockDeviceRecord{{DeviceID: "sda", SizeBytes: 512}}
	}, live)

	idx.BackfillHost(prometheus.HostInfoRecord{HostID: "hv1"}, live.Add(-24*time.Hour))

	if got := len(idx.hosts["hv1"].BlockDevices); got != 1 {
		t.Errorf("BlockDevices = %d, want the live detail kept", got)
	}
}

func TestBackfillResourceCreatesAndAdvances(t *testing.T) {
	idx := NewObservationIndex()
	older := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	newer := older.Add(72 * time.Hour)

	if !idx.BackfillResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1", HostID: "hv1", Name: "ldap", Kind: "libvirt_vm",
	}, older) {
		t.Fatal("backfill of an unknown resource reported no change")
	}
	if !idx.BackfillResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1", HostID: "hv1", Name: "ldap-renamed", Kind: "libvirt_vm",
	}, newer) {
		t.Fatal("backfill of a newer observation reported no change")
	}
	if idx.BackfillResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1", HostID: "hv1", Name: "ldap", Kind: "libvirt_vm",
	}, older) {
		t.Fatal("backfill replaced a newer observation with an older one")
	}

	got := idx.resources["hv1:libvirt_vm:uuid1"]
	if got == nil {
		t.Fatal("resource was not recorded")
	}
	if got.Record.Name != "ldap-renamed" {
		t.Errorf("Name = %q, want the newer reading", got.Record.Name)
	}
	if !got.LastSeen.Equal(newer) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen, newer)
	}
}

// TestBackfillSurvivesRetentionPrune states the reason the prune window is the
// retention window and not the view window: an instance that left the "now"
// view weeks ago is still what the "all" view is asking for.
func TestBackfillSurvivesRetentionPrune(t *testing.T) {
	idx := NewObservationIndex()
	idx.BackfillHost(prometheus.HostInfoRecord{HostID: "gone"}, time.Now().Add(-300*24*time.Hour))

	idx.Prune(shared.RetentionWindow)
	if idx.hosts["gone"] == nil {
		t.Fatal("a 300-day-old observation was pruned inside the retention window")
	}

	// Past the window it does go, so the index cannot grow without bound.
	idx.BackfillHost(prometheus.HostInfoRecord{HostID: "ancient"}, time.Now().Add(-400*24*time.Hour))
	idx.Prune(shared.RetentionWindow)
	if idx.hosts["ancient"] != nil {
		t.Error("a 400-day-old observation survived the retention window")
	}
}
