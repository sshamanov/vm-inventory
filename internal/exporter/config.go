package exporter

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"vm-inventory/internal/shared"
)

// Mode constants (§7).
const (
	ModeLinux = "linux"
	ModeESXi  = "esxi"
)

// Config is the top-level exporter configuration (§7).
type Config struct {
	Mode       string           `yaml:"mode"`
	Host       *HostConfig      `yaml:"host,omitempty"`
	Collection CollectionConfig `yaml:"collection"`
	Collectors *CollectorConfig `yaml:"collectors,omitempty"`
	Targets    []ESXITarget     `yaml:"targets,omitempty"`
}

// HostConfig holds host identity for Linux mode (§7.1).
type HostConfig struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description,omitempty"`
	Geo         string `yaml:"geo,omitempty"`
}

// CollectionConfig holds collection timing parameters (§8).
type CollectionConfig struct {
	Interval       time.Duration `yaml:"interval"`
	Timeout        time.Duration `yaml:"timeout"`
	MaxSnapshotAge time.Duration `yaml:"max_snapshot_age"`
}

// CollectorConfig enables or disables optional collectors (§7.1).
type CollectorConfig struct {
	Libvirt *CollectorToggle `yaml:"libvirt,omitempty"`
	LXD     *CollectorToggle `yaml:"lxd,omitempty"`
}

// CollectorToggle is an optional boolean with default true for Linux collectors.
type CollectorToggle struct {
	Enabled *bool `yaml:"enabled"`
}

// ESXITarget holds per-target ESXi credentials and identity (§7.2).
type ESXITarget struct {
	HostID             string `yaml:"host_id"`
	HostDescription    string `yaml:"host_description,omitempty"`
	Geo                string `yaml:"geo,omitempty"`
	Address            string `yaml:"address"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// IsEnabled returns true unless explicitly disabled.
func (c *CollectorToggle) IsEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true // enabled by default (§7.1)
	}
	return *c.Enabled
}

// LoadConfig reads and validates a YAML config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	return &cfg, nil
}

func (c *Config) validate() error {
	switch c.Mode {
	case ModeLinux:
		return c.validateLinux()
	case ModeESXi:
		return c.validateESXi()
	default:
		return fmt.Errorf("mode must be %q or %q, got %q", ModeLinux, ModeESXi, c.Mode)
	}
}

func (c *Config) validateLinux() error {
	if c.Host == nil {
		return errors.New("linux mode requires host configuration")
	}
	if c.Host.ID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return errors.New("host id is required when hostname cannot be determined")
		}
		c.Host.ID = hostname
	}
	return nil
}

func (c *Config) validateESXi() error {
	if len(c.Targets) == 0 {
		return errors.New("esxi mode requires at least one target")
	}
	for i, t := range c.Targets {
		if t.HostID == "" {
			return fmt.Errorf("target %d: host_id is required", i)
		}
		if t.Address == "" {
			return fmt.Errorf("target %d (%s): address is required", i, t.HostID)
		}
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Collection.Interval == 0 {
		c.Collection.Interval = shared.DefaultCollectionInterval
	}
	if c.Collection.Timeout == 0 {
		c.Collection.Timeout = shared.DefaultCollectionTimeout
	}
	if c.Collection.MaxSnapshotAge == 0 {
		c.Collection.MaxSnapshotAge = shared.DefaultMaxSnapshotAge
	}
	if c.Host != nil {
		if c.Host.Geo == "" {
			c.Host.Geo = shared.DefaultGeo
		}
	}
	for i := range c.Targets {
		if c.Targets[i].Geo == "" {
			c.Targets[i].Geo = shared.DefaultGeo
		}
	}
}

// ValidateHostID checks the config's host ID.
func (c *Config) ValidateHostID() error {
	if c.Host != nil {
		return shared.ValidateHostID(c.Host.ID)
	}
	return nil
}
