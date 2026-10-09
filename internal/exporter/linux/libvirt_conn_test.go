package linux

import (
	"reflect"
	"testing"

	"vm-inventory/internal/shared"
)

// realDomifaddrOutput is verbatim `virsh domifaddr <domain> --source agent` output:
// addresses carry a CIDR prefix and the MAC column must not be mistaken for an IP.
const realDomifaddrOutput = ` Name       MAC address          Protocol     Address
-------------------------------------------------------------------------------
 lo         00:00:00:00:00:00    ipv4         127.0.0.1/8
 -          -                    ipv6         ::1/128
 eth0       52:54:00:00:00:01    ipv4         192.0.2.25/24
 -          -                    ipv6         fe80::5054:ff:fe00:1/64
`

func TestParseIPsStripsCIDRAndSkipsMAC(t *testing.T) {
	got := parseIPs(realDomifaddrOutput)
	want := []string{"127.0.0.1", "::1", "192.0.2.25", "fe80::5054:ff:fe00:1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseIPs = %v, want %v", got, want)
	}
}

func TestParseIPsEmptyAndHeader(t *testing.T) {
	if got := parseIPs(""); got != nil {
		t.Errorf("parseIPs(\"\") = %v, want nil", got)
	}
	if got := parseIPs(" Name  MAC address  Protocol  Address\n"); got != nil {
		t.Errorf("header-only output = %v, want nil", got)
	}
}

// TestDomifaddrGlobalIPSurvivesFiltering guards the regression where the CIDR
// suffix made every libvirt address fail net.ParseIP and get dropped.
func TestDomifaddrGlobalIPSurvivesFiltering(t *testing.T) {
	got := shared.FilterIPs(parseIPs(realDomifaddrOutput))
	want := []string{"192.0.2.25"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterIPs(parseIPs(...)) = %v, want %v", got, want)
	}
}
