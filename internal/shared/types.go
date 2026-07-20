// Package shared defines the normalized inventory types, metric constants,
// and utility functions used by both the exporter and the processing backend.
// These types form the API contract (§17.2) and Confluence contract.
package shared

import "time"

// --- Top-level API response ---

// NormalizedInventory is the root JSON response for GET /api/inventory (§17.2).
type NormalizedInventory struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Geos          []Geo     `json:"geos"`
}

// Geo groups inventory by geographic location (§5.2).
type Geo struct {
	Name            string         `json:"name"`
	Hosts           []Host         `json:"hosts"`
	VirtualMachines []VMResource   `json:"virtual_machines"`
	LXDContainers   []LXDContainer `json:"lxd_containers"`
}

// --- Host ---

// Host represents a physical or virtualisation host (§9, §10).
type Host struct {
	ID               string        `json:"id"`
	Description      string        `json:"description"`
	Platform         string        `json:"platform"`
	Geo              string        `json:"geo"`
	Hostname         string        `json:"hostname"`
	OSName           string        `json:"os_name"`
	OSVersion        string        `json:"os_version"`
	Kernel           string        `json:"kernel"`
	Architecture     string        `json:"architecture"`
	IPs              []string      `json:"ips"`
	CPU              CPUInfo       `json:"cpu"`
	Memory           MemoryInfo    `json:"memory"`
	Disks            []DiskGroup   `json:"disks"`
	Filesystems      []Filesystem  `json:"filesystems"`
	StoragePools     []StoragePool `json:"storage_pools"`
	ObservationState string        `json:"observation_state"` // "current" or "retained"
	LastSeen         *time.Time    `json:"last_seen,omitempty"`
}

// CPUInfo represents host CPU specification and current usage (§11).
type CPUInfo struct {
	Model               string  `json:"model"`
	Sockets             int     `json:"sockets"`
	Cores               int     `json:"cores"`
	Threads             int     `json:"threads"`
	UsageRatio          *float64 `json:"usage_ratio,omitempty"`           // nil when usage is unavailable
	UsedThreadEquiv     *float64 `json:"used_thread_equivalents,omitempty"`
	FreeThreadEquiv     *float64 `json:"free_thread_equivalents,omitempty"`
}

// MemoryInfo represents host memory specification and usage (§12).
type MemoryInfo struct {
	TotalBytes          int64          `json:"total_bytes"`
	AvailableBytes      *int64         `json:"available_bytes,omitempty"`
	UsedBytes           *int64         `json:"used_bytes,omitempty"`
	HugepagesTotalBytes int64          `json:"hugepages_total_bytes"`
	HugepagesFreeBytes  *int64         `json:"hugepages_free_bytes,omitempty"`
	Hugepages           []HugepagePool `json:"hugepages"`
}

// HugepagePool represents one hugepage page-size pool (§11.3).
type HugepagePool struct {
	PageSizeBytes int64  `json:"page_size_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	FreeBytes     *int64 `json:"free_bytes,omitempty"`
}

// DiskGroup represents grouped physical disks (§16.3).
type DiskGroup struct {
	SizeBytes int64 `json:"size_bytes"`
	Count     int   `json:"count"`
}

// PhysicalDisk is the ungrouped raw record used internally by the normalizer.
type PhysicalDisk struct {
	DeviceID   string `json:"-"`
	DeviceName string `json:"device_name"`
	Model      string `json:"model"`
	SizeBytes  int64  `json:"size_bytes"`
}

// Filesystem represents a deduplicated local filesystem (§13, §16.4).
type Filesystem struct {
	FilesystemID   string   `json:"filesystem_id"`
	FilesystemType string   `json:"filesystem_type"` // "ext4" or "btrfs"
	Mountpoints    []string `json:"mountpoints"`
	TotalBytes     int64    `json:"total_bytes"`
	AvailableBytes *int64   `json:"available_bytes,omitempty"`
	UsedBytes      *int64   `json:"used_bytes,omitempty"`
}

// StoragePool represents a platform storage pool or datastore (§12.6, §14).
type StoragePool struct {
	PoolID         string `json:"pool_id"`
	PoolName       string `json:"pool_name"`
	PoolType       string `json:"pool_type"`
	TotalBytes     int64  `json:"total_bytes"`
	AvailableBytes *int64 `json:"available_bytes,omitempty"`
	UsedBytes      *int64 `json:"used_bytes,omitempty"`
}

// --- Resources (VMs and containers) ---

// VMResource represents a KVM/libvirt or ESXi virtual machine (§15, §16).
type VMResource struct {
	InventoryID      string    `json:"inventory_id"`
	HostID           string    `json:"host_id"`
	Name             string    `json:"name"`
	Kind             string    `json:"kind"` // "libvirt_vm" or "esxi_vm"
	Platform         string    `json:"platform"` // "KVM" or "ESXi"
	Geo              string    `json:"geo"`
	Description      string    `json:"description"`
	GuestOS          string    `json:"guest_os"`
	Architecture     string    `json:"architecture"`
	IPs              []string  `json:"ips"`
	CPUCount         int64     `json:"cpu_count"`
	MemoryBytes      int64     `json:"memory_bytes"`
	Disks            []ResourceDisk `json:"disks"`
	DiskTotalBytes   int64     `json:"disk_total_bytes"`
	CapacitySourceCPU    string `json:"capacity_source_cpu"`
	CapacitySourceRAM    string `json:"capacity_source_ram"`
	CapacitySourceDisk   string `json:"capacity_source_disk"`
	ObservationState string    `json:"observation_state"`
	LastSeen         *time.Time `json:"last_seen,omitempty"`
}

// LXDContainer represents a LXD container (§10.3, §16).
type LXDContainer struct {
	InventoryID      string    `json:"inventory_id"`
	HostID           string    `json:"host_id"`
	Name             string    `json:"name"`
	Kind             string    `json:"kind"` // "lxd_container"
	Geo              string    `json:"geo"`
	Description      string    `json:"description"`
	GuestOS          string    `json:"guest_os"`
	Architecture     string    `json:"architecture"`
	IPs              []string  `json:"ips"`
	CPUCount         int64     `json:"cpu_count"`
	MemoryBytes      int64     `json:"memory_bytes"`
	RootDiskBytes    int64     `json:"root_disk_bytes"`
	BackingPool      string    `json:"backing_pool"`
	CapacitySourceCPU    string `json:"capacity_source_cpu"`
	CapacitySourceRAM    string `json:"capacity_source_ram"`
	CapacitySourceDisk   string `json:"capacity_source_disk"`
	ObservationState string    `json:"observation_state"`
	LastSeen         *time.Time `json:"last_seen,omitempty"`
}

// ResourceDisk represents a configured virtual disk (§15).
type ResourceDisk struct {
	DiskID         string `json:"disk_id"`
	DiskName       string `json:"disk_name"`
	SizeBytes      int64  `json:"size_bytes"`
	CapacitySource string `json:"capacity_source"`
}

// --- Platform and inventory constants ---

// Platform values (§12.1).
const (
	PlatformLinux = "linux"
	PlatformKVM   = "kvm"
	PlatformLXD   = "lxd"
	PlatformESXi  = "esxi"
)

// Resource kinds (§12.7).
const (
	KindLibvirtVM    = "libvirt_vm"
	KindEsxiVM       = "esxi_vm"
	KindLXDContainer = "lxd_container"
)

// Capacity source values (§12.7).
const (
	CapacityConfigured  = "configured"
	CapacityHost        = "host-capacity"
	CapacityStoragePool = "storage-pool"
	CapacityUnknown     = "unknown"
)

// Storage pool type values (§12.6).
const (
	PoolTypeLibvirtLVM = "libvirt-lvm"
	PoolTypeLXDBtrfs   = "lxd-btrfs"
	PoolTypeLXDLVM     = "lxd-lvm"
	PoolTypeLXDZFS     = "lxd-zfs"
	PoolTypeLXDDir     = "lxd-dir"
	PoolTypeESXiDS     = "esxi-datastore"
)

// Filesystem types (§13.1).
const (
	FSTypeExt4  = "ext4"
	FSTypeBtrfs = "btrfs"
)

// Observation states (§5.4).
const (
	ObservationCurrent  = "current"
	ObservationRetained = "retained"
)

// UI display constants.
const (
	DefaultGeo = "general"
	UnknownValue = "—" // em dash for missing values
)

// Collection defaults.
const (
	DefaultCollectionInterval  = 15 * time.Minute
	DefaultCollectionTimeout   = 5 * time.Minute
	DefaultMaxSnapshotAge      = 8 * time.Hour
	DefaultRefreshInterval     = 10 * time.Minute
	UILivenessWindow           = 1 * time.Hour
	ConfluenceLivenessWindow   = 8 * time.Hour
	CPUSmoothingWindow         = 30 * time.Minute
	DiskGroupingTolerance      = 0.08
	MaxDescriptionBytes        = 1024
)
