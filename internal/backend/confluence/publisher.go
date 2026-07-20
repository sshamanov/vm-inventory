package confluence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/normalizer"
	"vm-inventory/internal/backend/state"
	"vm-inventory/internal/shared"
)

// PublishResult represents the outcome of a publication attempt (§19.5).
type PublishResult string

const (
	Published  PublishResult = "published"
	Unchanged  PublishResult = "unchanged"
	Failed     PublishResult = "failed"
)

// Publisher orchestrates Confluence page publication.
type Publisher struct {
	client        *http.Client
	confluenceURL string
	token         string // Personal Access Token for Bearer auth
	spaceKey      string
	idx           *index.ObservationIndex
	normalizer    *normalizer.Normalizer
	stateStore    *state.Store
	logger        *slog.Logger
}

// NewPublisher creates a new Confluence publisher.
// token is a Confluence Personal Access Token used as Bearer auth.
func NewPublisher(
	confluenceURL, token, spaceKey string,
	idx *index.ObservationIndex,
	stateStore *state.Store,
	logger *slog.Logger,
) *Publisher {
	return &Publisher{
		client:        &http.Client{Timeout: 30 * time.Second},
		confluenceURL: strings.TrimRight(confluenceURL, "/"),
		token:         token,
		spaceKey:      spaceKey,
		idx:           idx,
		normalizer:    normalizer.New(idx),
		stateStore:    stateStore,
		logger:        logger,
	}
}

// Publish runs the full publication flow (§19).
func (p *Publisher) Publish(ctx context.Context) PublishResult {
	// Build 8-hour snapshot.
	snapshot := p.normalizer.BuildConfluenceSnapshot()

	// Compute canonical hash (§19.4).
	hash := computeHash(snapshot)

	// Compare with stored hash.
	st, _ := p.stateStore.Load()
	shouldPublish := st.LastConfluenceHash != hash
	if !shouldPublish {
		p.logger.Info("confluence content unchanged, skipping publication")
		return Unchanged
	}

	// Render to Confluence Storage Format.
	body := renderStorageFormat(snapshot)

	// Publish to Confluence.
	if err := p.upsertPage(ctx, "VM Directory", body); err != nil {
		p.logger.Error("confluence publication failed", "error", err)
		return Failed
	}

	// Atomically update stored hash.
	p.stateStore.Update(func(st *state.State) (*state.State, error) {
		st.LastConfluenceHash = hash
		return st, nil
	})

	p.logger.Info("confluence page published")
	return Published
}

// computeHash creates a canonical SHA-256 hash of stable inventory values (§19.4).
func computeHash(snapshot *shared.NormalizedInventory) string {
	h := sha256.New()

	// Sort geos for deterministic output.
	sortedGeos := make([]shared.Geo, len(snapshot.Geos))
	copy(sortedGeos, snapshot.Geos)
	sort.Slice(sortedGeos, func(i, j int) bool { return sortedGeos[i].Name < sortedGeos[j].Name })

	for _, geo := range sortedGeos {
		fmt.Fprintf(h, "geo:%s\n", geo.Name)
		for _, host := range geo.Hosts {
			fmt.Fprintf(h, "host:%s desc:%s plat:%s os:%s ver:%s\n",
				host.ID, host.Description, host.Platform, host.OSName, host.OSVersion)
			fmt.Fprintf(h, "cpu:%s %d/%d/%d\n", host.CPU.Model, host.CPU.Sockets, host.CPU.Cores, host.CPU.Threads)
			fmt.Fprintf(h, "mem:%d\n", host.Memory.TotalBytes)
			fmt.Fprintf(h, "hp:%d\n", host.Memory.HugepagesTotalBytes)
			for _, dg := range host.Disks {
				fmt.Fprintf(h, "disk:%d*%d\n", dg.SizeBytes, dg.Count)
			}
			for _, fs := range host.Filesystems {
				fmt.Fprintf(h, "fs:%s:%d\n", fs.FilesystemID, fs.TotalBytes)
			}
			for _, pool := range host.StoragePools {
				fmt.Fprintf(h, "pool:%s:%d\n", pool.PoolID, pool.TotalBytes)
			}
		}
		for _, vm := range geo.VirtualMachines {
			fmt.Fprintf(h, "vm:%s:%s os:%s cpu:%d mem:%d\n",
				vm.HostID, vm.Name, vm.GuestOS, vm.CPUCount, vm.MemoryBytes)
		}
		for _, ct := range geo.LXDContainers {
			fmt.Fprintf(h, "lxd:%s:%s os:%s cpu:%d mem:%d\n",
				ct.HostID, ct.Name, ct.GuestOS, ct.CPUCount, ct.MemoryBytes)
		}
	}

	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

// renderStorageFormat builds Confluence Storage Format HTML (§20).
func renderStorageFormat(snapshot *shared.NormalizedInventory) string {
	var buf bytes.Buffer
	buf.WriteString(`<h1>VM Directory</h1>`)
	buf.WriteString(`<p>Active inventory includes resources observed during the eight hours before publication.</p>`)
	buf.WriteString(`<ac:structured-macro ac:name="toc"/>`)

	// Flatten all hosts, VMs, and LXD across geos.
	type hostWithGeo struct {
		host *shared.Host
		geo  string
	}
	var allHosts []hostWithGeo
	var allVMs []shared.VMResource
	var allLXDs []shared.LXDContainer
	for _, g := range snapshot.Geos {
		for i := range g.Hosts {
			allHosts = append(allHosts, hostWithGeo{host: &g.Hosts[i], geo: g.Name})
		}
		allVMs = append(allVMs, g.VirtualMachines...)
		allLXDs = append(allLXDs, g.LXDContainers...)
	}

	// --- Hosts table ---
	buf.WriteString(`<h2>Hosts</h2>`)
	buf.WriteString(`<table><tr><th>Host</th><th>Geo</th><th>Platform</th><th>OS</th><th>CPU</th><th>RAM</th><th>Storage</th></tr>`)
	for _, hg := range allHosts {
		h := hg.host
		cpu := fmt.Sprintf("%s (%d/%d/%d)", h.CPU.Model, h.CPU.Sockets, h.CPU.Cores, h.CPU.Threads)
		ram := formatBytes(h.Memory.TotalBytes)
		storage := ""
		if len(h.StoragePools) > 0 {
			var parts []string
			for _, p := range h.StoragePools {
				parts = append(parts, fmt.Sprintf("%s: %s total", p.PoolName, formatBytes(p.TotalBytes)))
			}
			storage = strings.Join(parts, "; ")
		} else if len(h.Disks) > 0 {
			var parts []string
			for _, d := range h.Disks {
				parts = append(parts, fmt.Sprintf("%s × %d", formatBytes(d.SizeBytes), d.Count))
			}
			storage = strings.Join(parts, ", ")
		}
		fmt.Fprintf(&buf, `<tr><td><strong>%s</strong></td><td>%s</td><td>%s</td><td>%s %s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			h.ID, hg.geo, h.Platform, h.OSName, h.OSVersion, cpu, ram, storage)
	}
	buf.WriteString(`</table>`)

	// --- Virtual Machines table ---
	if len(allVMs) > 0 {
		buf.WriteString(`<h2>Virtual Machines</h2>`)
		buf.WriteString(`<table><tr><th>Host</th><th>Name</th><th>Platform</th><th>Geo</th><th>IPs</th><th>Guest OS</th><th>vCPU</th><th>RAM</th><th>Disk</th></tr>`)
		for _, vm := range allVMs {
			ips := strings.Join(vm.IPs, ", ")
			fmt.Fprintf(&buf, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>`,
				vm.HostID, vm.Name, vm.Platform, vm.Geo, ips, vm.GuestOS, vm.CPUCount, formatBytes(vm.MemoryBytes), formatBytes(vm.DiskTotalBytes))
		}
		buf.WriteString(`</table>`)
	}

	// --- LXD Containers table ---
	if len(allLXDs) > 0 {
		buf.WriteString(`<h2>LXD Containers</h2>`)
		buf.WriteString(`<table><tr><th>Host</th><th>Name</th><th>Geo</th><th>IPs</th><th>Guest OS</th><th>CPU</th><th>RAM</th><th>Disk</th></tr>`)
		for _, ct := range allLXDs {
			ips := strings.Join(ct.IPs, ", ")
			fmt.Fprintf(&buf, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>`,
				ct.HostID, ct.Name, ct.Geo, ips, ct.GuestOS, ct.CPUCount, formatBytes(ct.MemoryBytes), formatBytes(ct.RootDiskBytes))
		}
		buf.WriteString(`</table>`)
	}

	return buf.String()
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}

func (p *Publisher) upsertPage(ctx context.Context, title, body string) error {
	// Confluence REST API: GET page by title, then PUT update or POST create.
	url := fmt.Sprintf("%s/rest/api/content?title=%s&spaceKey=%s&expand=version",
		p.confluenceURL, url.QueryEscape(title), url.QueryEscape(p.spaceKey))

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("finding page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Page exists — update it.
		var result struct {
			Results []struct {
				ID      string `json:"id"`
				Version struct {
					Number int `json:"number"`
				} `json:"version"`
			} `json:"results"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		if len(result.Results) > 0 {
			page := result.Results[0]
			updateURL := fmt.Sprintf("%s/rest/api/content/%s", p.confluenceURL, page.ID)
			payload := map[string]interface{}{
				"version": map[string]interface{}{"number": page.Version.Number + 1},
				"title":   title,
				"type":    "page",
				"body": map[string]interface{}{
					"storage": map[string]interface{}{
						"value":          body,
						"representation": "storage",
					},
				},
			}
			pl, _ := json.Marshal(payload)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPut, updateURL, bytes.NewReader(pl))
			req.Header.Set("Authorization", "Bearer "+p.token)
			req.Header.Set("Content-Type", "application/json")
			resp, err = p.client.Do(req)
			if err != nil {
				return fmt.Errorf("updating page: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("update failed: %d %s", resp.StatusCode, string(body))
			}
			return nil
		}
	}

	// Page doesn't exist — create it.
	createURL := fmt.Sprintf("%s/rest/api/content", p.confluenceURL)
	payload := map[string]interface{}{
		"title": title,
		"type":  "page",
		"space": map[string]string{"key": p.spaceKey},
		"body": map[string]interface{}{
			"storage": map[string]interface{}{
				"value":          body,
				"representation": "storage",
			},
		},
	}
	pl, _ := json.Marshal(payload)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(pl))
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = p.client.Do(req)
	if err != nil {
		return fmt.Errorf("creating page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create failed: %d %s", resp.StatusCode, string(body))
	}
	return nil
}
