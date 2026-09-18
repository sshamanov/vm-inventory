package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vm-inventory/internal/backend"
	"vm-inventory/internal/backend/api"
	"vm-inventory/internal/backend/confluence"
	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/prometheus"
	"vm-inventory/internal/backend/state"
	"vm-inventory/internal/shared"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := backend.LoadConfig()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	promClient, err := prometheus.NewClient(cfg.PrometheusURL)
	if err != nil {
		logger.Error("failed to create Prometheus client", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !promClient.IsAvailable(ctx) {
		logger.Warn("Prometheus is not reachable, starting with empty cache")
	}

	stateStore := state.NewStore()

	obsIndex := index.NewObservationIndex()

	if promClient.IsAvailable(context.Background()) {
		logger.Info("rebuilding observation index from Prometheus history")
		qr, err := promClient.QueryInstant(context.Background(), prometheus.QueryAllInventory())
		if err == nil {
			grouped := prometheus.MetricsByName(qr.Data.Result)

			// Disk bytes: pre-aggregate per resource before applying to
			// avoid += accumulation across refresh cycles.
			if diskResults, ok := grouped[shared.MetricResourceDiskBytes]; ok {
				delete(grouped, shared.MetricResourceDiskBytes)
				obsIndex.ApplyDiskBytes(diskResults, time.Now())
			}
			// Host IPs: pre-aggregate and deduplicate to avoid append accumulation.
			if hostIPs, ok := grouped[shared.MetricHostIPInfo]; ok {
				delete(grouped, shared.MetricHostIPInfo)
				obsIndex.ApplyHostIPs(hostIPs, time.Now())
			}
			// Resource IPs: pre-aggregate and deduplicate to avoid append accumulation.
			if resIPs, ok := grouped[shared.MetricResourceIPInfo]; ok {
				delete(grouped, shared.MetricResourceIPInfo)
				obsIndex.ApplyResourceIPs(resIPs, time.Now())
			}

			for _, results := range grouped {
				for _, result := range results {
					processMetricResult(obsIndex, result)
				}
			}
			now := time.Now()
			stateStore.Update(func(st *state.State) (*state.State, error) {
				st.LastSuccessfulRefresh = &now
				return st, nil
			})
			logger.Info("observation index rebuilt",
				"hosts", obsIndex.HostCount(),
				"resources", obsIndex.ResourceCount(),
			)
		} else {
			logger.Error("startup Prometheus query failed, starting with empty index", "error", err)
		}
	}

	var pub *confluence.Publisher
	if cfg.ConfluenceURL != "" && cfg.ConfluenceToken != "" && cfg.ConfluencePageID != "" {
		pub = confluence.NewPublisher(
			cfg.ConfluenceURL, cfg.ConfluenceToken, cfg.ConfluencePageID,
			obsIndex, logger,
		)
		logger.Info("Confluence publisher configured", "page", cfg.ConfluencePageID)
	}

	handler := api.NewHandler(obsIndex, promClient, stateStore, pub, cfg.UIPassword, cfg.ConfluenceURL, logger)
	handler.MarkRefreshed() // set initial cache timestamp after startup rebuild

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "web"
	}
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux}

	// Background refresh context — shared by pruning and refresh goroutines.
	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	defer refreshCancel()

	// Background pruning of stale observations every hour. The window is the
	// retention window, not the UI's: an instance has to survive in the index
	// for as long as the widest view can ask for it.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
			}
			before := obsIndex.HostCount() + obsIndex.ResourceCount()
			obsIndex.Prune(shared.RetentionWindow)
			after := obsIndex.HostCount() + obsIndex.ResourceCount()
			logger.Debug("pruned stale observations", "before", before, "after", after)
		}
	}()

	// Backfill instances that were collected in the past and have since gone
	// away, so the wider views have something to dim. This is several Prometheus
	// subqueries over the retention window; the UI serves from the index while
	// they run and picks up what they find when the rebuild below lands (§15.4).
	// The same pass runs at the end of every refresh, so an instance that departs
	// later is caught by the refresh that follows it rather than only at startup.
	go func() {
		handler.BackfillHistory(refreshCtx)
		handler.MarkRefreshed()
		logger.Info("history backfill complete",
			"hosts", obsIndex.HostCount(), "resources", obsIndex.ResourceCount(),
		)
	}()

	// Background refresh loop with graceful shutdown (§14.5).
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
			}
			qr, err := promClient.QueryInstant(context.Background(), prometheus.QueryAllInventory())
			if err != nil {
				logger.Warn("background refresh failed", "error", err)
				continue
			}
			grouped := prometheus.MetricsByName(qr.Data.Result)

			// Reset detail fields to prevent stale entries from persisting
			// across refresh cycles (BlockDevices, Filesystems, StoragePools).
			obsIndex.ResetHostDetailFields()

			// Disk bytes: pre-aggregate per resource before applying to
			// avoid += accumulation across refresh cycles.
			if diskResults, ok := grouped[shared.MetricResourceDiskBytes]; ok {
				delete(grouped, shared.MetricResourceDiskBytes)
				obsIndex.ApplyDiskBytes(diskResults, time.Now())
			}
			// Host IPs: pre-aggregate and deduplicate to avoid append accumulation.
			if hostIPs, ok := grouped[shared.MetricHostIPInfo]; ok {
				delete(grouped, shared.MetricHostIPInfo)
				obsIndex.ApplyHostIPs(hostIPs, time.Now())
			}
			// Resource IPs: pre-aggregate and deduplicate to avoid append accumulation.
			if resIPs, ok := grouped[shared.MetricResourceIPInfo]; ok {
				delete(grouped, shared.MetricResourceIPInfo)
				obsIndex.ApplyResourceIPs(resIPs, time.Now())
			}

			for _, results := range grouped {
				for _, result := range results {
					processMetricResult(obsIndex, result)
				}
			}
			logger.Debug("background refresh complete",
				"hosts", obsIndex.HostCount(), "resources", obsIndex.ResourceCount(),
			)
			now := time.Now()
			stateStore.Update(func(st *state.State) (*state.State, error) {
				st.LastSuccessfulRefresh = &now
				return st, nil
			})

			// The query above sees only what is being scraped now, so a host
			// that has gone away since the last cycle is found only here. Same
			// pass as the button runs, so both triggers recover a departure
			// (§15.4).
			handler.BackfillHistory(refreshCtx)
			handler.MarkRefreshed()
		}
	}()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down")
		refreshCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	logger.Info("backend starting", "listen", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func processMetricResult(obsIndex *index.ObservationIndex, result prometheus.MetricResult) {
	name := result.Metric["__name__"]
	now := time.Now()
	hostID := result.Metric["host_id"]
	inventoryID := result.Metric["inventory_id"]

	switch name {
	case "inventory_host_info":
		rec := prometheus.DecodeHostInfo(result)
		obsIndex.UpsertHost(rec, now)
	case "inventory_host_cpu_info":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.CPUModel = result.Metric["model"]
		}, now)
	case "inventory_host_cpu_sockets":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.CPUSockets = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_cpu_cores":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.CPUCores = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_cpu_threads":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.CPUThreads = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_cpu_usage_ratio":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.CPUUsage = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_memory_total_bytes":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.MemoryTotal = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_memory_available_bytes":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.MemoryAvail = prometheus.ParseValue(result)
		}, now)
	case "inventory_resource_info":
		rec := prometheus.DecodeResourceInfo(result)
		obsIndex.UpsertResource(rec, now)
	case "inventory_resource_cpu_count":
		obsIndex.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.CPUCount = prometheus.ParseValue(result)
		}, now)
	case "inventory_resource_memory_bytes":
		obsIndex.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.MemoryBytes = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_block_device_info", "inventory_host_block_device_bytes":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.MergeBlockDevice(prometheus.DecodeBlockDevice(result, name))
		}, now)
	case "inventory_host_filesystem_info", "inventory_host_filesystem_total_bytes",
		"inventory_host_filesystem_available_bytes", "inventory_host_filesystem_mount_info":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.MergeFilesystem(prometheus.DecodeFilesystem(result, name))
		}, now)
	case "inventory_host_storage_pool_info", "inventory_host_storage_pool_total_bytes",
		"inventory_host_storage_pool_available_bytes":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.MergeStoragePool(prometheus.DecodeStoragePool(result, name))
		}, now)
	case "inventory_host_hugepages_total_bytes":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.InitMaps()
			h.HugepagesTotal[result.Metric["page_size_bytes"]] = prometheus.ParseValue(result)
		}, now)
	case "inventory_host_hugepages_free_bytes":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.InitMaps()
			h.HugepagesFree[result.Metric["page_size_bytes"]] = prometheus.ParseValue(result)
		}, now)
	}
}
