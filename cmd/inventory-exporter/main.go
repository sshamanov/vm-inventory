package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"vm-inventory/internal/exporter"
	"vm-inventory/internal/exporter/linux"
	"vm-inventory/internal/version"
)

// multiGatherer combines multiple prometheus.Gatherer sources.
type multiGatherer struct {
	mu        sync.RWMutex
	gatherers map[string]prometheus.Gatherer
}

func newMultiGatherer() *multiGatherer {
	return &multiGatherer{gatherers: make(map[string]prometheus.Gatherer)}
}

func (mg *multiGatherer) set(name string, g prometheus.Gatherer) {
	mg.mu.Lock()
	defer mg.mu.Unlock()
	mg.gatherers[name] = g
}

func (mg *multiGatherer) Gather() ([]*dto.MetricFamily, error) {
	mg.mu.RLock()
	defer mg.mu.RUnlock()
	var all []*dto.MetricFamily
	for _, g := range mg.gatherers {
		mfs, err := g.Gather()
		if err != nil {
			continue
		}
		all = append(all, mfs...)
	}
	return all, nil
}

func main() {
	configFile := flag.String("config.file", "", "Path to configuration file (optional)")
	listenAddr := flag.String("web.listen-address", ":9171", "Address to listen on for HTTP requests")
	versionFlag := flag.Bool("version", false, "Show version and exit")
	flag.BoolVar(versionFlag, "v", false, "Show version and exit")

	flag.Parse()

	if *versionFlag {
		fmt.Printf("inventory-exporter %s (commit %s)\n", version.Version, version.Commit)
		os.Exit(0)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := defaultConfig()
	if *configFile != "" {
		if loaded, err := exporter.LoadConfig(*configFile); err != nil {
			logger.Warn("failed to load config, using defaults", "error", err)
		} else {
			cfg = loaded
		}
	}

	logger.Info("exporter starting", "mode", cfg.Mode)

	var collectors []exporter.Collector
	var exporterHost string

	switch cfg.Mode {
	case exporter.ModeLinux:
		exporterHost = cfg.Host.ID
		collectors = append(collectors,
			linux.NewHostCollector(cfg.Host.ID, cfg.Host.Description, cfg.Host.Geo),
		)
		// Libvirt and LXD are enabled by default when unconfigured (§7.1).
		if cfg.Collectors == nil || cfg.Collectors.Libvirt.IsEnabled() {
			conn, err := linux.NewLibvirtConnection()
			if err != nil {
				logger.Warn("libvirt collector disabled", "error", err)
			} else {
				collectors = append(collectors, linux.NewLibvirtCollector(cfg.Host.ID, conn))
			}
		}
		if cfg.Collectors != nil && cfg.Collectors.LXD != nil && !cfg.Collectors.LXD.IsEnabled() {
			// LXD opt-out only when explicitly disabled.
		} else {
			logger.Info("LXD collector enabled (not yet implemented)")
		}

	case exporter.ModeESXi:
		logger.Info("ESXi mode not yet implemented")
		os.Exit(1)
	}

	if exporterHost == "" {
		exporterHost = "unknown"
	}

	snapMgr := exporter.NewSnapshotManager(exporterHost)
	gatherer := newMultiGatherer()

	// Start background collection for each collector.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, col := range collectors {
		c := col
		// Each collector gets its own AtomicGatherer.
		colGatherer := exporter.NewAtomicGatherer()
		gatherer.set(c.Name(), colGatherer)

		go func() {
			ticker := time.NewTicker(cfg.Collection.Interval)
			defer ticker.Stop()

			// Run first collection immediately.
			runCollection(ctx, c, cfg, snapMgr, colGatherer, logger)

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					runCollection(ctx, c, cfg, snapMgr, colGatherer, logger)
				}
			}
		}()
	}

	// Expose version info as a metric.
	versionReg := prometheus.NewRegistry()
	versionReg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "inventory_exporter_info",
		Help: "Exporter version and commit information.",
		ConstLabels: map[string]string{
			"version": version.Version,
			"commit":  version.Commit,
		},
	}, func() float64 { return 1 }))
	gatherer.set("version", versionReg)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK\n"))
	})

	server := &http.Server{
		Addr:    *listenAddr,
		Handler: mux,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		logger.Info("shutting down")
		cancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "address", *listenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func defaultConfig() *exporter.Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return &exporter.Config{
		Mode: exporter.ModeLinux,
		Host: &exporter.HostConfig{
			ID:  hostname,
			Geo: "general",
		},
		Collection: exporter.CollectionConfig{
			Interval:       15 * time.Minute,
			Timeout:        5 * time.Minute,
			MaxSnapshotAge: 8 * time.Hour,
		},
	}
}

func runCollection(
	ctx context.Context,
	col exporter.Collector,
	cfg *exporter.Config,
	mgr *exporter.SnapshotManager,
	gatherer *exporter.AtomicGatherer,
	logger *slog.Logger,
) {
	name := col.Name()
	sourceHost := ""
	if cfg.Mode == exporter.ModeLinux && cfg.Host != nil {
		sourceHost = cfg.Host.ID
	}

	if !mgr.TryCollect(name) {
		logger.Debug("skipping overlapping collection", "collector", name)
		return
	}

	collectCtx, cancel := context.WithTimeout(ctx, cfg.Collection.Timeout)
	defer cancel()

	result, err := col.Collect(collectCtx)
	if err != nil {
		logger.Error("collection failed", "collector", name, "error", err)
		mgr.FailSnapshot(name, sourceHost)
		return
	}

	if err := mgr.CommitSnapshot(name, sourceHost, result, cfg.Collection.MaxSnapshotAge); err != nil {
		logger.Error("failed to commit snapshot", "collector", name, "error", err)
		mgr.FailSnapshot(name, sourceHost)
		return
	}

	// Atomically swap the gatherer's registry.
	reg, _ := mgr.GetSnapshot(name)
	if reg != nil {
		gatherer.Swap(reg)
	}

	logger.Info("collection complete", "collector", name, "metrics", len(result.Metrics))
}
