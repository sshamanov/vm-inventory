package linux

import "testing"

// TestParseLXDInstancesIdentityAndFiltering covers the parse path against a
// verbatim-shaped `lxc list --format json` payload: only running instances are
// kept, and each one carries the volatile.uuid that forms its identity.
func TestParseLXDInstancesIdentityAndFiltering(t *testing.T) {
	out := []byte(`[
	  {
	    "name": "build-03",
	    "status": "Running",
	    "config": {"volatile.uuid": "6b4b1c8e-0f52-4d5a-9a3e-2f3c9d1e7a55", "image.description": "Ubuntu 22.04"},
	    "expanded_config": {"volatile.uuid": "6b4b1c8e-0f52-4d5a-9a3e-2f3c9d1e7a55", "image.description": "Ubuntu 22.04"},
	    "state": {"network": {"eth0": {"addresses": [{"address": "10.0.0.50"}]}}}
	  },
	  {
	    "name": "stopped",
	    "status": "Stopped",
	    "config": {"volatile.uuid": "00000000-0000-0000-0000-000000000000"},
	    "state": {"network": {}}
	  },
	  {
	    "name": "legacy",
	    "status": "Running",
	    "config": {"image.description": "Ubuntu 18.04"},
	    "expanded_config": {"image.description": "Ubuntu 18.04"},
	    "state": {"network": {}}
	  },
	  {
	    "name": "expanded-only",
	    "status": "Running",
	    "config": {},
	    "expanded_config": {"volatile.uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
	    "state": {"network": {"eth0": {"addresses": [{"address": "10.0.0.51"}, {"address": "fe80::1"}]}}}
	  }
	]`)

	instances, err := parseLXDInstances(out)
	if err != nil {
		t.Fatalf("parseLXDInstances: %v", err)
	}
	if len(instances) != 3 {
		t.Fatalf("instances = %d, want 3 (stopped instance excluded)", len(instances))
	}

	if got := instances[0].InstanceUUID; got != "6b4b1c8e-0f52-4d5a-9a3e-2f3c9d1e7a55" {
		t.Errorf("InstanceUUID = %q, want the volatile.uuid", got)
	}
	if got := instances[0].OSName; got != "Ubuntu 22.04" {
		t.Errorf("OSName = %q, want the image description", got)
	}
	if got := instances[0].IPs; len(got) != 1 || got[0] != "10.0.0.50" {
		t.Errorf("IPs = %v, want [10.0.0.50]", got)
	}

	// Pre-4.9 instance: no volatile.uuid, so callers fall back to project/name.
	if got := instances[1].InstanceUUID; got != "" {
		t.Errorf("InstanceUUID = %q, want empty for an instance without volatile.uuid", got)
	}

	// The expanded config is a fallback source for the key.
	if got := instances[2].InstanceUUID; got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("InstanceUUID = %q, want the expanded_config value", got)
	}
	if got := instances[2].IPs; len(got) != 2 {
		t.Errorf("IPs = %v, want both reported addresses", got)
	}
}

func TestParseLXDInstancesRejectsMalformedJSON(t *testing.T) {
	if _, err := parseLXDInstances([]byte("not json")); err == nil {
		t.Error("parseLXDInstances(malformed) = nil error, want an error")
	}
}
