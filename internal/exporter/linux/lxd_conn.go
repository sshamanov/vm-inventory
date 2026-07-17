package linux

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// lxdConn implements LXDClient via LXD REST API (Unix socket).
// No external binary needed — talks directly to the LXD daemon.
type lxdConn struct {
	httpClient *http.Client
}

// NewLXDConnection connects to the local LXD daemon via Unix socket.
func NewLXDConnection() (LXDClient, error) {
	socket := "/var/snap/lxd/common/lxd/unix.socket"
	if _, err := net.Dial("unix", socket); err != nil {
		// Try the non-snap path.
		socket = "/var/lib/lxd/unix.socket"
		if _, err := net.Dial("unix", socket); err != nil {
			return nil, fmt.Errorf("LXD socket not found at /var/snap/lxd/common/lxd/unix.socket or /var/lib/lxd/unix.socket")
		}
	}

	return &lxdConn{
		httpClient: &http.Client{
			Transport: &http.Transport{
				Dial: func(_, _ string) (net.Conn, error) {
					return net.Dial("unix", socket)
				},
			},
			Timeout: 10 * time.Second,
		},
	}, nil
}

func (c *lxdConn) Connect() error   { return nil }
func (c *lxdConn) Disconnect() error { return nil }

func (c *lxdConn) ListInstances() ([]LXDInstance, error) {
	var instances []LXDInstance

	// Recursively list instances from all projects.
	projects := c.listProjects()

	for _, project := range projects {
		data, err := c.do("GET", "/1.0/instances?project="+project+"&recursion=1")
		if err != nil {
			continue
		}

		var resp struct {
			Metadata []struct {
				Name      string `json:"name"`
				Status    string `json:"status"`
				Config    map[string]string `json:"config"`
				ExpandedConfig map[string]string `json:"expanded_config"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}

		for _, inst := range resp.Metadata {
			if inst.Status != "Running" {
				continue
			}

			li := LXDInstance{Name: fmt.Sprintf("%s/%s", project, inst.Name)}

			cfg := inst.ExpandedConfig
			if cfg == nil {
				cfg = inst.Config
			}

			if v, ok := cfg["image.description"]; ok {
				li.OSName = v
			}
			if v, ok := cfg["limits.cpu"]; ok {
				if n, ok2 := parseNumber(v); ok2 {
					cpu := int64(n)
					li.CPULimit = &cpu
				}
			}
			if v, ok := cfg["limits.memory"]; ok {
				if n, ok2 := parseNumber(v); ok2 {
					mem := int64(n)
					li.MemLimit = &mem
				}
			}
			instances = append(instances, li)
		}
	}

	return instances, nil
}

// listProjects returns project names. Always includes "default".
func (c *lxdConn) listProjects() []string {
	names := []string{"default"}
	data, err := c.do("GET", "/1.0/projects")
	if err != nil {
		return names
	}
	var raw struct {
		Metadata []string `json:"metadata"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return names
	}
	for _, u := range raw.Metadata {
		if p := strings.TrimPrefix(u, "/1.0/projects/"); p != u && p != "default" {
			names = append(names, p)
		}
	}
	return names
}

func (c *lxdConn) ListStoragePools() ([]LXDPool, error) {
	data, err := c.do("GET", "/1.0/storage-pools?recursion=1")
	if err != nil {
		return nil, fmt.Errorf("storage-pools: %w", err)
	}

	var resp struct {
		Metadata []struct {
			Name   string `json:"name"`
			Driver string `json:"driver"`
			Config map[string]string `json:"config"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing storage-pools: %w", err)
	}

	var pools []LXDPool
	for _, p := range resp.Metadata {
		pool := LXDPool{Name: p.Name, Driver: p.Driver}

		// Get detailed resources from /1.0/storage-pools/<name>/resources
		if resData, err := c.do("GET", "/1.0/storage-pools/"+p.Name+"/resources"); err == nil {
			var resResp struct {
				Metadata struct {
					Space struct {
						Total int64 `json:"total"`
						Used  int64 `json:"used"`
					} `json:"space"`
				} `json:"metadata"`
			}
			if json.Unmarshal(resData, &resResp) == nil {
				pool.TotalBytes = resResp.Metadata.Space.Total
				pool.AvailBytes = pool.TotalBytes - resResp.Metadata.Space.Used
			}
		}

		pools = append(pools, pool)
	}
	return pools, nil
}

func (c *lxdConn) do(method, path string) ([]byte, error) {
	req, err := http.NewRequest(method, "http://unix"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func parseNumber(s string) (float64, bool) {
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
