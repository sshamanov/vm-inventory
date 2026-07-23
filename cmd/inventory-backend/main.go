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

	stateStore := state.NewStore(cfg.StatePath)
	if _, err := stateStore.Load(); err != nil {
		logger.Warn("failed to load state, starting fresh", "error", err)
	}

	obsIndex := index.NewObservationIndex()

	if promClient.IsAvailable(context.Background()) {
		logger.Info("rebuilding observation index from Prometheus history")
		qr, err := promClient.QueryInstant(context.Background(), prometheus.QueryAllInventory())
		if err == nil {
			grouped := prometheus.MetricsByName(qr.Data.Result)

			// Disk bytes: pre-aggregate per resource before applying to
			// avoid += accumulation across refresh cycles.
			if diskResults, ok := grouped["inventory_resource_disk_bytes"]; ok {
				delete(grouped, "inventory_resource_disk_bytes")
				obsIndex.ApplyDiskBytes(diskResults, time.Now())
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

	// Background pruning of stale observations every hour.
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
			obsIndex.Prune(24 * time.Hour)
			after := obsIndex.HostCount() + obsIndex.ResourceCount()
			logger.Debug("pruned stale observations", "before", before, "after", after)
		}
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

			// Disk bytes: pre-aggregate per resource before applying to
			// avoid += accumulation across refresh cycles.
			if diskResults, ok := grouped["inventory_resource_disk_bytes"]; ok {
				delete(grouped, "inventory_resource_disk_bytes")
				obsIndex.ApplyDiskBytes(diskResults, time.Now())
			}

			for _, results := range grouped {
				for _, result := range results {
					processMetricResult(obsIndex, result)
				}
			}
			logger.Debug("background refresh complete",
				"hosts", obsIndex.HostCount(), "resources", obsIndex.ResourceCount(),
			)
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
	case "inventory_host_ip_info":
		obsIndex.UpdateHostField(hostID, func(h *index.HostObservation) {
			h.IPs = append(h.IPs, prometheus.DecodeHostIP(result))
		}, now)
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
	case "inventory_resource_ip_info":
		obsIndex.UpdateResourceField(inventoryID, func(r *index.ResourceObservation) {
			r.IPs = append(r.IPs, prometheus.HostIPRecord{
				Address: result.Metric["address"],
				Family:  result.Metric["family"],
			})
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
