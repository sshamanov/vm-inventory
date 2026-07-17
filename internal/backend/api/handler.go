package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/normalizer"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/backend/state"
	"vm-inventory/internal/version"
)

// Handler serves the inventory HTTP API (§17).
type Handler struct {
	idx            *index.ObservationIndex
	normalizer     *normalizer.Normalizer
	promClient     *prometheus.Client
	stateStore     *state.Store
	snapshotMu     sync.Mutex // serializes refresh
	publishMu      sync.Mutex // serializes publication
	cachedSnapshot []byte
	cachedEtag     string
	cacheMu        sync.RWMutex
	lastRefresh    time.Time
	logger         *slog.Logger
}

// NewHandler creates a new API handler.
func NewHandler(
	idx *index.ObservationIndex,
	promClient *prometheus.Client,
	stateStore *state.Store,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		idx:        idx,
		normalizer: normalizer.New(idx),
		promClient: promClient,
		stateStore: stateStore,
		logger:     logger,
	}
}

// MarkRefreshed updates the cache timestamp after a background refresh.
func (h *Handler) MarkRefreshed() {
	h.rebuildSnapshot()
	h.lastRefresh = time.Now()
}

// RegisterRoutes registers all HTTP routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/inventory", h.handleInventory)
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/refresh", h.handleRefresh)
	mux.HandleFunc("/api/confluence/publish", h.handlePublish)
}

// GET /api/inventory (§17.2) — returns normalized JSON with ETag support (§17.6).
func (h *Handler) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Use cached snapshot if available; rebuild only when stale.
	h.cacheMu.RLock()
	snapshot := h.cachedSnapshot
	etag := h.cachedEtag
	h.cacheMu.RUnlock()

	if snapshot == nil {
		snapshot, etag = h.rebuildSnapshot()
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)

	if match := r.Header.Get("If-None-Match"); match == etag && etag != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Write(snapshot)
}

func (h *Handler) rebuildSnapshot() ([]byte, string) {
	s := h.normalizer.BuildUISnapshot()
	data, err := json.Marshal(s)
	if err != nil {
		return nil, ""
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))

	h.cacheMu.Lock()
	h.cachedSnapshot = data
	h.cachedEtag = etag
	h.cacheMu.Unlock()

	return data, etag
}

// GET /api/status (§17.3) — returns cache and publication status.
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	st, err := h.stateStore.Load()
	if err != nil {
		h.logger.Error("failed to load state", "error", err)
		st = &state.State{SchemaVersion: 1}
	}
		cacheTime := h.lastRefresh
	if cacheTime.IsZero() {
		cacheTime = time.Now().UTC()
	}

	status := map[string]interface{}{
		"version":                   version.Version,
		"commit":                    version.Commit,
		"cache_generated_at":        cacheTime.Format(time.RFC3339),
		"last_prometheus_refresh":   nil,
		"last_refresh_status":       "ok",
		"last_confluence_update":    nil,
		"last_confluence_status":    st.LastConfluenceStatus,
		"host_count":                h.idx.HostCount(),
		"resource_count":            h.idx.ResourceCount(),
	}

	if st.LastSuccessfulRefresh != nil {
		status["last_prometheus_refresh"] = st.LastSuccessfulRefresh.Format(time.RFC3339)
	}
	if st.LastConfluenceUpdate != nil {
		status["last_confluence_update"] = st.LastConfluenceUpdate.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// POST /api/refresh (§17.4) — triggers immediate Prometheus refresh.
func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkMutationAuth(r, w) {
		return
	}

	if !h.snapshotMu.TryLock() {
		http.Error(w, `{"error":"refresh_in_progress"}`, http.StatusConflict)
		return
	}
	defer h.snapshotMu.Unlock()

	// Clear and rebuild observation index from Prometheus.
	h.idx.Clear()
	ctx := r.Context()
	qr, err := h.promClient.QueryInstant(ctx, prometheus.QueryAllInventory())
	if err != nil {
		h.logger.Error("refresh query failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "prometheus_unavailable",
		})
		return
	}

	// Decode results into the observation index.
	grouped := prometheus.MetricsByName(qr.Data.Result)
	for _, results := range grouped {
		for _, result := range results {
			h.processMetricResult(result, time.Now())
		}
	}

	// Persist last successful refresh atomically.
	h.stateStore.Update(func(st *state.State) (*state.State, error) {
		now := time.Now()
		st.LastSuccessfulRefresh = &now
		return st, nil
	})

	// Rebuild cached snapshot after refresh.
	h.rebuildSnapshot()
		h.lastRefresh = time.Now()

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// POST /api/confluence/publish (§17.5) — triggers Confluence publication.
func (h *Handler) handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkMutationAuth(r, w) {
		return
	}

	if !h.publishMu.TryLock() {
		http.Error(w, `{"error":"publish_in_progress"}`, http.StatusConflict)
		return
	}
	defer h.publishMu.Unlock()

	// Check Prometheus availability (§19.3).
	if !h.promClient.IsAvailable(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "prometheus_unavailable_publish_blocked",
		})
		return
	}

	// Confluence publication placeholder — full implementation in Phase 9.
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "published",
		"note":   "confluence publisher not yet wired",
	})
}

// checkMutationAuth enforces same-origin and custom header for mutation endpoints (§21.4).
func (h *Handler) checkMutationAuth(r *http.Request, w http.ResponseWriter) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, `{"error":"content_type_must_be_json"}`, http.StatusUnsupportedMediaType)
		return false
	}
	if r.Header.Get("X-Inventory-Action") == "" {
		http.Error(w, `{"error":"missing_required_header"}`, http.StatusForbidden)
		return false
	}
	return true
}

func (h *Handler) processMetricResult(result prometheus.MetricResult, timestamp time.Time) {
	name := result.Metric["__name__"]
	hostID := result.Metric["host_id"]
	inventoryID := result.Metric["inventory_id"]

	switch name {
	case "inventory_host_info":
		rec := prometheus.DecodeHostInfo(result)
		h.idx.UpsertHost(rec, timestamp)
	case "inventory_host_ip_info":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.IPs = append(ho.IPs, prometheus.DecodeHostIP(result))
		}, timestamp)
	case "inventory_host_cpu_info":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.CPUModel = result.Metric["model"]
		}, timestamp)
	case "inventory_host_cpu_sockets":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.CPUSockets = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_host_cpu_cores":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.CPUCores = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_host_cpu_threads":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.CPUThreads = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_host_cpu_usage_ratio":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.CPUUsage = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_host_memory_total_bytes":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.MemoryTotal = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_host_memory_available_bytes":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.MemoryAvail = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_resource_info":
		rec := prometheus.DecodeResourceInfo(result)
		h.idx.UpsertResource(rec, timestamp)
	case "inventory_resource_cpu_count":
		h.idx.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.CPUCount = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_resource_memory_bytes":
		h.idx.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.MemoryBytes = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_resource_disk_bytes":
		h.idx.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.DiskBytes += prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_resource_ip_info":
		h.idx.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.IPs = append(r.IPs, prometheus.HostIPRecord{
				Address: result.Metric["address"],
				Family:  result.Metric["family"],
			})
		}, timestamp)
	case "inventory_host_block_device_info", "inventory_host_block_device_bytes":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.MergeBlockDevice(prometheus.DecodeBlockDevice(result, name))
		}, timestamp)
	case "inventory_host_filesystem_info", "inventory_host_filesystem_total_bytes",
		"inventory_host_filesystem_available_bytes", "inventory_host_filesystem_mount_info":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.MergeFilesystem(prometheus.DecodeFilesystem(result, name))
		}, timestamp)
	case "inventory_host_storage_pool_info", "inventory_host_storage_pool_total_bytes",
		"inventory_host_storage_pool_available_bytes":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.MergeStoragePool(prometheus.DecodeStoragePool(result, name))
		}, timestamp)
	case "inventory_host_hugepages_total_bytes":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.InitMaps()
			ho.HugepagesTotal[result.Metric["page_size_bytes"]] = prometheus.ParseValue(result)
		}, timestamp)
	case "inventory_host_hugepages_free_bytes":
		h.idx.UpdateHostField(hostID, func(ho *index.HostObservation) {
			ho.InitMaps()
			ho.HugepagesFree[result.Metric["page_size_bytes"]] = prometheus.ParseValue(result)
		}, timestamp)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
