package linux

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// lxcConn implements LXDClient via lxc CLI (JSON output).
type lxcConn struct{}

// NewLXDConnection creates an lxc-backed LXD client.
func NewLXDConnection() (LXDClient, error) {
	if _, err := exec.LookPath("lxc"); err != nil {
		return nil, fmt.Errorf("lxc not found: %w", err)
	}
	return &lxcConn{}, nil
}

func (c *lxcConn) Connect() error   { return nil }
func (c *lxcConn) Disconnect() error { return nil }

func (c *lxcConn) ListInstances() ([]LXDInstance, error) {
	cmd := exec.Command("lxc", "list", "--format", "json",
		"-c", "n,s,config:image.description,config:limits.cpu,config:limits.memory,config:volatile.last_state.power")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lxc list: %w", err)
	}

	var raw []map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing lxc list: %w", err)
	}

	var instances []LXDInstance
	for _, entry := range raw {
		// Only running containers.
		state := jsonGet(entry, "state")
		if state == nil || fmt.Sprint(state) != "Running" {
			continue
		}

		name := jsonGet(entry, "name")
		if name == nil {
			continue
		}

		inst := LXDInstance{Name: fmt.Sprint(name)}

		// Expanded config.
		if config, ok := entry["expanded_config"].(map[string]interface{}); ok {
			if v := jsonGet(config, "image.description"); v != nil {
				inst.OSName = fmt.Sprint(v)
			}
			if v := jsonGet(config, "limits.cpu"); v != nil {
				if n, ok := parseNumber(fmt.Sprint(v)); ok {
					cpulimit := int64(n)
					inst.CPULimit = &cpulimit
				}
			}
			if v := jsonGet(config, "limits.memory"); v != nil {
				if n, ok := parseNumber(fmt.Sprint(v)); ok {
					memlimit := int64(n)
					inst.MemLimit = &memlimit
				}
			}
			if v := jsonGet(config, "volatile.last_state.power"); v != nil {
				inst.Description = fmt.Sprint(v)
			}
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

func (c *lxcConn) ListStoragePools() ([]LXDPool, error) {
	cmd := exec.Command("lxc", "storage", "list", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lxc storage list: %w", err)
	}

	var raw []map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing lxc storage list: %w", err)
	}

	var pools []LXDPool
	for _, entry := range raw {
		name := jsonGet(entry, "name")
		driver := jsonGet(entry, "driver")
		if name == nil || driver == nil {
			continue
		}

		pool := LXDPool{
			Name:   fmt.Sprint(name),
			Driver: fmt.Sprint(driver),
		}

		// Total/available from config.
		if config, ok := entry["config"].(map[string]interface{}); ok {
			if v := jsonGet(config, "size"); v != nil {
				if n, ok := parseNumber(fmt.Sprint(v)); ok {
					pool.TotalBytes = int64(n)
				}
			}
		}

		// Also try resources.space.total / resources.space.used for usage.
		if resources, ok := entry["resources"].(map[string]interface{}); ok {
			if space, ok := resources["space"].(map[string]interface{}); ok {
				if v := jsonGet(space, "total"); v != nil {
					if n, ok := parseNumber(fmt.Sprint(v)); ok {
						pool.TotalBytes = int64(n)
					}
				}
				if v := jsonGet(space, "used"); v != nil {
					if n, ok := parseNumber(fmt.Sprint(v)); ok {
						pool.AvailBytes = pool.TotalBytes - int64(n)
					}
				}
			}
		}

		pools = append(pools, pool)
	}

	return pools, nil
}

func jsonGet(m map[string]interface{}, key string) interface{} {
	parts := strings.Split(key, ".")
	cur := interface{}(m)
	for _, p := range parts {
		if cm, ok := cur.(map[string]interface{}); ok {
			cur = cm[p]
		} else {
			return nil
		}
	}
	return cur
}

func parseNumber(s string) (float64, bool) {
	var n float64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &n)
	return n, err == nil
}
