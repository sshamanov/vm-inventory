package confluence

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	Unchanged PublishResult = "unchanged"
	Failed    PublishResult = "failed"
)

const (
	pageTitle = "VM Directory"
	// hashPropertyKey is the Confluence content property that carries the hash
	// of the body this application last published. It lives on the page, not in
	// the container: nothing about the last publication survives a restart
	// locally, so a replaced or duplicated backend reads the same answer (§20).
	hashPropertyKey = "inventory-hash"
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

// Publish runs the publication flow (§19): render the current snapshot, ask
// Confluence what it already holds, and rewrite the page only when the content
// differs. The unchanged case is the common one — most publications run on a
// schedule against an inventory that has not moved — and skipping it is the
// whole point of the hash, since every write bumps the page version and
// notifies its watchers.
func (p *Publisher) Publish(ctx context.Context) PublishResult {
	snapshot := p.normalizer.BuildConfluenceSnapshot()
	body := renderStorageFormat(snapshot)
	hash := contentHash(body)

	pageVersion, err := p.currentVersion(ctx)
	if err != nil {
		p.logger.Error("confluence publish failed", "error", err, "page", p.pageID)
		return Failed
	}

	stored, propVersion, found, err := p.storedHash(ctx)
	if err != nil {
		p.logger.Error("confluence publish failed", "error", err, "page", p.pageID)
		return Failed
	}
	if found && stored == hash {
		p.logger.Info("confluence page unchanged", "page", p.pageID, "hash", hash)
		return Unchanged
	}

	p.logger.Info("publishing to Confluence", "page", p.pageID, "hash", hash)
	if err := p.putPage(ctx, pageVersion, pageTitle, body); err != nil {
		p.logger.Error("confluence publish failed", "error", err, "page", p.pageID)
		return Failed
	}

	// The page is the artifact; the property only records what was published.
	// A property write that fails leaves the published content correct and costs
	// one redundant write next time, so it is reported as a failure rather than
	// swallowed, but the page is never rolled back to hide it.
	if err := p.writeHash(ctx, hash, propVersion); err != nil {
		p.logger.Error("confluence page updated but hash not recorded", "error", err, "page", p.pageID)
		return Failed
	}

	p.logger.Info("confluence page published", "page", p.pageID, "version", pageVersion+1)
	return Published
}

// contentHash hashes the rendered body, not the snapshot behind it. A renderer
// change — a new column, a relabelled heading — therefore counts as a change and
// republishes, which is what §19.4 wants: hashing the snapshot instead would pin
// the page to whatever layout was live the first time, because the inventory
// underneath never moved.
func contentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return fmt.Sprintf("%x", sum)
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

// currentVersion returns the page's current version number, whose successor the
// next write must claim (§19.4).
func (p *Publisher) currentVersion(ctx context.Context) (int, error) {
	getURL := fmt.Sprintf("%s/rest/api/content/%s?expand=version", p.confluenceURL, p.pageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return 0, fmt.Errorf("building version request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("fetching version: %d %s", resp.StatusCode, string(errBody))
	}
	var page struct {
		Version struct {
			Number int `json:"number"`
		} `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return 0, fmt.Errorf("decoding version: %w", err)
	}
	return page.Version.Number, nil
}

// storedHash reads the hash this application recorded for the page. A page that
// this application has never published has no such property, and neither has one
// whose property API refuses to answer: both are reported as "not found" so the
// page is rewritten. Refusing to publish because bookkeeping is unreadable would
// be the worse failure.
func (p *Publisher) storedHash(ctx context.Context) (string, int, bool, error) {
	url := fmt.Sprintf("%s/rest/api/content/%s/property/%s", p.confluenceURL, p.pageID, hashPropertyKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, false, fmt.Errorf("building property request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, false, fmt.Errorf("reading published hash: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", 0, false, nil
	case resp.StatusCode != http.StatusOK:
		p.logger.Warn("published hash unreadable, publishing", "status", resp.StatusCode, "page", p.pageID)
		return "", 0, false, nil
	}

	var prop struct {
		Value struct {
			Hash string `json:"hash"`
		} `json:"value"`
		Version struct {
			Number int `json:"number"`
		} `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&prop); err != nil {
		p.logger.Warn("published hash unparsable, publishing", "error", err, "page", p.pageID)
		return "", 0, false, nil
	}
	if prop.Value.Hash == "" {
		return "", 0, false, nil
	}
	return prop.Value.Hash, prop.Version.Number, true, nil
}

// putPage writes the rendered body as the page's next version.
func (p *Publisher) putPage(ctx context.Context, version int, title, body string) error {
	payload := map[string]interface{}{
		"version": map[string]interface{}{"number": version + 1},
		"title":   title,
		"type":    "page",
		"body":    map[string]interface{}{"storage": map[string]interface{}{"value": body, "representation": "storage"}},
	}
	pl, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding page: %w", err)
	}
	putURL := fmt.Sprintf("%s/rest/api/content/%s", p.confluenceURL, p.pageID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(pl))
	if err != nil {
		return fmt.Errorf("building page request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
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

// writeHash records the published hash as a content property on the page.
// Content properties carry their own version chain, so the number is the stored
// property's successor; a fresh property starts at 1.
func (p *Publisher) writeHash(ctx context.Context, hash string, propVersion int) error {
	payload := map[string]interface{}{
		"key":     hashPropertyKey,
		"value":   map[string]string{"hash": hash},
		"version": map[string]int{"number": propVersion + 1},
	}
	pl, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding property: %w", err)
	}
	url := fmt.Sprintf("%s/rest/api/content/%s/property/%s", p.confluenceURL, p.pageID, hashPropertyKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(pl))
	if err != nil {
		return fmt.Errorf("building property request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("recording published hash: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("recording published hash: %d %s", resp.StatusCode, string(errBody))
	}
	return nil
}
