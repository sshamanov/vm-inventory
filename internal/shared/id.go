package shared

import "fmt"

// StableID constructs the internal resource identity per §11.4:
//
//	host_id + resource_kind + platform_source_id
//
// Examples:
//
//	hv-kvm-01 + libvirt-vm + domain-uuid
//	esxi-01   + esxi-vm    + platform-vm-id
//	lxd-01    + lxd        + project/name
//
// Internal IDs are never shown in the UI or Confluence (§11.4).
func StableID(hostID, resourceKind, platformSourceID string) string {
	return fmt.Sprintf("%s:%s:%s", hostID, resourceKind, platformSourceID)
}

// HostStableID constructs a stable host identity for the observation index.
func HostStableID(hostID string) string {
	return hostID
}
