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

	// Initialize Prometheus client.
	promClient, err := prometheus.NewClient(cfg.PrometheusURL)
	if err != nil {
		logger.Error("failed to create Prometheus client", "error", err)
		os.Exit(1)
	}

	// Check Prometheus connectivity.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !promClient.IsAvailable(ctx) {
		logger.Warn("Prometheus is not reachable, starting with empty cache")
	}

	// Initialize state store.
	stateStore := state.NewStore(cfg.StatePath)
	if _, err := stateStore.Load(); err != nil {
		logger.Warn("failed to load state, starting fresh", "error", err)
	}

	// Initialize observation index.
	obsIndex := index.NewObservationIndex()

	// Try to rebuild from Prometheus history on startup (§14.3).
	if promClient.IsAvailable(context.Background()) {
		logger.Info("rebuilding observation index from Prometheus history")
		qr, err := promClient.QueryInstant(context.Background(), prometheus.QueryAllInventory())
		if err == nil {
			grouped := prometheus.MetricsByName(qr.Data.Result)
			for _, results := range grouped {
				for _, result := range results {
					processMetricResult(obsIndex, result)
				}
			}
			logger.Info("observation index rebuilt",
				"hosts", obsIndex.HostCount(),
				"resources", obsIndex.ResourceCount(),
			)
		}
	}

	// Set up Confluence publisher (if configured).
	var pub *confluence.Publisher
	if cfg.ConfluenceURL != "" && cfg.ConfluenceSpaceKey != "" {
		pub = confluence.NewPublisher(
			cfg.ConfluenceURL,
			cfg.ConfluenceUsername,
			cfg.ConfluencePassword,
			cfg.ConfluenceSpaceKey,
			obsIndex,
			stateStore,
			logger,
		)
		logger.Info("Confluence publisher configured", "space", cfg.ConfluenceSpaceKey)
	}

	// Wire up HTTP API.
	handler := api.NewHandler(obsIndex, promClient, stateStore, logger)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Serve static frontend assets.
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "web"
	}
	mux.Handle("/", http.FileServer(http.Dir(webDir)))

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	// Start background refresh loop (§14.5: 10-minute default).
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			qr, err := promClient.QueryInstant(context.Background(), prometheus.QueryAllInventory())
			if err != nil {
				logger.Warn("background refresh failed", "error", err)
				continue
			}
			grouped := prometheus.MetricsByName(qr.Data.Result)
			for _, results := range grouped {
				for _, result := range results {
					processMetricResult(obsIndex, result)
				}
			}
			logger.Debug("background refresh complete",
				"hosts", obsIndex.HostCount(),
				"resources", obsIndex.ResourceCount(),
			)
		}
	}()

	// Confluence publish hook — accessible via POST to allow scheduled triggers.
	if pub != nil {
		mux.HandleFunc("/api/confluence/publish", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			result := pub.Publish(r.Context())
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"` + string(result) + `"}`))
		})
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down")
		server.Shutdown(context.Background())
	}()

	logger.Info("backend starting", "listen", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func processMetricResult(obsIndex *index.ObservationIndex, result prometheus.MetricResult) {
	name := result.Metric["__name__"]
	switch name {
	case "inventory_host_info":
		rec := prometheus.DecodeHostInfo(result)
		obsIndex.UpsertHost(rec, time.Now())
	case "inventory_resource_info":
		rec := prometheus.DecodeResourceInfo(result)
		obsIndex.UpsertResource(rec, time.Now())
	}
}
