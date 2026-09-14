package prometheus

import (
	"fmt"
	"strings"

	"vm-inventory/internal/shared"
)

// Pre-built PromQL queries for all metric families.

// QueryAllInventory returns all inventory metrics for startup reconstruction (§14.3).
func QueryAllInventory() string {
	return `{__name__=~"inventory_.+"}`
}

// QueryAllInventoryRange returns all inventory metrics over a time range.
func QueryAllInventoryRange(lookback string) string {
	return fmt.Sprintf(`{__name__=~"inventory_.+"}[%s]`, lookback)
}

// QueryHostInfo returns all host identity records.
func QueryHostInfo() string {
	return shared.MetricHostInfo
}

// QueryHostIPs returns all host IP records.
func QueryHostIPs() string {
	return shared.MetricHostIPInfo
}

// QueryCPUSmoothed returns the 30-minute average CPU usage ratio (§16.1).
func QueryCPUSmoothed(hostID string) string {
	return fmt.Sprintf(`avg_over_time(%s{host_id="%s"}[30m])`,
		shared.MetricHostCPUUsageRatio, hostID)
}

// QueryCollectorSnapshots returns collector snapshot timestamps for pairing (§14.3).
func QueryCollectorSnapshots() string {
	return shared.MetricCollectorSnapshotTS
}

// QueryAllCurrent returns instant queries for all metric families.
func QueryAllCurrent() []string {
	return []string{
		shared.MetricHostInfo,
		shared.MetricHostIPInfo,
		shared.MetricHostCPUInfo,
		shared.MetricHostCPUSockets,
		shared.MetricHostCPUCores,
		shared.MetricHostCPUThreads,
		shared.MetricHostCPUUsageRatio,
		shared.MetricHostMemoryTotalBytes,
		shared.MetricHostMemoryAvailableBytes,
		shared.MetricHostHugepagesTotalBytes,
		shared.MetricHostHugepagesFreeBytes,
		shared.MetricHostBlockDeviceInfo,
		shared.MetricHostBlockDeviceBytes,
		shared.MetricHostFilesystemInfo,
		shared.MetricHostFilesystemMountInfo,
		shared.MetricHostFilesystemTotalBytes,
		shared.MetricHostFilesystemAvailBytes,
		shared.MetricHostStoragePoolInfo,
		shared.MetricHostStoragePoolTotalBytes,
		shared.MetricHostStoragePoolAvailBytes,
		shared.MetricResourceInfo,
		shared.MetricResourceIPInfo,
		shared.MetricResourceCPUCount,
		shared.MetricResourceMemoryBytes,
		shared.MetricResourceDiskBytes,
		shared.MetricCollectorUp,
		shared.MetricCollectorLastSuccess,
		shared.MetricCollectorSnapshotTS,
	}
}

// --- Decoded record types ---

// HostInfoRecord represents a decoded inventory_host_info metric.
type HostInfoRecord struct {
	HostID       string
	Description  string
	Geo          string
	Platform     string
	Hostname     string
	OSName       string
	OSVersion    string
	Kernel       string
	Architecture string
}

// HostIPRecord represents a decoded inventory_host_ip_info metric.
type HostIPRecord struct {
	HostID  string
	Address string
	Family  string
}

// HostCPUSocketsRecord represents a decoded inventory_host_cpu_sockets metric.
type HostCPUSocketsRecord struct {
	HostID  string
	Sockets float64
}

// HostCPUCoresRecord represents a decoded inventory_host_cpu_cores metric.
type HostCPUCoresRecord struct {
	HostID string
	Cores  float64
}

// HostCPUThreadsRecord represents a decoded inventory_host_cpu_threads metric.
type HostCPUThreadsRecord struct {
	HostID  string
	Threads float64
}

// HostCPUUsageRecord represents a decoded inventory_host_cpu_usage_ratio metric.
type HostCPUUsageRecord struct {
	HostID     string
	UsageRatio float64
}

// HostMemoryRecord represents decoded memory metrics.
type HostMemoryRecord struct {
	HostID    string
	TotalBytes float64
}

// HostMemoryAvailRecord represents decoded available memory.
type HostMemoryAvailRecord struct {
	HostID       string
	AvailBytes float64
}

// HugepageRecord represents a decoded hugepage metric.
type HugepageRecord struct {
	HostID       string
	PageSizeBytes string
	TotalBytes   float64
	FreeBytes    float64
}

// BlockDeviceRecord represents a decoded block device metric.
type BlockDeviceRecord struct {
	HostID   string
	DeviceID string
	DeviceName string
	Model    string
	SizeBytes float64
}

// FilesystemRecord represents decoded filesystem metrics.
type FilesystemRecord struct {
	HostID        string
	FilesystemID  string
	FilesystemType string
	Mountpoint    string
	TotalBytes    float64
	AvailBytes    float64
}

// StoragePoolRecord represents decoded storage pool metrics.
type StoragePoolRecord struct {
	HostID    string
	PoolID    string
	PoolName  string
	PoolType  string
	TotalBytes float64
	AvailBytes float64
}

// ResourceInfoRecord represents a decoded inventory_resource_info metric.
type ResourceInfoRecord struct {
	InventoryID  string
	HostID       string
	Name         string
	Title        string
	Kind         string
	Description  string
	GuestOS      string
	Architecture string
}

// ResourceIPRecord represents a decoded inventory_resource_ip_info metric.
type ResourceIPRecord struct {
	InventoryID string
	Address     string
	Family      string
}

// ResourceCPUCountRecord represents decoded resource CPU count.
type ResourceCPUCountRecord struct {
	InventoryID      string
	Count            float64
	CapacitySource   string
}

// ResourceMemoryRecord represents decoded resource memory.
type ResourceMemoryRecord struct {
	InventoryID      string
	MemoryBytes      float64
	CapacitySource   string
}

// ResourceDiskRecord represents decoded resource disk.
type ResourceDiskRecord struct {
	InventoryID      string
	DiskID           string
	DiskName         string
	SizeBytes        float64
	CapacitySource   string
	SourceName       string
}

// CollectorHealthRecord represents decoded collector health.
type CollectorHealthRecord struct {
	ExporterHost string
	Collector    string
	SourceHost   string
	Up           float64
	SnapshotTS   float64
}

// DecodeHostInfo extracts a HostInfoRecord from a MetricResult.
func DecodeHostInfo(m MetricResult) HostInfoRecord {
	return HostInfoRecord{
		HostID:       m.Metric[shared.LabelHostID],
		Description:  m.Metric[shared.LabelDescription],
		Geo:          m.Metric[shared.LabelGeo],
		Platform:     m.Metric[shared.LabelPlatform],
		Hostname:     m.Metric[shared.LabelHostname],
		OSName:       m.Metric[shared.LabelOSName],
		OSVersion:    m.Metric[shared.LabelOSVersion],
		Kernel:       m.Metric[shared.LabelKernel],
		Architecture: m.Metric[shared.LabelArchitecture],
	}
}

// DecodeHostIP extracts a HostIPRecord from a MetricResult.
func DecodeHostIP(m MetricResult) HostIPRecord {
	return HostIPRecord{
		HostID:  m.Metric[shared.LabelHostID],
		Address: m.Metric[shared.LabelAddress],
		Family:  m.Metric[shared.LabelFamily],
	}
}

// DecodeResourceInfo extracts a ResourceInfoRecord from a MetricResult.
func DecodeResourceInfo(m MetricResult) ResourceInfoRecord {
	return ResourceInfoRecord{
		InventoryID:  m.Metric[shared.LabelInventoryID],
		HostID:       m.Metric[shared.LabelHostID],
		Name:         m.Metric[shared.LabelName],
		Title:        m.Metric[shared.LabelTitle],
		Kind:         m.Metric[shared.LabelKind],
		Description:  m.Metric[shared.LabelDescription],
		GuestOS:      m.Metric[shared.LabelGuestOS],
		Architecture: m.Metric[shared.LabelArchitecture],
	}
}

// DecodeBlockDevice extracts a BlockDeviceRecord.
func DecodeBlockDevice(m MetricResult, metricName string) BlockDeviceRecord {
	r := BlockDeviceRecord{
		HostID:     m.Metric[shared.LabelHostID],
		DeviceID:   m.Metric[shared.LabelDeviceID],
		DeviceName: m.Metric[shared.LabelDeviceName],
		Model:      m.Metric[shared.LabelModel],
	}
	if metricName == shared.MetricHostBlockDeviceBytes {
		r.SizeBytes = ParseValue(m)
	}
	return r
}

// DecodeFilesystem extracts a FilesystemRecord.
func DecodeFilesystem(m MetricResult, metricName string) FilesystemRecord {
	r := FilesystemRecord{
		HostID:         m.Metric[shared.LabelHostID],
		FilesystemID:   m.Metric[shared.LabelFilesystemID],
		FilesystemType: m.Metric[shared.LabelFilesystemType],
		Mountpoint:     m.Metric[shared.LabelMountpoint],
	}
	switch metricName {
	case shared.MetricHostFilesystemTotalBytes:
		r.TotalBytes = ParseValue(m)
	case shared.MetricHostFilesystemAvailBytes:
		r.AvailBytes = ParseValue(m)
	}
	return r
}

// DecodeStoragePool extracts a StoragePoolRecord.
func DecodeStoragePool(m MetricResult, metricName string) StoragePoolRecord {
	r := StoragePoolRecord{
		HostID:   m.Metric[shared.LabelHostID],
		PoolID:   m.Metric[shared.LabelPoolID],
		PoolName: m.Metric[shared.LabelPoolName],
		PoolType: m.Metric[shared.LabelPoolType],
	}
	switch metricName {
	case shared.MetricHostStoragePoolTotalBytes:
		r.TotalBytes = ParseValue(m)
	case shared.MetricHostStoragePoolAvailBytes:
		r.AvailBytes = ParseValue(m)
	}
	return r
}

// ParseValue extracts the numeric value from a Prometheus result.
func ParseValue(m MetricResult) float64 {
	if len(m.Value) >= 2 {
		if s, ok := m.Value[1].(string); ok {
			// Parse string value.
			var v float64
			fmt.Sscanf(s, "%f", &v)
			return v
		}
		if f, ok := m.Value[1].(float64); ok {
			return f
		}
	}
	return 0
}

// MetricsByName groups MetricResults by metric name.
func MetricsByName(results []MetricResult) map[string][]MetricResult {
	grouped := make(map[string][]MetricResult)
	for _, r := range results {
		name := r.Metric["__name__"]
		grouped[name] = append(grouped[name], r)
	}
	return grouped
}

// SplitHostRecords splits decoded records belonging to the same host.
func SplitHostRecords(records []HostInfoRecord) map[string]HostInfoRecord {
	m := make(map[string]HostInfoRecord)
	for _, r := range records {
		m[r.HostID] = r
	}
	return m
}

// SplitResourceRecords splits decoded resource records by inventory ID.
func SplitResourceRecords(records []ResourceInfoRecord) map[string]ResourceInfoRecord {
	m := make(map[string]ResourceInfoRecord)
	for _, r := range records {
		m[r.InventoryID] = r
	}
	return m
}

// GroupByHost groups a slice of records by host ID using the provided key function.
func GroupByHost[T any](items []T, keyFn func(T) string) map[string][]T {
	result := make(map[string][]T)
	for _, item := range items {
		k := keyFn(item)
		result[k] = append(result[k], item)
	}
	return result
}

// contains checks if a string slice contains a value.
func contains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}

// FirstNonEmpty returns the first non-empty string from the given list.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
