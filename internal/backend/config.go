package backend

import (
	"fmt"
	"os"
)

// Config holds backend configuration from environment variables.
type Config struct {
	PrometheusURL      string
	ConfluenceURL      string
	ConfluenceToken    string // Personal Access Token (Bearer auth)
	ConfluenceSpaceKey string
	UIPassword         string // simple password gate for UI (UI_PASSWORD env)
	ListenAddr         string
	StatePath          string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr: ":8080",
		StatePath:  "/data/state.json",
	}

	cfg.PrometheusURL = os.Getenv("PROMETHEUS_URL")
	if cfg.PrometheusURL == "" {
		return nil, fmt.Errorf("PROMETHEUS_URL is required")
	}

	cfg.ConfluenceURL = os.Getenv("CONFLUENCE_URL")
	cfg.ConfluenceToken = os.Getenv("CONFLUENCE_TOKEN")
	cfg.ConfluenceSpaceKey = os.Getenv("CONFLUENCE_SPACE_KEY")
	cfg.UIPassword = os.Getenv("UI_PASSWORD")

	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		cfg.ListenAddr = addr
	}
	if path := os.Getenv("STATE_PATH"); path != "" {
		cfg.StatePath = path
	}

	return cfg, nil
}
