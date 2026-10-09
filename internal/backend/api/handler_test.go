package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/backend/state"
	"vm-inventory/internal/shared"
)

// viewFixture returns a handler over an index holding one running host and one
// that stopped ten days ago.
func viewFixture(t *testing.T) *Handler {
	t.Helper()

	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "live", Geo: "Test"}, time.Now())
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "gone", Geo: "Test"}, time.Now().Add(-10*24*time.Hour))

	store := state.NewStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(idx, nil, store, nil, "", "", logger)
}

func getInventory(t *testing.T, h *Handler, window string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/api/inventory"
	if window != "" {
		target += "?window=" + window
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.handleInventory(rec, req)
	return rec
}

func decodeHostIDs(t *testing.T, body []byte) []string {
	t.Helper()

	var snap shared.NormalizedInventory
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	var ids []string
	for _, geo := range snap.Geos {
		for _, host := range geo.Hosts {
			ids = append(ids, host.ID)
		}
	}
	return ids
}

func TestHandleInventoryRejectsUnknownWindow(t *testing.T) {
	h := viewFixture(t)

	rec := getInventory(t, h, "week", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown window", rec.Code)
	}
}

// TestHandleInventoryServesTheRequestedWindow covers the trap in caching one
// snapshot per window: the "now" view has to be rendered and cached before the
// "month" view is asked for, and the answer must still be the month's.
func TestHandleInventoryServesTheRequestedWindow(t *testing.T) {
	h := viewFixture(t)

	nowRec := getInventory(t, h, "", nil)
	if nowRec.Code != http.StatusOK {
		t.Fatalf("now view status = %d", nowRec.Code)
	}
	if got := decodeHostIDs(t, nowRec.Body.Bytes()); len(got) != 1 || got[0] != "live" {
		t.Errorf("now view hosts = %v, want only the running host", got)
	}

	monthRec := getInventory(t, h, "month", nil)
	if monthRec.Code != http.StatusOK {
		t.Fatalf("month view status = %d", monthRec.Code)
	}
	if got := decodeHostIDs(t, monthRec.Body.Bytes()); len(got) != 2 {
		t.Errorf("month view hosts = %v, want both hosts", got)
	}

	// And the default view is still the narrow one after the wide one was cached.
	again := getInventory(t, h, "", nil)
	if got := decodeHostIDs(t, again.Body.Bytes()); len(got) != 1 {
		t.Errorf("now view after month = %v, want only the running host", got)
	}
}

// refreshFixture returns a handler over an index holding a host that is still
// reporting, a host that stopped ten days ago, and a VM belonging to the host
// that stopped. Its Prometheus client holds the two halves of the truth a real
// one does: the live query answers with the host still being scraped, and the
// history pass answers with a host that departed ten days ago. Neither answer
// contains the host the index already knows and Prometheus has forgotten.
func refreshFixture(t *testing.T) *Handler {
	t.Helper()

	idx := index.NewObservationIndex()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "live", Geo: "Test"}, time.Now())
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "gone", Geo: "Test"}, time.Now().Add(-10*24*time.Hour))
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "gone:libvirt_vm:1",
		HostID:      "gone",
		Name:        "vm-1",
		Kind:        shared.KindLibvirtVM,
	}, time.Now().Add(-10*24*time.Hour))

	departed := time.Now().Add(-10 * 24 * time.Hour)
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("query")
		switch {
		case !strings.Contains(query, "timestamp("):
			// The live instant query: only the series being scraped now.
			io.WriteString(w, promAnswer(
				[]string{`{"__name__":"inventory_host_info","host_id":"live","geo":"Test"}`}, []string{"1"}))
		case strings.Contains(query, "inventory_host_info"):
			// The history pass over hosts: a departure the index never saw.
			io.WriteString(w, promAnswer(
				[]string{`{"__name__":"inventory_host_info","host_id":"departed","geo":"Test"}`},
				[]string{strconv.FormatInt(departed.Unix(), 10)}))
		default:
			// The history pass over resources: nothing has departed.
			io.WriteString(w, promAnswer(nil, nil))
		}
	}))
	t.Cleanup(prom.Close)

	client, err := prometheus.NewClient(prom.URL)
	if err != nil {
		t.Fatalf("prometheus client: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(idx, client, state.NewStore(), nil, "", "", logger)
}

// promAnswer renders a Prometheus instant-query answer carrying one series per
// metric/value pair, or the empty vector when given none.
func promAnswer(metrics, values []string) string {
	series := make([]string, len(metrics))
	for i := range metrics {
		series[i] = fmt.Sprintf(`{"metric":%s,"value":[1,%q]}`, metrics[i], values[i])
	}
	return `{"status":"success","data":{"resultType":"vector","result":[` +
		strings.Join(series, ",") + `]}}`
}

func postRefresh(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Inventory-Action", "refresh")
	rec := httptest.NewRecorder()
	h.handleRefresh(rec, req)
	return rec
}

// TestRefreshKeepsAndRecoversUnobservedInstances covers §5.4 on the manual
// refresh path. A refresh re-reads what Prometheus is scraping now, and an
// instance that has stopped being scraped is simply not in that answer — it is
// not thereby gone. The index has to go on holding it, dated to when it was last
// seen, and the history pass the same refresh runs has to find the ones the
// index has never heard of, or the wider views lose every departed host and
// resource until the process restarts.
func TestRefreshKeepsAndRecoversUnobservedInstances(t *testing.T) {
	h := refreshFixture(t)

	if rec := postRefresh(t, h); rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	var snap shared.NormalizedInventory
	rec := getInventory(t, h, "month", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	var hosts, vms []string
	for _, geo := range snap.Geos {
		for _, host := range geo.Hosts {
			hosts = append(hosts, host.ID+"="+host.ObservationState)
			if host.ObservationState == "retained" && host.LastSeen == nil {
				t.Errorf("retained host %s carries no last_seen, so the card cannot say when it was last seen", host.ID)
			}
		}
		for _, vm := range geo.VirtualMachines {
			vms = append(vms, vm.HostID+"/"+vm.Name+"="+vm.ObservationState)
			if vm.HostID == "gone" && vm.LastSeen == nil {
				t.Error("retained resource carries no last_seen")
			}
		}
	}

	// "gone" is known to the index and forgotten by Prometheus; "departed" is
	// the other way round. Both are retained, and neither displaces the host
	// still reporting.
	if want := []string{"departed=retained", "gone=retained", "live=current"}; !equalStringSlices(hosts, want) {
		t.Errorf("hosts after refresh = %v, want %v", hosts, want)
	}
	if want := []string{"gone/vm-1=retained"}; !equalStringSlices(vms, want) {
		t.Errorf("VMs after refresh = %v, want %v", vms, want)
	}
}

// equalStringSlices compares without caring about order, since the payload is
// grouped by geo and the fixture puts both hosts in one.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

func TestHandleInventoryEtagPerWindow(t *testing.T) {
	h := viewFixture(t)

	nowRec := getInventory(t, h, "", nil)
	monthRec := getInventory(t, h, "month", nil)

	if nowRec.Header().Get("ETag") == monthRec.Header().Get("ETag") {
		t.Fatal("both windows share an ETag; a cached body would be served for the wrong view")
	}
	if nowRec.Header().Get("ETag") == "" {
		t.Fatal("no ETag on the response")
	}
}

func TestHandleInventoryHonoursIfNoneMatch(t *testing.T) {
	h := viewFixture(t)

	first := getInventory(t, h, "month", nil)
	etag := first.Header().Get("ETag")

	second := getInventory(t, h, "month", map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304 for a matching ETag", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried %d bytes of body", second.Body.Len())
	}

	// The validator of one window must not validate another.
	crossed := getInventory(t, h, "", map[string]string{"If-None-Match": etag})
	if crossed.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when the validator belongs to another window", crossed.Code)
	}
}
