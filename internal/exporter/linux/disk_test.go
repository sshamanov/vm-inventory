package linux

import (
	"testing"
)

func TestParseMountsSizes(t *testing.T) {
	mounts, err := parseMounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) == 0 {
		t.Skip("no real filesystems found in test environment")
	}
	for _, m := range mounts {
		if m.totalBytes == 0 {
			t.Errorf("filesystem %s on %s (type %s): totalBytes is 0 — statfs may have failed", m.device, m.mountpoint, m.fsType)
		}
	}
}

func TestParseMountsFiltersPseudo(t *testing.T) {
	mounts, err := parseMounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mounts {
		if hasPrefix(m.mountpoint, "/proc/", "/sys/", "/dev/", "/run/", "/snap/", "/var/lib/docker/", "/var/lib/lxc") {
			t.Errorf("pseudo mount %s should have been filtered out", m.mountpoint)
		}
	}
}

func hasPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}
