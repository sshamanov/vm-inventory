package shared

// Metric name constants — the binding contract from ARCHITECTURE.md §12.
// These must not be changed without updating the architecture document.

const (
	// §12.1 Host identity.
	MetricHostInfo   = "inventory_host_info"
	MetricHostIPInfo = "inventory_host_ip_info"

	// §12.2 Host CPU.
	MetricHostCPUInfo       = "inventory_host_cpu_info"
	MetricHostCPUSockets    = "inventory_host_cpu_sockets"
	MetricHostCPUCores      = "inventory_host_cpu_cores"
	MetricHostCPUThreads    = "inventory_host_cpu_threads"
	MetricHostCPUUsageRatio = "inventory_host_cpu_usage_ratio"

	// §12.3 Host memory.
	MetricHostMemoryTotalBytes     = "inventory_host_memory_total_bytes"
	MetricHostMemoryAvailableBytes = "inventory_host_memory_available_bytes"
	MetricHostHugepagesTotalBytes  = "inventory_host_hugepages_total_bytes"
	MetricHostHugepagesFreeBytes   = "inventory_host_hugepages_free_bytes"

	// §12.4 Physical disks.
	MetricHostBlockDeviceInfo  = "inventory_host_block_device_info"
	MetricHostBlockDeviceBytes = "inventory_host_block_device_bytes"

	// §12.5 Filesystems.
	MetricHostFilesystemInfo        = "inventory_host_filesystem_info"
	MetricHostFilesystemMountInfo   = "inventory_host_filesystem_mount_info"
	MetricHostFilesystemTotalBytes  = "inventory_host_filesystem_total_bytes"
	MetricHostFilesystemAvailBytes  = "inventory_host_filesystem_available_bytes"

	// §12.6 Platform storage pools and datastores.
	MetricHostStoragePoolInfo        = "inventory_host_storage_pool_info"
	MetricHostStoragePoolTotalBytes  = "inventory_host_storage_pool_total_bytes"
	MetricHostStoragePoolAvailBytes  = "inventory_host_storage_pool_available_bytes"

	// §12.7 Resources.
	MetricResourceInfo       = "inventory_resource_info"
	MetricResourceIPInfo     = "inventory_resource_ip_info"
	MetricResourceCPUCount   = "inventory_resource_cpu_count"
	MetricResourceMemoryBytes = "inventory_resource_memory_bytes"
	MetricResourceDiskBytes  = "inventory_resource_disk_bytes"

	// §12.8 Collector health.
	MetricCollectorUp             = "inventory_collector_up"
	MetricCollectorLastSuccess    = "inventory_collector_last_success_timestamp_seconds"
	MetricCollectorSnapshotTS     = "inventory_collector_snapshot_timestamp_seconds"
	MetricCollectorSnapshotExpiry = "inventory_collector_snapshot_expires_timestamp_seconds"
	MetricCollectorSnapshotValid  = "inventory_collector_snapshot_valid"
)

// Label name constants — the binding contract from ARCHITECTURE.md §12.
const (
	LabelHostID        = "host_id"
	LabelDescription   = "description"
	LabelGeo           = "geo"
	LabelPlatform      = "platform"
	LabelHostname      = "hostname"
	LabelOSName        = "os_name"
	LabelOSVersion     = "os_version"
	LabelKernel        = "kernel"
	LabelArchitecture  = "architecture"
	LabelAddress       = "address"
	LabelFamily        = "family"
	LabelModel         = "model"
	LabelPageSizeBytes = "page_size_bytes"
	LabelDeviceID      = "device_id"
	LabelDeviceName    = "device_name"
	LabelFilesystemID  = "filesystem_id"
	LabelFilesystemType = "filesystem_type"
	LabelMountpoint    = "mountpoint"
	LabelPoolID        = "pool_id"
	LabelPoolName      = "pool_name"
	LabelPoolType      = "pool_type"
	LabelInventoryID   = "inventory_id"
	LabelName          = "name"
	LabelKind          = "kind"
	LabelGuestOS       = "guest_os"
	LabelDiskID        = "disk_id"
	LabelDiskName      = "disk_name"
	LabelCapacitySource = "capacity_source"
	LabelSourceName    = "source_name"
	LabelExporterHost  = "exporter_host"
	LabelCollector     = "collector"
	LabelSourceHost    = "source_host"
)

// Label value constants.
const (
	FamilyIPv4 = "4"
	FamilyIPv6 = "6"
)
