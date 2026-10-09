package exporter

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"vm-inventory/internal/shared"
)

func TestLoadConfigLinux(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
mode: linux
host:
  id: hv-kvm-01
  description: Main KVM host
  geo: belgrade
collection:
  interval: 15m
  timeout: 5m
  max_snapshot_age: 8h
collectors:
  libvirt:
    enabled: true
  lxd:
    enabled: true
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Mode != ModeLinux {
		t.Errorf("mode = %q, want %q", cfg.Mode, ModeLinux)
	}
	if cfg.Host.ID != "hv-kvm-01" {
		t.Errorf("host.id = %q, want %q", cfg.Host.ID, "hv-kvm-01")
	}
	if cfg.Host.Description != "Main KVM host" {
		t.Errorf("host.description = %q", cfg.Host.Description)
	}
	if cfg.Host.Geo != "belgrade" {
		t.Errorf("host.geo = %q", cfg.Host.Geo)
	}
	if cfg.Collection.Interval != 15*time.Minute {
		t.Errorf("interval = %v", cfg.Collection.Interval)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
mode: linux
host:
  id: minimal-host
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Host.Geo != shared.DefaultGeo {
		t.Errorf("geo default = %q, want %q", cfg.Host.Geo, shared.DefaultGeo)
	}
	if cfg.Collection.Interval != shared.DefaultCollectionInterval {
		t.Errorf("interval default = %v, want %v", cfg.Collection.Interval, shared.DefaultCollectionInterval)
	}
	if cfg.Collection.Timeout != shared.DefaultCollectionTimeout {
		t.Errorf("timeout default = %v", cfg.Collection.Timeout)
	}
	if cfg.Collection.MaxSnapshotAge != shared.DefaultMaxSnapshotAge {
		t.Errorf("maxSnapshotAge default = %v", cfg.Collection.MaxSnapshotAge)
	}
}

func TestLoadConfigESXi(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	content := `
mode: esxi
collection:
  interval: 15m
targets:
  - host_id: esxi-01
    address: https://esxi-01.internal
    username: admin
    password: secret
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Mode != ModeESXi {
		t.Errorf("mode = %q", cfg.Mode)
	}
	if len(cfg.Targets) != 1 {
		t.Fatalf("targets = %d", len(cfg.Targets))
	}
	if cfg.Targets[0].HostID != "esxi-01" {
		t.Errorf("target host_id = %q", cfg.Targets[0].HostID)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content string
	}{
		{"empty mode", "mode: \n"},
		{"invalid mode", "mode: windows\n"},
		{"esxi without targets", "mode: esxi\n"},
		{"esxi target without address", "mode: esxi\ntargets:\n  - host_id: test\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(dir, tt.name+".yaml")
			os.WriteFile(configPath, []byte(tt.content), 0644)
			_, err := LoadConfig(configPath)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestCollectorToggle(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name string
		ct   *CollectorToggle
		want bool
	}{
		{"nil -> true", nil, true},
		{"enabled", &CollectorToggle{Enabled: &enabled}, true},
		{"disabled", &CollectorToggle{Enabled: &disabled}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ct.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}
