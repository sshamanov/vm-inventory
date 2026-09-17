package backend

import (
	"fmt"
	"os"
)

// Config holds backend configuration from environment variables.
type Config struct {
	PrometheusURL      string
	ConfluenceURL      string
	ConfluenceToken  string // Personal Access Token (Bearer auth)
	ConfluencePageID string // Confluence page ID (CONFLUENCE_PAGE_ID env)
	UIPassword         string // simple password gate for UI (UI_PASSWORD env)
	ListenAddr         string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr: ":8080",
	}

	cfg.PrometheusURL = os.Getenv("PROMETHEUS_URL")
	if cfg.PrometheusURL == "" {
		return nil, fmt.Errorf("PROMETHEUS_URL is required")
	}

	cfg.ConfluenceURL = os.Getenv("CONFLUENCE_URL")
	cfg.ConfluenceToken = os.Getenv("CONFLUENCE_TOKEN")
	cfg.ConfluencePageID = os.Getenv("CONFLUENCE_PAGE_ID")
	cfg.UIPassword = os.Getenv("UI_PASSWORD")

	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		cfg.ListenAddr = addr
	}

	return cfg, nil
}
