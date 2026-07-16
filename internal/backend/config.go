package backend

import (
	"fmt"
	"os"
)

// Config holds backend configuration from environment variables.
type Config struct {
	PrometheusURL      string
	ConfluenceURL      string
	ConfluenceUsername string
	ConfluencePassword string
	ConfluenceSpaceKey string
	ListenAddr         string
	StatePath          string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr: "0.0.0.0:8080",
		StatePath:  "/data/state.json",
	}

	cfg.PrometheusURL = os.Getenv("PROMETHEUS_URL")
	if cfg.PrometheusURL == "" {
		return nil, fmt.Errorf("PROMETHEUS_URL is required")
	}

	cfg.ConfluenceURL = os.Getenv("CONFLUENCE_URL")
	cfg.ConfluenceUsername = os.Getenv("CONFLUENCE_USERNAME")
	cfg.ConfluencePassword = os.Getenv("CONFLUENCE_PASSWORD")
	cfg.ConfluenceSpaceKey = os.Getenv("CONFLUENCE_SPACE_KEY")

	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		cfg.ListenAddr = addr
	}
	if path := os.Getenv("STATE_PATH"); path != "" {
		cfg.StatePath = path
	}

	return cfg, nil
}
