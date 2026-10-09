package confluence

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/normalizer"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/shared"
)

func TestFormatText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"missing value", "", "—"},
		{"plain text", "build node", "build node"},
		{"ampersand", "R&D builder", "R&amp;D builder"},
		{"markup", "<b>x</b>", "&lt;b&gt;x&lt;/b&gt;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatText(tt.in); got != tt.want {
				t.Errorf("formatText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The page title and its heading are the same document name, and both are
// fixed by §19.1 and DESIGN §20. They drifted apart once; keep them together.
func TestRenderStorageFormatUsesTheDocumentedTitle(t *testing.T) {
	if pageTitle != "VM Inventory" {
		t.Errorf("pageTitle = %q, want %q", pageTitle, "VM Inventory")
	}
	if body := renderStorageFormat(&shared.NormalizedInventory{}); !strings.Contains(body, "<h1>VM Inventory</h1>") {
		t.Error("rendered body does not open with the documented heading")
	}
}

// The description is the last column of both resource tables, so a cell that
// ends a row ends in that column's value.
func TestRenderStorageFormatPutsDescriptionLast(t *testing.T) {
	snapshot := &shared.NormalizedInventory{
		Geos: []shared.Geo{{
			Name:            "denver",
			VirtualMachines: []shared.VMResource{{HostID: "hv1", Name: "vm-a", Description: "R&D builder"}},
			LXDContainers:   []shared.LXDContainer{{HostID: "hv1", Name: "ct-a"}},
		}},
	}

	body := renderStorageFormat(snapshot)

	if got := strings.Count(body, "<th>Description</th></tr>"); got != 2 {
		t.Errorf("resource tables ending in a Description header = %d, want 2", got)
	}
	if !strings.Contains(body, "<td>R&amp;D builder</td></tr>") {
		t.Error("VM row does not end with its escaped description")
	}
	if !strings.Contains(body, "<td>—</td></tr>") {
		t.Error("container row does not end with the missing-value dash")
	}
}

// --- Publication against a Confluence that records what it was sent ---

// fakeConfluence is a Confluence that answers the four calls publication makes
// and remembers what it was given.
type fakeConfluence struct {
	pageVersion int    // version the page currently reports
	storedHash  string // hash held in the inventory-hash property, "" = absent
	propVersion int    // version of that property

	pageWrites  int
	propWrites  int
	lastPage    string // body of the last page write
	lastHash    string // hash of the last property write
	writtenProp int    // property version of the last property write

	failVersion int // status to answer the page read with (0 = 200)
	failPage    int // status to answer the page write with (0 = 200)
	failPropGet int // status to answer the property read with (0 = 200)
	failPropPut int // status to answer the property write with (0 = 200)
}

func (f *fakeConfluence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	isProperty := strings.HasSuffix(path, "/property/"+hashPropertyKey)

	switch {
	case isProperty && r.Method == http.MethodGet:
		if f.failPropGet != 0 {
			w.WriteHeader(f.failPropGet)
			return
		}
		if f.storedHash == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"key":     hashPropertyKey,
			"value":   map[string]string{"hash": f.storedHash},
			"version": map[string]int{"number": f.propVersion},
		})

	case isProperty && r.Method == http.MethodPut:
		if f.failPropPut != 0 {
			w.WriteHeader(f.failPropPut)
			return
		}
		var payload struct {
			Value   map[string]string `json:"value"`
			Version struct {
				Number int `json:"number"`
			} `json:"version"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		f.propWrites++
		f.lastHash = payload.Value["hash"]
		f.writtenProp = payload.Version.Number
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodGet:
		if f.failVersion != 0 {
			w.WriteHeader(f.failVersion)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"version": map[string]int{"number": f.pageVersion},
		})

	case r.Method == http.MethodPut:
		if f.failPage != 0 {
			w.WriteHeader(f.failPage)
			return
		}
		var payload struct {
			Body struct {
				Storage struct {
					Value string `json:"value"`
				} `json:"storage"`
			} `json:"body"`
			Version struct {
				Number int `json:"number"`
			} `json:"version"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		f.pageWrites++
		f.lastPage = payload.Body.Storage.Value
		f.pageVersion = payload.Version.Number
		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// newTestPublisher wires a publisher to a fake Confluence holding one VM, and
// returns the fake and the body that publisher will render.
func newTestPublisher(t *testing.T, fake *fakeConfluence) (*Publisher, string) {
	t.Helper()

	idx := index.NewObservationIndex()
	now := time.Now()
	idx.UpsertHost(prometheus.HostInfoRecord{HostID: "hv1", Geo: "denver", Platform: "linux"}, now)
	idx.UpsertResource(prometheus.ResourceInfoRecord{
		InventoryID: "hv1:libvirt_vm:uuid1",
		HostID:      "hv1",
		Name:        "vm-a",
		Kind:        shared.KindLibvirtVM,
		Description: "build node",
	}, now)

	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := NewPublisher(server.URL, "token", "12345", idx, logger)
	return pub, renderStorageFormat(normalizer.New(idx).BuildConfluenceSnapshot())
}

// The common case: the page already holds this inventory. Nothing may be
// written, because every write bumps the page version and notifies watchers.
func TestPublishUnchangedWritesNothing(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 7, propVersion: 3}
	pub, body := newTestPublisher(t, fake)
	fake.storedHash = contentHash(body)

	if got := pub.Publish(context.Background()); got != Unchanged {
		t.Errorf("Publish = %q, want %q", got, Unchanged)
	}
	if fake.pageWrites != 0 || fake.propWrites != 0 {
		t.Errorf("writes = %d page, %d property, want none", fake.pageWrites, fake.propWrites)
	}
	if fake.pageVersion != 7 {
		t.Errorf("page version = %d, want it untouched at 7", fake.pageVersion)
	}
}

// A page this application has never published has no property to read, which is
// not a difference in content but does mean the page has to be written.
func TestPublishWritesPageAndRecordsHashWhenPropertyAbsent(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 4}
	pub, body := newTestPublisher(t, fake)

	if got := pub.Publish(context.Background()); got != Published {
		t.Fatalf("Publish = %q, want %q", got, Published)
	}
	if fake.pageWrites != 1 {
		t.Errorf("page writes = %d, want 1", fake.pageWrites)
	}
	if fake.pageVersion != 5 {
		t.Errorf("page version = %d, want the successor 5", fake.pageVersion)
	}
	if fake.lastPage != body {
		t.Error("published body is not the rendered body")
	}
	if fake.propWrites != 1 {
		t.Fatalf("property writes = %d, want 1", fake.propWrites)
	}
	if fake.lastHash != contentHash(body) {
		t.Errorf("recorded hash = %q, want the rendered body's hash", fake.lastHash)
	}
	if fake.writtenProp != 1 {
		t.Errorf("property version = %d, want a fresh property at 1", fake.writtenProp)
	}
}

func TestPublishWritesPageWhenStoredHashDiffers(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 4, storedHash: "deadbeef", propVersion: 3}
	pub, body := newTestPublisher(t, fake)

	if got := pub.Publish(context.Background()); got != Published {
		t.Fatalf("Publish = %q, want %q", got, Published)
	}
	if fake.lastHash != contentHash(body) {
		t.Errorf("recorded hash = %q, want the rendered body's hash", fake.lastHash)
	}
	if fake.writtenProp != 4 {
		t.Errorf("property version = %d, want the successor 4", fake.writtenProp)
	}
}

// Bookkeeping that cannot be read must not stop the page from being published:
// treating it as absent costs one redundant write, refusing costs the update.
func TestPublishTreatsUnreadablePropertyAsAbsent(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 4, failPropGet: http.StatusForbidden}
	pub, _ := newTestPublisher(t, fake)

	if got := pub.Publish(context.Background()); got != Published {
		t.Errorf("Publish = %q, want the unreadable property to be disregarded", got)
	}
	if fake.pageWrites != 1 {
		t.Errorf("page writes = %d, want 1", fake.pageWrites)
	}
}

func TestPublishFailsWithoutWritingWhenThePageCannotBeRead(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 4, failVersion: http.StatusInternalServerError}
	pub, _ := newTestPublisher(t, fake)

	if got := pub.Publish(context.Background()); got != Failed {
		t.Errorf("Publish = %q, want %q", got, Failed)
	}
	if fake.pageWrites != 0 || fake.propWrites != 0 {
		t.Errorf("writes = %d page, %d property, want none", fake.pageWrites, fake.propWrites)
	}
}

func TestPublishFailsWhenPageWriteFails(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 4, failPage: http.StatusConflict}
	pub, _ := newTestPublisher(t, fake)

	if got := pub.Publish(context.Background()); got != Failed {
		t.Errorf("Publish = %q, want %q", got, Failed)
	}
	if fake.pageWrites != 0 {
		t.Errorf("page writes = %d, want 0", fake.pageWrites)
	}
}

// The page carries the content and the property only records it: a property
// write that fails is reported, because the record is incomplete, but the
// published content is never rolled back.
func TestPublishFailsWhenHashCannotBeRecorded(t *testing.T) {
	fake := &fakeConfluence{pageVersion: 4, failPropPut: http.StatusInternalServerError}
	pub, _ := newTestPublisher(t, fake)

	if got := pub.Publish(context.Background()); got != Failed {
		t.Errorf("Publish = %q, want %q", got, Failed)
	}
}

// The hash must follow the rendered page, not the snapshot: a renderer change
// is a change to what readers see and has to reach them.
func TestContentHashFollowsTheRenderedBody(t *testing.T) {
	base := &shared.NormalizedInventory{
		Geos: []shared.Geo{{
			Name:            "denver",
			VirtualMachines: []shared.VMResource{{HostID: "hv1", Name: "vm-a", Description: "build node"}},
		}},
	}
	same := &shared.NormalizedInventory{
		Geos: []shared.Geo{{
			Name:            "denver",
			VirtualMachines: []shared.VMResource{{HostID: "hv1", Name: "vm-a", Description: "build node"}},
		}},
	}
	edited := &shared.NormalizedInventory{
		Geos: []shared.Geo{{
			Name:            "denver",
			VirtualMachines: []shared.VMResource{{HostID: "hv1", Name: "vm-a", Description: "build node 2"}},
		}},
	}

	if contentHash(renderStorageFormat(base)) != contentHash(renderStorageFormat(same)) {
		t.Error("identical inventories hashed differently")
	}
	if contentHash(renderStorageFormat(base)) == contentHash(renderStorageFormat(edited)) {
		t.Error("a changed description did not change the hash")
	}
}
