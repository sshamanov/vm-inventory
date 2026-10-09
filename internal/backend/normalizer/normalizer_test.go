package normalizer

import (
	"reflect"
	"testing"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

// TestBuildUISnapshotCarriesVMTitleAndIPs covers the libvirt path end to end
// from decoded metrics to the UI snapshot: title, name, description, guest OS
// and joined IP addresses must all reach the VM resource.
func TestBuildUISnapshotCarriesVMTitleAndIPs(t *testing.T) {
	now := time.Now()
	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Geo: "Test", Platform: "linux"}, now)
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1",
		HostID:      "hv1",
		Name:        "ldap01_192.0.2.25",
		Title:       "LDAP / DNS",
		Kind:        shared.KindLibvirtVM,
		Description: "Denver LDAP / DNS Server",
		GuestOS:     "Ubuntu 22.04",
	}, now)
	idx.ApplyResourceIPs([]prometheus.MetricResult{
		{Metric: map[string]string{
			shared.LabelInventoryID: "hv1:libvirt_vm:uuid1",
			shared.LabelAddress:     "192.0.2.25",
			shared.LabelFamily:      "4",
		}},
		{Metric: map[string]string{
			shared.LabelInventoryID: "hv1:libvirt_vm:uuid1",
			shared.LabelAddress:     "192.0.2.1", // sorts before the management address
			shared.LabelFamily:      "4",
		}},
	}, now)

	snap := New(idx).BuildUISnapshot(shared.UIWindowNow)
	if len(snap.Geos) != 1 {
		t.Fatalf("geos = %d, want 1", len(snap.Geos))
	}
	vms := snap.Geos[0].VirtualMachines
	if len(vms) != 1 {
		t.Fatalf("VMs = %d, want 1", len(vms))
	}
	vm := vms[0]
	if vm.Title != "LDAP / DNS" {
		t.Errorf("Title = %q, want %q", vm.Title, "LDAP / DNS")
	}
	if vm.Name != "ldap01_192.0.2.25" {
		t.Errorf("Name = %q, want the stable name preserved", vm.Name)
	}
	if vm.GuestOS != "Ubuntu 22.04" {
		t.Errorf("GuestOS = %q", vm.GuestOS)
	}
	// The name-embedded management address must come first, ahead of the
	// lower address that canonical sorting would place before it.
	want := []string{"192.0.2.25", "192.0.2.1"}
	if !reflect.DeepEqual(vm.IPs, want) {
		t.Errorf("IPs = %v, want %v", vm.IPs, want)
	}
}

// TestBuildUISnapshotRetiresWhatTheWindowReveals is the behaviour the view
// switch depends on: widening the window reveals instances that are already
// retired, dated by their last observation, and it never presents them as
// current (§15.1).
func TestBuildUISnapshotRetiresWhatTheWindowReveals(t *testing.T) {
	idx := index.NewObservationIndex()
	gone := time.Now().Add(-10 * 24 * time.Hour)

	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "live", Geo: "Test"}, time.Now())
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "gone", Geo: "Test"}, gone)
	idx.UpdateHostField("gone", func(h *index.HostObservation) {
		h.MemoryTotal = 64 << 30
		h.MemoryAvail = 16 << 30
	}, gone)

	nowView := New(idx).BuildUISnapshot(shared.UIWindowNow)
	if got := hostIDs(nowView); !reflect.DeepEqual(got, []string{"live"}) {
		t.Errorf("now view hosts = %v, want only the live host", got)
	}

	monthView := New(idx).BuildUISnapshot(shared.UIWindowMonth)
	if got := hostIDs(monthView); !reflect.DeepEqual(got, []string{"gone", "live"}) {
		t.Fatalf("month view hosts = %v, want both hosts", got)
	}

	for _, host := range monthView.Geos[0].Hosts {
		switch host.ID {
		case "live":
			if host.ObservationState != shared.ObservationCurrent {
				t.Errorf("live host state = %q, want current", host.ObservationState)
			}
			if host.LastSeen != nil {
				t.Errorf("live host carries last_seen %v", host.LastSeen)
			}
		case "gone":
			if host.ObservationState != shared.ObservationRetained {
				t.Errorf("retired host state = %q, want retained", host.ObservationState)
			}
			if host.LastSeen == nil {
				t.Error("retired host has no last_seen to label it with")
			}
			// Capacity survives the host going away; usage does not (§15.3).
			if host.Memory.TotalBytes != 64<<30 {
				t.Errorf("retired host total = %d, want the last known capacity", host.Memory.TotalBytes)
			}
			if host.Memory.AvailableBytes != nil || host.Memory.UsedBytes != nil {
				t.Errorf("retired host reported usage: available=%v used=%v",
					host.Memory.AvailableBytes, host.Memory.UsedBytes)
			}
		}
	}
}

// TestBuildUISnapshotDropsUsageOutsideFreshness covers a resource that has
// stopped being collected while its host is still running — the migrated VM.
// The row is kept and dated rather than deleted, because both the old host and
// the new one are shown.
func TestBuildUISnapshotDropsUsageOutsideFreshness(t *testing.T) {
	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Geo: "Test"}, time.Now())
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1",
		HostID:      "hv1",
		Name:        "ldap",
		Kind:        shared.KindLibvirtVM,
	}, time.Now().Add(-3*time.Hour))

	snap := New(idx).BuildUISnapshot(shared.UIWindowMonth)
	vms := snap.Geos[0].VirtualMachines
	if len(vms) != 1 {
		t.Fatalf("VMs = %d, want the departed resource kept in a wider view", len(vms))
	}
	vm := vms[0]
	if vm.ObservationState != shared.ObservationRetained {
		t.Errorf("state = %q, want retained", vm.ObservationState)
	}
	if vm.LastSeen == nil {
		t.Error("retained VM has no last_seen")
	}
}

// TestBuildUISnapshotSuppressesRelabelledPhantom covers the artefact the
// backfill exposes: when an instance's platform source id changes — an exporter
// that starts reading the hypervisor's durable id, a re-registered guest — the
// old series stops while the instance keeps running under the new identity.
// Reporting the stale series as a departure would draw the same VM twice, once
// of them dimmed as gone, so it is suppressed where its live twin sits (§15.1).
func TestBuildUISnapshotSuppressesRelabelledPhantom(t *testing.T) {
	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Geo: "Test"}, time.Now())

	// One VM, two identities: what it used to be keyed by, and what it is keyed
	// by now.
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:esxi_vm:10", HostID: "hv1", Name: "vm-a", Kind: shared.KindEsxiVM,
	}, time.Now().Add(-20*24*time.Hour))
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:esxi_vm:564dd8a4-3213-b0ee-4c1b-75827ae16b04",
		HostID:      "hv1", Name: "vm-a", Kind: shared.KindEsxiVM,
	}, time.Now())

	// A container sharing the name is a different instance, not a twin.
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:lxd_container:vm-a", HostID: "hv1", Name: "vm-a",
		Kind: shared.KindLXDContainer,
	}, time.Now().Add(-20*24*time.Hour))

	snap := New(idx).BuildUISnapshot(shared.UIWindowMonth)
	vms := snap.Geos[0].VirtualMachines
	if len(vms) != 1 {
		t.Fatalf("VMs = %d, want only the identity still running", len(vms))
	}
	if want := "hv1:esxi_vm:564dd8a4-3213-b0ee-4c1b-75827ae16b04"; vms[0].InventoryID != want {
		t.Errorf("kept %q, want the current identity", vms[0].InventoryID)
	}
	if vms[0].ObservationState != shared.ObservationCurrent {
		t.Errorf("state = %q, want current", vms[0].ObservationState)
	}

	cts := snap.Geos[0].LXDContainers
	if len(cts) != 1 {
		t.Fatalf("containers = %d, want the departed same-named container kept", len(cts))
	}
	if cts[0].ObservationState != shared.ObservationRetained {
		t.Errorf("container state = %q, want retained", cts[0].ObservationState)
	}
}

// TestBuildUISnapshotShowsBothEndsOfAMigration is the case the suppression must
// not swallow: a guest that moved naming the same on another host is a real
// departure from the host it left, and the arrival is running there.
func TestBuildUISnapshotShowsBothEndsOfAMigration(t *testing.T) {
	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Geo: "Test"}, time.Now())
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv2", Geo: "Test"}, time.Now())

	// A fresh domain on the destination gets a new UUID, so the two ends are
	// distinct identities that share only the name.
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1", HostID: "hv1", Name: "ldap",
		Kind: shared.KindLibvirtVM,
	}, time.Now().Add(-6*time.Hour))
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv2:libvirt_vm:uuid2", HostID: "hv2", Name: "ldap",
		Kind: shared.KindLibvirtVM,
	}, time.Now())

	snap := New(idx).BuildUISnapshot(shared.UIWindowMonth)
	vms := snap.Geos[0].VirtualMachines
	if len(vms) != 2 {
		t.Fatalf("VMs = %d, want the departure and the arrival both shown", len(vms))
	}

	byHost := map[string]shared.VMResource{}
	for _, vm := range vms {
		byHost[vm.HostID] = vm
	}
	left, ok := byHost["hv1"]
	if !ok {
		t.Fatal("the host the guest left has no record of it")
	}
	if left.ObservationState != shared.ObservationRetained || left.LastSeen == nil {
		t.Errorf("departure state = %q last_seen = %v, want retained and dated",
			left.ObservationState, left.LastSeen)
	}
	if arrived, ok := byHost["hv2"]; !ok || arrived.ObservationState != shared.ObservationCurrent {
		t.Errorf("arrival on hv2 = %+v, want current", arrived)
	}
}

func hostIDs(snap *shared.NormalizedInventory) []string {
	var ids []string
	for _, geo := range snap.Geos {
		for _, h := range geo.Hosts {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

// The publication hash covers the rendered page (§19.4), so the same inventory
// has to render in the same order every time. Resources reach the snapshot from
// a Go map, whose iteration order differs between calls; §5.2 requires them
// ordered by host, then name. Without that sort the page row order changes on
// every render and no two publications can ever compare equal.
func TestBuildConfluenceSnapshotOrdersResourcesCanonically(t *testing.T) {
	now := time.Now()
	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv2", Geo: "Test"}, now)
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Geo: "Test"}, now)

	for _, r := range []struct{ id, host, name string }{
		{"z", "hv2", "zeta"},
		{"a", "hv1", "alpha"},
		{"m", "hv1", "mu"},
		{"b", "hv2", "beta"},
	} {
		idx.UpsertResource(prometheus.ResourceInfoRecord{
			InventoryID: r.host + ":libvirt_vm:" + r.id,
			HostID:      r.host,
			Name:        r.name,
			Kind:        shared.KindLibvirtVM,
		}, now)
	}
	for _, r := range []struct{ id, host, name string }{
		{"c2", "hv2", "ct-b"},
		{"c1", "hv1", "ct-a"},
	} {
		idx.UpsertResource(prometheus.ResourceInfoRecord{
			InventoryID: r.host + ":lxd_container:" + r.id,
			HostID:      r.host,
			Name:        r.name,
			Kind:        shared.KindLXDContainer,
		}, now)
	}

	wantVMs := []string{"hv1/alpha", "hv1/mu", "hv2/beta", "hv2/zeta"}
	wantCTs := []string{"hv1/ct-a", "hv2/ct-b"}
	// Repeated, because a map's order is randomized per iteration: one lucky
	// render must not be able to pass this.
	for i := 0; i < 8; i++ {
		snap := New(idx).BuildConfluenceSnapshot()
		if len(snap.Geos) != 1 {
			t.Fatalf("geos = %d, want 1", len(snap.Geos))
		}
		geo := snap.Geos[0]

		var vms []string
		for _, vm := range geo.VirtualMachines {
			vms = append(vms, vm.HostID+"/"+vm.Name)
		}
		if !reflect.DeepEqual(vms, wantVMs) {
			t.Fatalf("render %d: VMs = %v, want %v", i, vms, wantVMs)
		}

		var cts []string
		for _, ct := range geo.LXDContainers {
			cts = append(cts, ct.HostID+"/"+ct.Name)
		}
		if !reflect.DeepEqual(cts, wantCTs) {
			t.Fatalf("render %d: containers = %v, want %v", i, cts, wantCTs)
		}
	}
}
