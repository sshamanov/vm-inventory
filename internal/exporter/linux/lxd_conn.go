package linux

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// lxcConn implements LXDClient via lxc CLI.
type lxcConn struct{}

func NewLXDConnection() (LXDClient, error) {
	return &lxcConn{}, nil
}

func (c *lxcConn) Connect() error   { return nil }
func (c *lxcConn) Disconnect() error { return nil }

func (c *lxcConn) lxc(args ...string) *exec.Cmd {
	cmd := exec.Command("lxc", args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin"}
	return cmd
}

func (c *lxcConn) ListInstances() ([]LXDInstance, error) {
	out, err := c.lxc("list", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("lxc list: %w", err)
	}

	var raw []struct {
		Name           string            `json:"name"`
		Status         string            `json:"status"`
		Config         map[string]string `json:"config"`
		ExpandedConfig map[string]string `json:"expanded_config"`
		State          struct {
			Network map[string]struct {
				Addresses []struct {
					Address string `json:"address"`
				} `json:"addresses"`
			} `json:"network"`
		} `json:"state"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing lxc list: %w", err)
	}

	var instances []LXDInstance
	for _, e := range raw {
		if e.Status != "Running" {
			continue
		}
		inst := LXDInstance{Name: e.Name}

		cfg := e.ExpandedConfig
		if cfg == nil {
			cfg = e.Config
		}

		if v, ok := cfg["image.description"]; ok {
			inst.OSName = v
		}
		if v, ok := cfg["limits.cpu"]; ok {
			if n, ok2 := parseCLI(v); ok2 {
				cpu := int64(n)
				inst.CPULimit = &cpu
			}
		}
		if v, ok := cfg["limits.memory"]; ok {
			if n, ok2 := parseCLI(v); ok2 {
				mem := int64(n)
				inst.MemLimit = &mem
			}
		}

		for _, nic := range e.State.Network {
			for _, addr := range nic.Addresses {
				inst.IPs = append(inst.IPs, addr.Address)
			}
		}

		instances = append(instances, inst)
	}
	return instances, nil
}

func (c *lxcConn) ListStoragePools() ([]LXDPool, error) {
	out, err := c.lxc("storage", "list", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("lxc storage list: %w", err)
	}

	var raw []struct {
		Name   string            `json:"name"`
		Driver string            `json:"driver"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing lxc storage list: %w", err)
	}

	var pools []LXDPool
	for _, e := range raw {
		pool := LXDPool{Name: e.Name, Driver: e.Driver}

		if v, ok := e.Config["size"]; ok {
			if n, ok2 := parseCLI(v); ok2 {
				pool.TotalBytes = int64(n)
			}
		}

		infoOut, err := c.lxc("storage", "info", e.Name, "--format", "json").Output()
		if err == nil {
			var info struct {
				Resources struct {
					Space struct {
						Total int64 `json:"total"`
						Used  int64 `json:"used"`
					} `json:"space"`
				} `json:"resources"`
			}
			if json.Unmarshal(infoOut, &info) == nil {
				pool.TotalBytes = info.Resources.Space.Total
				pool.AvailBytes = pool.TotalBytes - info.Resources.Space.Used
			}
		}

		pools = append(pools, pool)
	}
	return pools, nil
}

func parseCLI(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	mult := 1.0
	for _, suf := range []struct {
		unit string
		m    float64
	}{
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
	} {
		if strings.HasSuffix(s, suf.unit) {
			s = strings.TrimSuffix(s, suf.unit)
			mult = suf.m
			break
		}
	}
	var n float64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &n)
	return n * mult, err == nil
}
