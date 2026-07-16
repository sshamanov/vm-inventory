package linux

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// virshConn implements LibvirtConnection via virsh CLI.
type virshConn struct{}

func NewLibvirtConnection() (LibvirtConnection, error) {
	if _, err := exec.LookPath("virsh"); err != nil {
		return nil, fmt.Errorf("virsh not found: %w", err)
	}
	return &virshConn{}, nil
}

func (c *virshConn) Connect() error   { return nil }
func (c *virshConn) Disconnect() error { return nil }

func (c *virshConn) ListDomains(ctx context.Context) ([]LibvirtDomain, error) {
	out, err := c.virshCtx(ctx, "list", "--uuid", "--state-running")
	if err != nil {
		return nil, fmt.Errorf("virsh list: %w", err)
	}

	uuids := strings.Fields(out)
	var domains []LibvirtDomain
	for _, uuid := range uuids {
		d, err := c.domainInfo(ctx, uuid)
		if err != nil {
			continue
		}
		domains = append(domains, d)
	}
	return domains, nil
}

func (c *virshConn) domainInfo(ctx context.Context, uuid string) (LibvirtDomain, error) {
	name := c.virshIgnoreError(ctx, "domname", uuid)

	d := LibvirtDomain{
		UUID:        uuid,
		Name:        name,
		Description: c.virshIgnoreError(ctx, "desc", uuid),
	}

	if v := c.virshIgnoreError(ctx, "domstats", "--vcpu", uuid); v != "" {
		d.VCPUs = parseVirshStat(v, "vcpu.current")
	}
	if v := c.virshIgnoreError(ctx, "domstats", "--balloon", uuid); v != "" {
		kb := parseVirshStat(v, "balloon.current")
		d.MemoryBytes = int64(kb) * 1024
	}

	// Disks.
	out, _ := exec.CommandContext(ctx, "virsh", "-q", "domblklist", "--details", uuid).Output()
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "disk" {
			continue
		}
		sizeBytes := int64(0)
		if sizeStr := c.virshIgnoreError(ctx, "domblkinfo", uuid, fields[2]); sizeStr != "" {
			if v := parseVirshStat(sizeStr, "Capacity"); v > 0 {
				sizeBytes = int64(v)
			}
		}
		d.Disks = append(d.Disks, LibvirtDisk{Name: fields[2], SizeBytes: sizeBytes})
	}

	// IPs from QEMU agent.
	if ifaces := c.virshIgnoreError(ctx, "domifaddr", "--source=agent", uuid); ifaces != "" {
		d.IPs = parseIPs(ifaces)
	}

	// Guest OS.
	guestOS := c.virshIgnoreError(ctx, "qemu-agent-command", uuid, `{"execute":"guest-get-osinfo"}`)
	if guestOS != "" {
		d.GuestOS = extractJSON(guestOS, "name")
	}

	return d, nil
}

func (c *virshConn) ListStoragePools(ctx context.Context) ([]LibvirtPool, error) {
	out, err := c.virshCtx(ctx, "pool-list", "--type", "logical", "--details")
	if err != nil {
		return nil, fmt.Errorf("virsh pool-list: %w", err)
	}

	// pool-list --details columns: Name State Autostart Persistent Capacity Allocation Available
	var pools []LibvirtPool
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[1] != "active" {
			continue
		}
		name := fields[0]
		uuid := c.virshIgnoreError(ctx, "pool-uuid", name)

		p := LibvirtPool{UUID: uuid, Name: name, PoolType: "logical"}
		if info := c.virshIgnoreError(ctx, "pool-info", name); info != "" {
			p.TotalBytes = parsePoolInfo(info, "Capacity")
			p.AvailBytes = parsePoolInfo(info, "Available")
		}
		pools = append(pools, p)
	}
	return pools, nil
}

func (c *virshConn) virshCtx(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "virsh", append([]string{"-q"}, args...)...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// virshIgnoreError runs virsh silently, returning empty string on any error.
func (c *virshConn) virshIgnoreError(ctx context.Context, args ...string) string {
	s, _ := c.virshCtx(ctx, args...)
	return s
}

func parseVirshStat(output, key string) int {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, key) {
			// Supports both "key=value" (domstats) and "key: value" (domblkinfo).
			for _, sep := range []string{"=", ":"} {
				if parts := strings.SplitN(line, sep, 2); len(parts) == 2 {
					v, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
					return v
				}
			}
		}
	}
	return 0
}

func parsePoolInfo(output, field string) int64 {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, field+":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				valFields := strings.Fields(strings.TrimSpace(parts[1]))
				if len(valFields) < 1 {
					return 0
				}
				v, _ := strconv.ParseFloat(valFields[0], 64)
				// Handle unit suffix (KiB, MiB, GiB, TiB).
				if len(valFields) >= 2 {
					switch valFields[1] {
					case "TiB":
						v *= 1024 * 1024 * 1024 * 1024
					case "GiB":
						v *= 1024 * 1024 * 1024
					case "MiB":
						v *= 1024 * 1024
					case "KiB":
						v *= 1024
					}
				}
				return int64(v)
			}
		}
	}
	return 0
}

func parseIPs(output string) []string {
	var ips []string
	for _, line := range strings.Split(output, "\n") {
		for _, f := range strings.Fields(line) {
			if strings.Count(f, ".") == 3 || strings.Count(f, ":") >= 2 {
				ips = append(ips, f)
			}
		}
	}
	return ips
}

func extractJSON(output, key string) string {
	search := fmt.Sprintf(`"%s":`, key)
	idx := strings.Index(output, search)
	if idx < 0 {
		return ""
	}
	rest := output[idx+len(search):]
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		rest = rest[1:]
		if end := strings.Index(rest, `"`); end > 0 {
			return rest[:end]
		}
	}
	return ""
}
