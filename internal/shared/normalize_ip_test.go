package shared

import (
	"fmt"
	"reflect"
	"testing"
)

func TestFilterIPs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"empty", nil, nil},
		{"nil input", nil, nil},
		{"single valid IPv4", []string{"192.168.1.1"}, []string{"192.168.1.1"}},
		{"single valid IPv6", []string{"2001:db8::1"}, []string{"2001:db8::1"}},
		{
			"loopback IPv4 excluded",
			[]string{"127.0.0.1", "192.168.1.1"},
			[]string{"192.168.1.1"},
		},
		{
			"loopback IPv6 excluded",
			[]string{"::1", "2001:db8::1"},
			[]string{"2001:db8::1"},
		},
		{
			"link-local IPv4 excluded",
			[]string{"169.254.1.1", "192.168.1.1"},
			[]string{"192.168.1.1"},
		},
		{
			"link-local IPv6 excluded",
			[]string{"fe80::1", "2001:db8::1"},
			[]string{"2001:db8::1"},
		},
		{
			"mixed IPv4 and IPv6 sorted",
			[]string{"2001:db8::1", "192.168.1.1", "10.0.0.1"},
			[]string{"10.0.0.1", "192.168.1.1", "2001:db8::1"},
		},
		{
			"deduplication",
			[]string{"192.168.1.1", "192.168.1.1", "10.0.0.1"},
			[]string{"10.0.0.1", "192.168.1.1"},
		},
		{
			"invalid IP excluded",
			[]string{"not-an-ip", "192.168.1.1"},
			[]string{"192.168.1.1"},
		},
		{
			"empty string excluded",
			[]string{"", "192.168.1.1"},
			[]string{"192.168.1.1"},
		},
		{
			"whitespace-only excluded",
			[]string{"  ", "192.168.1.1"},
			[]string{"192.168.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterIPs(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FilterIPs(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSortIPs(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			"IPv4 before IPv6",
			[]string{"2001:db8::1", "192.168.1.1", "10.0.0.1"},
			[]string{"10.0.0.1", "192.168.1.1", "2001:db8::1"},
		},
		{
			"IPv4 ascending",
			[]string{"192.168.1.1", "10.0.0.1"},
			[]string{"10.0.0.1", "192.168.1.1"},
		},
		{
			"IPv6 ascending",
			[]string{"2001:db8::2", "2001:db8::1"},
			[]string{"2001:db8::1", "2001:db8::2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SortIPs(tt.input)
			if !reflect.DeepEqual(tt.input, tt.want) {
				t.Errorf("SortIPs produced %v, want %v", tt.input, tt.want)
			}
		})
	}
}

func TestSelectIPsWithinBoundUnchanged(t *testing.T) {
	in := []string{"10.0.0.1", "10.0.0.2"}
	got := SelectIPs("vm-01_10.0.0.1", in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("SelectIPs within bound = %v, want unchanged %v", got, in)
	}
}

// TestSelectIPsManagementAddressFirst reproduces the load-generator case: a VM
// holding whole address blocks, whose management address is encoded in its name.
// The management address must be emitted first and survive the cap, and the
// remaining slots must be the lowest of the other addresses.
func TestSelectIPsManagementAddressFirst(t *testing.T) {
	const management = "203.0.113.5"
	var in []string
	for i := 1; i <= 40; i++ {
		in = append(in, fmt.Sprintf("198.51.100.%d", i))
	}
	in = append(in, management)
	SortIPs(in) // callers pass FilterIPs output, which is already sorted
	// The management address sorts last of all, so only the name rule keeps it.
	if in[len(in)-1] != management {
		t.Fatalf("test setup: %s should sort last, got %v", management, in)
	}

	got := SelectIPs("loadgen-01_"+management, in)

	if len(got) != MaxResourceIPs {
		t.Fatalf("len = %d, want %d", len(got), MaxResourceIPs)
	}
	if got[0] != management {
		t.Errorf("got[0] = %q, want management address %q", got[0], management)
	}

	rest := make([]string, 0, len(in))
	for _, ip := range in {
		if ip != management {
			rest = append(rest, ip)
		}
	}
	want := append([]string{management}, rest[:MaxResourceIPs-1]...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SelectIPs = %v, want %v", got, want)
	}
}

// TestSelectIPsReordersWithinBound checks that management-first applies even
// when no truncation is needed.
func TestSelectIPsReordersWithinBound(t *testing.T) {
	got := SelectIPs("vm-01_10.0.0.9", []string{"10.0.0.1", "10.0.0.9", "10.0.0.2"})
	want := []string{"10.0.0.9", "10.0.0.1", "10.0.0.2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SelectIPs = %v, want %v", got, want)
	}
}

func TestIsLoopbackOrLinkLocal(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"169.254.0.1", true},
		{"169.254.255.255", true},
		{"fe80::1", true},
		{"fe80::dead:beef", true},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"2001:db8::1", false},
		{"", true},          // invalid
		{"not-an-ip", true}, // invalid
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := isLoopbackOrLinkLocal(tt.ip)
			if got != tt.want {
				t.Errorf("isLoopbackOrLinkLocal(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
