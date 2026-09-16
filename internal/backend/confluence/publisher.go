package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vm-inventory/internal/backend/index"
	"vm-inventory/internal/backend/normalizer"
	"vm-inventory/internal/shared"
)

// PublishResult represents the outcome of a publication attempt (§19.5).
type PublishResult string

const (
	Published PublishResult = "published"
	Failed    PublishResult = "failed"
)

// Publisher orchestrates Confluence page publication.
type Publisher struct {
	client        *http.Client
	confluenceURL string
	token         string // Personal Access Token for Bearer auth
	pageID        string // Confluence page ID to update
	idx           *index.ObservationIndex
	normalizer    *normalizer.Normalizer
	logger        *slog.Logger
}

// NewPublisher creates a new Confluence publisher.
// token is a Confluence Personal Access Token used as Bearer auth.
// pageID is the Confluence page ID to update (from CONFLUENCE_PAGE_ID env).
func NewPublisher(
	confluenceURL, token, pageID string,
	idx *index.ObservationIndex,
	logger *slog.Logger,
) *Publisher {
	return &Publisher{
		client:        &http.Client{Timeout: 30 * time.Second},
		confluenceURL: strings.TrimRight(confluenceURL, "/"),
		token:         token,
		pageID:        pageID,
		idx:           idx,
		normalizer:    normalizer.New(idx),
		logger:        logger,
	}
}

// Publish runs the publication flow: render current snapshot and update the Confluence page.
func (p *Publisher) Publish(ctx context.Context) PublishResult {
	snapshot := p.normalizer.BuildConfluenceSnapshot()
	body := renderStorageFormat(snapshot)

	p.logger.Info("publishing to Confluence", "page", p.pageID)
	if err := p.updatePage(ctx, p.pageID, "VM Directory", body); err != nil {
		p.logger.Error("confluence publish failed", "error", err, "page", p.pageID)
		return Failed
	}

	p.logger.Info("confluence page published", "page", p.pageID)
	return Published
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
		buf.WriteString(`<table><tr><th>Host</th><th>Name</th><th>Platform</th><th>Geo</th><th>IPs</th><th>Guest OS</th><th>vCPU</th><th>RAM</th><th>Disk</th><th>Description</th></tr>`)
		for _, vm := range allVMs {
			ips := formatIPs(vm.IPs)
			fmt.Fprintf(&buf, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				vm.HostID, vm.Name, vm.Platform, vm.Geo, ips, vm.GuestOS, formatCPU(vm.CPUCount), formatBytes(vm.MemoryBytes), formatBytes(vm.DiskTotalBytes), formatText(vm.Description))
		}
		buf.WriteString(`</table>`)
	}

	// --- LXD Containers table ---
	if len(allLXDs) > 0 {
		buf.WriteString(`<h2>LXD Containers</h2>`)
		buf.WriteString(`<table><tr><th>Host</th><th>Name</th><th>Geo</th><th>IPs</th><th>Guest OS</th><th>CPU</th><th>RAM</th><th>Disk</th><th>Description</th></tr>`)
		for _, ct := range allLXDs {
			ips := formatIPs(ct.IPs)
			fmt.Fprintf(&buf, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
				ct.HostID, ct.Name, ct.Geo, ips, ct.GuestOS, formatCPU(ct.CPUCount), formatBytes(ct.MemoryBytes), formatBytes(ct.RootDiskBytes), formatText(ct.Description))
		}
		buf.WriteString(`</table>`)
	}

	return buf.String()
}

func formatCPU(n int64) string {
	if n == 0 { return "—" }
	return fmt.Sprintf("%d", n)
}

func formatIPs(ips []string) string {
	if len(ips) == 0 {
		return "—"
	}
	if len(ips) <= 2 {
		return strings.Join(ips, ", ")
	}
	return strings.Join(ips[:2], ", ") + ", …"
}

// formatText renders free text for the page: the em dash for a value the
// inventory does not have (§18), and escaped otherwise, since a description is
// free text landing in Confluence's storage format.
func formatText(s string) string {
	if s == "" {
		return "—"
	}
	return html.EscapeString(s)
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "—"
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

// updatePage updates a Confluence page by ID.
func (p *Publisher) updatePage(ctx context.Context, pageID, title, body string) error {
	// Fetch current version.
	getURL := fmt.Sprintf("%s/rest/api/content/%s?expand=version", p.confluenceURL, pageID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching version: %w", err)
	}
	var page struct{ Version struct{ Number int `json:"number"` } `json:"version"` }
	if resp.StatusCode == http.StatusOK {
		json.NewDecoder(resp.Body).Decode(&page)
	}
	resp.Body.Close()

	payload := map[string]interface{}{
		"version": map[string]interface{}{"number": page.Version.Number + 1},
		"title":   title,
		"type":    "page",
		"body":    map[string]interface{}{"storage": map[string]interface{}{"value": body, "representation": "storage"}},
	}
	pl, _ := json.Marshal(payload)
	putURL := fmt.Sprintf("%s/rest/api/content/%s", p.confluenceURL, pageID)
	req, _ = http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(pl))
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = p.client.Do(req)
	if err != nil {
		return fmt.Errorf("updating page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update failed: %d %s", resp.StatusCode, string(errBody))
	}
	return nil
}
