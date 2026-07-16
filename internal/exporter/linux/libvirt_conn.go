package linux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// virshConn implements LibvirtConnection via virsh CLI.
// go-libvirt is the long-term goal, but virsh is reliable and available everywhere.
type virshConn struct{}

// NewLibvirtConnection creates a virsh-backed connection.
func NewLibvirtConnection() (LibvirtConnection, error) {
	if _, err := exec.LookPath("virsh"); err != nil {
		return nil, fmt.Errorf("virsh not found: %w", err)
	}
	return &virshConn{}, nil
}

func (c *virshConn) Connect() error    { return nil }
func (c *virshConn) Disconnect() error  { return nil }

func (c *virshConn) ListDomains() ([]LibvirtDomain, error) {
	out, err := exec.Command("virsh", "-q", "list", "--uuid", "--state-running").Output()
	if err != nil {
		return nil, fmt.Errorf("virsh list: %w", err)
	}

	uuids := strings.Fields(string(out))
	var domains []LibvirtDomain

	for _, uuid := range uuids {
		d, err := c.domainInfo(uuid)
		if err != nil {
			continue
		}
		domains = append(domains, d)
	}

	return domains, nil
}

func (c *virshConn) domainInfo(uuid string) (LibvirtDomain, error) {
	name := c.virsh("domname", uuid)

	d := LibvirtDomain{
		UUID:        uuid,
		Name:        name,
		Description: c.virsh("desc", uuid),
	}

	// vCPU count.
	if v := c.virsh("domstats", "--vcpu", uuid); v != "" {
		d.VCPUs = parseVirshStat(v, "vcpu.current")
	}

	// Memory.
	if v := c.virsh("domstats", "--balloon", uuid); v != "" {
		kb := parseVirshStat(v, "balloon.current")
		d.MemoryBytes = int64(kb) * 1024
	}

	// Disks from domblklist.
	blkOut, _ := exec.Command("virsh", "-q", "domblklist", "--details", uuid).Output()
	for _, line := range strings.Split(string(blkOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "disk" {
			continue
		}
		sizeBytes := int64(0)
		if sizeStr := c.virsh("domblkinfo", uuid, fields[0]); sizeStr != "" {
			if v := parseVirshStat(sizeStr, "Capacity"); v > 0 {
				sizeBytes = int64(v)
			}
		}
		d.Disks = append(d.Disks, LibvirtDisk{Name: fields[0], SizeBytes: sizeBytes})
	}

	// IPs.
	if ifaces := c.virsh("domifaddr", "--source=agent", uuid); ifaces != "" {
		d.IPs = parseIPs(ifaces)
	}

	// Guest OS from QEMU agent.
	guestOS := c.virsh("qemu-agent-command", uuid, `{"execute":"guest-get-osinfo"}`)
	if guestOS != "" {
		d.GuestOS = extractJSON(guestOS, "name")
	}

	return d, nil
}

func (c *virshConn) ListStoragePools() ([]LibvirtPool, error) {
	out, err := exec.Command("virsh", "-q", "pool-list", "--type", "logical", "--details").Output()
	if err != nil {
		return nil, fmt.Errorf("virsh pool-list: %w", err)
	}

	var pools []LibvirtPool
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[4] != "active" {
			continue
		}
		name := fields[0]
		uuid := c.virsh("pool-uuid", name)

		p := LibvirtPool{
			UUID:     uuid,
			Name:     name,
			PoolType: "logical",
		}

		if info := c.virsh("pool-info", name); info != "" {
			p.TotalBytes = parsePoolInfo(info, "Capacity") * 1024
			p.AvailBytes = parsePoolInfo(info, "Available") * 1024
		}

		pools = append(pools, p)
	}

	return pools, nil
}

func (c *virshConn) virsh(args ...string) string {
	out, err := exec.Command("virsh", append([]string{"-q"}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func parseVirshStat(output, key string) int {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, key+"=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				v, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
				return v
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
				valStr := strings.TrimSpace(strings.TrimSuffix(parts[1], " KiB"))
				v, _ := strconv.ParseFloat(strings.Fields(valStr)[0], 64)
				return int64(v)
			}
		}
	}
	return 0
}

func parseIPs(output string) []string {
	var ips []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.Count(f, ".") == 3 || strings.Count(f, ":") >= 2 {
				ips = append(ips, f)
			}
		}
	}
	return ips
}

func extractJSON(output, key string) string {
	// Simple extraction: {"key":"value"} or {"key": "value"}.
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

