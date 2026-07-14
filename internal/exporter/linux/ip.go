package linux

import (
	"net"
	"sort"

	"vm-inventory/internal/shared"
)

// getHostIPs returns all non-loopback, non-link-local IP addresses
// for the host, sorted IPv4 before IPv6 (§11.2).
func getHostIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var ips []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip == nil {
				continue
			}
			// Strip zone ID for IPv6 link-local.
			ipStr := ip.String()
			ips = append(ips, ipStr)
		}
	}

	// Filter and sort using shared functions.
	filtered := shared.FilterIPs(ips)
	sort.Strings(filtered)
	shared.SortIPs(filtered)
	return filtered
}
