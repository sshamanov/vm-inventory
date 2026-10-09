package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vm-inventory/internal/backend/api"
	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/backend/state"
	"vm-inventory/internal/shared"
)

// TestFullPipeline verifies the end-to-end flow:
// mock Prometheus metrics → backend query/decoder/index → JSON API response.
func TestFullPipeline(t *testing.T) {
	// Set up mock Prometheus server.
	mockProm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return valid Prometheus response with one host.
		response := prometheus.QueryResponse{
			Status: "success",
			Data: prometheus.QueryData{
				ResultType: "vector",
				Result: []prometheus.MetricResult{
					{
						Metric: map[string]string{
							"__name__":      shared.MetricHostInfo,
							shared.LabelHostID:      "hv-kvm-01",
							shared.LabelDescription:  "Main KVM host",
							shared.LabelGeo:          "belgrade",
							shared.LabelPlatform:     shared.PlatformLinux,
							shared.LabelHostname:     "hv-kvm-01",
							shared.LabelOSName:       "Ubuntu",
							shared.LabelOSVersion:    "22.04",
							shared.LabelKernel:       "5.15.0-91-generic",
							shared.LabelArchitecture: "amd64",
						},
						Value: []interface{}{float64(time.Now().Unix()), "1"},
					},
					{
						Metric: map[string]string{
							"__name__":               shared.MetricResourceInfo,
							shared.LabelInventoryID:  "hv-kvm-01:libvirt_vm:test-uuid",
							shared.LabelHostID:       "hv-kvm-01",
							shared.LabelName:         "web-server",
							shared.LabelKind:         shared.KindLibvirtVM,
							shared.LabelDescription:  "Web server VM",
							shared.LabelGuestOS:      "Ubuntu 22.04",
							shared.LabelArchitecture: "x86_64",
						},
						Value: []interface{}{float64(time.Now().Unix()), "1"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer mockProm.Close()

	// Create backend components.
	promClient, err := prometheus.NewClient(mockProm.URL)
	if err != nil {
		t.Fatalf("creating prometheus client: %v", err)
	}

	stateStore := state.NewStore()

	obsIndex := index.NewObservationIndex()

	// Simulate startup rebuild (§14.3).
	qr, err := promClient.QueryInstant(context.Background(), prometheus.QueryAllInventory())
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}

	grouped := prometheus.MetricsByName(qr.Data.Result)
	for _, results := range grouped {
		for _, result := range results {
			name := result.Metric["__name__"]
			switch name {
			case shared.MetricHostInfo:
				rec := prometheus.DecodeHostInfo(result)
				obsIndex.UpsertHost(rec, time.Now())
			case shared.MetricResourceInfo:
				rec := prometheus.DecodeResourceInfo(result)
				obsIndex.UpsertResource(rec, time.Now())
			}
		}
	}

	// Verify index state.
	if obsIndex.HostCount() != 1 {
		t.Errorf("host count = %d, want 1", obsIndex.HostCount())
	}
	if obsIndex.ResourceCount() != 1 {
		t.Errorf("resource count = %d, want 1", obsIndex.ResourceCount())
	}

	// Verify API handler.
	handler := api.NewHandler(obsIndex, promClient, stateStore, nil, "", "", slog.Default())
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Test GET /api/inventory.
	req := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/inventory: status %d", rec.Code)
	}

	var inventory shared.NormalizedInventory
	if err := json.NewDecoder(rec.Body).Decode(&inventory); err != nil {
		t.Fatalf("decoding inventory: %v", err)
	}

	if inventory.SchemaVersion != 1 {
		t.Errorf("schema_version = %d", inventory.SchemaVersion)
	}
	if len(inventory.Geos) != 1 {
		t.Fatalf("geos count = %d, want 1", len(inventory.Geos))
	}
	if inventory.Geos[0].Name != "belgrade" {
		t.Errorf("geo name = %q", inventory.Geos[0].Name)
	}
	if len(inventory.Geos[0].Hosts) != 1 {
		t.Errorf("hosts count = %d, want 1", len(inventory.Geos[0].Hosts))
	}
	if len(inventory.Geos[0].VirtualMachines) != 1 {
		t.Errorf("VMs count = %d, want 1", len(inventory.Geos[0].VirtualMachines))
	}

	// Test GET /api/status.
	req2 := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("GET /api/status: status %d", rec2.Code)
	}
}

// TestETag verifies ETag/304 behavior (§17.6).
func TestETag(t *testing.T) {
	obsIndex := index.NewObservationIndex()
	mockProm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(prometheus.QueryResponse{
			Status: "success",
			Data:   prometheus.QueryData{ResultType: "vector", Result: nil},
		})
	}))
	defer mockProm.Close()

	promClient, _ := prometheus.NewClient(mockProm.URL)
	stateStore := state.NewStore()
	handler := api.NewHandler(obsIndex, promClient, stateStore, nil, "", "", slog.Default())
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// First request — should get ETag.
	req := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag header missing")
	}

	// Second request — should get 304.
	req2 := httptest.NewRequest(http.MethodGet, "/api/inventory", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", rec2.Code)
	}
}

// TestRefreshMutationAuth verifies mutation endpoint security (§21.4).
func TestRefreshMutationAuth(t *testing.T) {
	obsIndex := index.NewObservationIndex()
	mockProm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(prometheus.QueryResponse{
			Status: "success",
			Data:   prometheus.QueryData{ResultType: "vector", Result: nil},
		})
	}))
	defer mockProm.Close()

	promClient, _ := prometheus.NewClient(mockProm.URL)
	stateStore := state.NewStore()
	handler := api.NewHandler(obsIndex, promClient, stateStore, nil, "", "", slog.Default())
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Without custom header — should fail.
	req := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 without header, got %d", rec.Code)
	}

	// With custom header — should succeed.
	req2 := httptest.NewRequest(http.MethodPost, "/api/refresh", nil)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Inventory-Action", "refresh")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 with header, got %d (body: %s)", rec2.Code, rec2.Body.String())
	}
}
