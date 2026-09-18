package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"vm-inventory/internal/backend/confluence"
	"vm-inventory/internal/backend/history"
	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/normalizer"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/backend/state"
	"vm-inventory/internal/shared"
	"vm-inventory/internal/version"
)

// cachedView is one rendered view window. Each window needs its own body and
// its own validator: the switch asks for now/month/all, and a browser that
// cached the month view would otherwise be served it for the now view.
type cachedView struct {
	snapshot []byte
	etag     string
}

// Handler serves the inventory HTTP API (§17).
type Handler struct {
	idx           *index.ObservationIndex
	normalizer    *normalizer.Normalizer
	promClient    *prometheus.Client
	stateStore    *state.Store
	publisher     *confluence.Publisher // nil if Confluence not configured
	uiPassword    string                // simple password gate; empty = no auth required
	confluenceURL string
	snapshotMu    sync.Mutex // serializes refresh
	publishMu     sync.Mutex // serializes publication
	historyMu     sync.Mutex // serializes the history backfill
	views         map[string]cachedView
	cacheMu       sync.RWMutex
	lastRefresh   time.Time
	logger        *slog.Logger
}

// NewHandler creates a new API handler. publisher may be nil if Confluence is not configured.
func NewHandler(
	idx *index.ObservationIndex,
	promClient *prometheus.Client,
	stateStore *state.Store,
	publisher *confluence.Publisher,
	uiPassword, confluenceURL string,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		idx:           idx,
		normalizer:    normalizer.New(idx),
		promClient:    promClient,
		stateStore:    stateStore,
		publisher:     publisher,
		uiPassword:    uiPassword,
		confluenceURL: confluenceURL,
		views:         make(map[string]cachedView),
		logger:        logger,
	}
}

// MarkRefreshed updates the cache timestamp after a background refresh.
func (h *Handler) MarkRefreshed() {
	h.rebuildViews()
	h.lastRefresh = time.Now()
}

// BackfillHistory asks Prometheus which instances were collected at some point
// in the retention window and merges the ones that have gone away into the
// index (§15.4). The live refresh cannot find them — an instant query sees only
// what is scraped now — so this is the half of a refresh that remembers
// departures, and without it the wider views have nothing to dim and a host that
// stops reporting is simply absent everywhere.
//
// It runs on every refresh trigger: at startup, on the periodic cycle, and on
// the button. A second caller arriving while one pass is running returns at
// once, since a pass covers the whole window and the index merges by last-seen.
// The caller rebuilds the views afterwards (MarkRefreshed).
func (h *Handler) BackfillHistory(ctx context.Context) {
	if !h.historyMu.TryLock() {
		return
	}
	defer h.historyMu.Unlock()

	history.Backfill(ctx, h.promClient, h.idx, h.logger)
}

// RegisterRoutes registers all HTTP routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/inventory", h.handleInventory)
	mux.HandleFunc("/api/status", h.handleStatus)
	mux.HandleFunc("/api/auth", h.handleAuth)
	mux.HandleFunc("/api/refresh", h.handleRefresh)
	mux.HandleFunc("/api/confluence/publish", h.handlePublish)
}

// GET /api/inventory (§17.2) — returns normalized JSON with ETag support (§17.6).
// The optional ?window= parameter selects the view window (§15.1).
func (h *Handler) handleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkAuth(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	name := r.URL.Query().Get("window")
	if name == "" {
		name = shared.UIWindowNames[0]
	}
	window, ok := shared.ParseUIWindow(name)
	if !ok {
		http.Error(w, `{"error":"unknown_window"}`, http.StatusBadRequest)
		return
	}

	// Use the cached rendering if available; rebuild only when it is missing.
	h.cacheMu.RLock()
	view, cached := h.views[name]
	h.cacheMu.RUnlock()

	if !cached {
		view = h.rebuildView(name, window)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", view.etag)

	if match := r.Header.Get("If-None-Match"); match == view.etag && view.etag != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Write(view.snapshot)
}

// rebuildViews re-renders every window. A refresh changes what all of them
// hold, so they are rebuilt together rather than left to go stale until
// somebody happens to ask for them.
func (h *Handler) rebuildViews() {
	for _, name := range shared.UIWindowNames {
		if window, ok := shared.ParseUIWindow(name); ok {
			h.rebuildView(name, window)
		}
	}
}

func (h *Handler) rebuildView(name string, window time.Duration) cachedView {
	data, err := json.Marshal(h.normalizer.BuildUISnapshot(window))
	if err != nil {
		h.logger.Error("failed to marshal inventory snapshot", "window", name, "error", err)
		return cachedView{}
	}
	view := cachedView{
		snapshot: data,
		etag:     fmt.Sprintf(`"%x"`, sha256.Sum256(data)),
	}

	h.cacheMu.Lock()
	h.views[name] = view
	h.cacheMu.Unlock()

	return view
}

// GET /api/status (§17.3) — returns cache and publication status.
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	st := h.stateStore.Load()
	cacheTime := h.lastRefresh
	if cacheTime.IsZero() {
		cacheTime = time.Now().UTC()
	}

	status := map[string]interface{}{
		"version":                 version.Version,
		"commit":                  version.Commit,
		"cache_generated_at":      cacheTime.Format(time.RFC3339),
		"last_prometheus_refresh": nil,
		"last_refresh_status":     "ok",
		"last_confluence_update":  nil,
		"last_confluence_status":  st.LastConfluenceStatus,
		"confluence_url":          h.confluenceURL,
		"host_count":              h.idx.HostCount(),
		"resource_count":          h.idx.ResourceCount(),
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

	// Re-read what Prometheus is scraping now. Nothing is cleared first: an
	// instant query cannot see an instance that has stopped being scraped, so
	// emptying the index would retire every departed host and resource. The
	// history pass below is what finds departures; this one only refreshes what
	// is still reporting. Discarding the detail fields that are merged rather
	// than assigned keeps the parts of the index that are rebuilt from live
	// series from going stale.
	h.idx.ResetHostDetailFields()
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

	// Disk bytes: pre-aggregate per resource before applying to
	// avoid += accumulation across refresh cycles.
	if diskResults, ok := grouped[shared.MetricResourceDiskBytes]; ok {
		delete(grouped, shared.MetricResourceDiskBytes)
		h.idx.ApplyDiskBytes(diskResults, time.Now())
	}
	// Host IPs: pre-aggregate and deduplicate to avoid append accumulation.
	if hostIPs, ok := grouped[shared.MetricHostIPInfo]; ok {
		delete(grouped, shared.MetricHostIPInfo)
		h.idx.ApplyHostIPs(hostIPs, time.Now())
	}
	// Resource IPs: pre-aggregate and deduplicate to avoid append accumulation.
	if resIPs, ok := grouped[shared.MetricResourceIPInfo]; ok {
		delete(grouped, shared.MetricResourceIPInfo)
		h.idx.ApplyResourceIPs(resIPs, time.Now())
	}

	for _, results := range grouped {
		for _, result := range results {
			h.processMetricResult(result, time.Now())
		}
	}

	// Record the successful refresh for the status endpoint.
	h.stateStore.Update(func(st *state.State) (*state.State, error) {
		now := time.Now()
		st.LastSuccessfulRefresh = &now
		return st, nil
	})

	// Recheck the history too, for the same reason the background cycle does: a
	// button pressed after a host went away has to bring it back as retained.
	// The caller waits for the pass, so that a successful refresh means the
	// snapshot the UI reloads next is already complete.
	h.BackfillHistory(ctx)

	// Rebuild cached snapshots after refresh.
	h.rebuildViews()
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

	if h.publisher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "confluence_not_configured",
		})
		return
	}

	// Check Prometheus availability (§19.3).
	if !h.promClient.IsAvailable(r.Context()) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "prometheus_unavailable_publish_blocked",
		})
		return
	}

	result := h.publisher.Publish(r.Context())

	// Record the publication result for the status endpoint. It is process-local
	// (§20): the page itself carries what was published.
	h.stateStore.Update(func(st *state.State) (*state.State, error) {
		now := time.Now()
		st.LastConfluenceUpdate = &now
		st.LastConfluenceStatus = string(result)
		return st, nil
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": string(result)})
}

// POST /api/auth — simple password gate. Returns 200 + token on success.
func (h *Handler) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.uiPassword == "" {
		writeJSON(w, http.StatusOK, map[string]string{"token": ""})
		return
	}
	var body struct{ Password string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password != h.uiPassword {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong_password"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": h.uiPassword})
}

// checkAuth returns true if no password is required or the request has a valid token.
func (h *Handler) checkAuth(r *http.Request) bool {
	if h.uiPassword == "" {
		return true
	}
	return r.Header.Get("X-Auth") == h.uiPassword
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
