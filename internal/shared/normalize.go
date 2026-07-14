package shared

import (
	"net"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CleanDescription normalizes a description string per §11.1:
// valid UTF-8, no control characters (except space and printable),
// collapse whitespace, trim, max 1024 bytes, truncate on UTF-8 boundary.
func CleanDescription(s string) string {
	if s == "" {
		return ""
	}

	// Remove control characters except space and common whitespace.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			continue
		}
		// Replace non-printable non-space characters.
		if !unicode.IsPrint(r) && r != '\t' && r != '\n' && r != '\r' {
			continue
		}
		b.WriteRune(r)
	}

	// Collapse whitespace: replace runs of whitespace with a single space.
	fields := strings.Fields(b.String())
	result := strings.Join(fields, " ")

	// Truncate to max bytes on a UTF-8 boundary.
	if len(result) > MaxDescriptionBytes {
		result = result[:MaxDescriptionBytes]
		// Walk back to the last valid UTF-8 boundary.
		for len(result) > 0 && !utf8.ValidString(result) {
			result = result[:len(result)-1]
		}
	}

	return result
}

// loopbackBlocks contains standard loopback address blocks.
var loopbackBlocks = []*net.IPNet{
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("::1/128"),
}

// linkLocalBlocks contains IPv4 and IPv6 link-local blocks.
var linkLocalBlocks = []*net.IPNet{
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("fe80::/10"),
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// isLoopbackOrLinkLocal checks whether an IP string is loopback or link-local.
func isLoopbackOrLinkLocal(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return true // invalid IPs are excluded
	}
	for _, block := range loopbackBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	for _, block := range linkLocalBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// FilterIPs removes loopback, link-local, and invalid IP addresses (§11.2).
func FilterIPs(ips []string) []string {
	if len(ips) == 0 {
		return nil
	}
	out := make([]string, 0, len(ips))
	seen := make(map[string]struct{})
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if isLoopbackOrLinkLocal(ip) {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue // deduplicate
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	SortIPs(out)
	return out
}

// SortIPs sorts IPv4 before IPv6, each family in ascending lexical order (§11.2).
func SortIPs(ips []string) {
	sort.SliceStable(ips, func(i, j int) bool {
		a, b := net.ParseIP(ips[i]), net.ParseIP(ips[j])
		if a == nil || b == nil {
			return ips[i] < ips[j]
		}
		a4, b4 := a.To4() != nil, b.To4() != nil
		if a4 != b4 {
			return a4 // IPv4 before IPv6
		}
		return ips[i] < ips[j]
	})
}

// ipFamily returns the family string ("4" or "6") for a valid IP.
func ipFamily(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if ip.To4() != nil {
		return FamilyIPv4
	}
	return FamilyIPv6
}
