# Infrastructure Inventory — System Architecture

## 1. System Overview

Infrastructure Inventory consists of:

1. A Go inventory exporter binary with mutually exclusive Linux and ESXi modes.
2. Prometheus as the only data source used by the processing application.
3. A processing backend that queries Prometheus, reconstructs recent observations, normalizes data, serves a JSON API, serves static frontend assets, and publishes Confluence.
4. A plain client-side HTML/CSS/JavaScript UI.
5. One generated Confluence page.

Docker inventory is not part of V1.

```text
Linux hosts                         Standalone ESXi hosts
  inventory-exporter                  inventory-exporter in ESXi mode
  mode: linux                         external Docker-ready process
        \                              /
         \                            /
          +------ Prometheus --------+
                       |
                       v
              Processing backend
                 - Prometheus queries
                 - observation index
                 - normalized JSON API
                 - static web UI
                 - Confluence renderer
                 - one published page
```

## 2. Technical Stack

| Component | Choice |
|---|---|
| Language | Go 1.22+ |
| Module path | `vm-inventory` |
| Libvirt client | `github.com/digitalocean/go-libvirt` (pure Go, no CGO) |
| ESXi client | `github.com/vmware/govmomi` |
| Frontend | Plain HTML, CSS, JavaScript — no framework, no build pipeline |
| Data source | Prometheus (instant and range query APIs) |
| Confluence API | Server / Data Center REST API |
| Container registry | `ghcr.io/sshamanov/` |

## 3. Goals

- Collect host, VM, and LXD inventory without querying platforms from the browser.
- Use Prometheus as the sole inventory transport and history source.
- Avoid scrape-time platform calls.
- Mask short infrastructure and collection interruptions.
- Keep exporter output platform-oriented and processing logic centralized.
- Support standalone ESXi without vCenter.
- Produce a static Linux AMD64 exporter binary where practical.
- Keep deployment simple enough for Docker Compose and direct binary execution.
- Keep the web frontend dependency-free in V1.

## 4. Non-Goals

- Docker inventory.
- Real-time monitoring.
- Alerting inside the inventory backend.
- Inventory editing.
- vCenter-specific orchestration.
- Per-guest performance metrics.
- Persistent inventory database.
- Global storage-total calculation.
- Remote or network-backed libvirt storage-pool inventory in V1.
- Guest filesystem utilization.

## 5. Architecture Decision Summary

### Selected architecture

- One Go exporter codebase and binary: `inventory-exporter`.
- Required YAML `mode`: `linux` or `esxi`.
- Linux exporters run on each Linux host.
- ESXi mode runs externally and may collect many standalone ESXi targets.
- Exporters collect in the background every 15 minutes.
- `/metrics` serves cached complete snapshots and never performs live platform collection.
- Prometheus scrapes exporters and stores current and historical observations.
- Processing backend reads Prometheus only.
- UI liveness window is 1 hour.
- Confluence liveness window is 8 hours.
- Backend rebuilds its observation index from the previous 8 hours of Prometheus history after restart.
- UI is plain client-side JavaScript consuming a normalized JSON API.
- Confluence publication is blocked when Prometheus cannot be reached.

### Why this architecture

It separates slow and failure-prone platform access from Prometheus scrapes, preserves recent inventory across maintenance windows, avoids a second inventory database, and keeps the frontend simple.

## 6. System Boundaries

### Exporter responsibilities

- Connect to local Linux facilities, libvirt, LXD, or configured ESXi targets.
- Collect complete platform snapshots in the background.
- Validate and normalize low-level source values.
- Expose raw inventory and collector-health metrics.
- Avoid business grouping, Confluence formatting, UI formatting, and cross-platform joins.

### Prometheus responsibilities

- Scrape exporter metrics.
- Retain time-series history.
- Provide instant and range query APIs.
- Alert on scrape, collector, and staleness failures.

### Processing backend responsibilities

- Query Prometheus.
- Reconstruct the latest coherent observations.
- Maintain an in-memory recent-observation index.
- Apply 1-hour and 8-hour liveness windows.
- Validate global host identity.
- Join metric families.
- Normalize platform differences.
- Group disks by approximate capacity.
- Deduplicate filesystems.
- Build host-centered geo views.
- Serve a stable JSON API.
- Serve static HTML/CSS/JS.
- Render and publish Confluence.
- Keep no state on disk (§20).

### Frontend responsibilities

- Fetch normalized JSON.
- Render the long page.
- Render bars and tables.
- Filter the existing hierarchy during search.
- Invoke refresh and Confluence publication endpoints.

The frontend must not query Prometheus or implement metric joins.

## 7. Deployment Model

### 7.1 Linux exporter

Initial target:

- Linux AMD64
- oldest supported distribution: Ubuntu 20.04
- standalone static binary where practical
- optional Docker image
- no required local package installation

The implementation should avoid CGO where possible.

### 7.2 ESXi exporter

- Docker-ready external collector
- one process may collect many standalone ESXi hosts
- no vCenter required
- per-target credentials in mounted YAML configuration

### 7.3 Processing backend

- Docker deployment
- listens on configured address (default `:8080`)
- serves API and static frontend
- reads Prometheus URL from `PROMETHEUS_URL` environment variable
- reads Confluence settings from environment variables (`CONFLUENCE_URL`, `CONFLUENCE_TOKEN`, `CONFLUENCE_PAGE_ID`)
- keeps no state on disk and mounts no volume; refresh and publication status are
  process-local and the publication hash lives on the Confluence page (§20)

### 7.4 Releases

GitHub releases should include:

- Linux AMD64 exporter binary
- Docker image: `ghcr.io/sshamanov/inventory-exporter:latest`
- Docker image: `ghcr.io/sshamanov/inventory-backend:latest`
- example configuration files
- Prometheus scrape and alerting examples

## 8. Exporter Configuration

The schemas are mutually exclusive.

### 8.1 Linux mode

```yaml
mode: linux

host:
  id: hv-kvm-01
  description: Main KVM and LXD host
  geo: belgrade

collection:
  interval: 15m
  timeout: 5m
  max_snapshot_age: 8h

collectors:
  libvirt:
    enabled: true

  lxd:
    enabled: true
```

Rules:

- `host.id` defaults to hostname if omitted.
- `host.description` defaults to empty.
- `host.geo` defaults to `general`.
- Libvirt and LXD collectors are enabled by default.
- A host not running one of them should explicitly disable it.

### 8.2 ESXi mode

```yaml
mode: esxi

collection:
  interval: 15m
  timeout: 5m
  max_snapshot_age: 8h

targets:
  - host_id: esxi-01
    host_description: Primary ESXi host
    geo: belgrade
    address: https://esxi-01.internal
    username: inventory-reader
    password: secret
    insecure_skip_verify: true
```

Rules:

- Each target has its own host identity, description, geo, credentials, and TLS policy.
- `geo` defaults to `general`.
- `insecure_skip_verify` defaults to `true` (ESXi hosts use self-signed certificates).
- Credentials must never be logged.
- Configuration should be mounted read-only with restrictive permissions.

## 9. Exporter Collection Model

### 9.1 Background collection

Each collector runs asynchronously on its configured interval.

`/metrics` serves the last complete cached snapshot.

Platform calls are never executed from the HTTP scrape handler.

### 9.2 Atomic snapshots

Linux uses independent atomic snapshots for:

- host hardware and operating-system data
- libvirt domains and logical pools
- LXD instances and storage pools

ESXi uses one atomic snapshot per target in V1:

- host identity and version
- host IPs
- CPU and RAM
- physical disks
- datastores
- powered-on VMs
- VM configuration, IPs, and guest metadata

### 9.3 No half-data publication

A collector replaces its previous snapshot only after a complete successful collection and validation pass.

If collection fails or is incomplete:

- keep serving the previous complete snapshot temporarily
- preserve its original collection timestamp
- set collector health metrics accordingly
- stop exposing that collector's inventory series when the snapshot reaches `max_snapshot_age`
- continue exposing collector health metrics after inventory expiry

The default `max_snapshot_age` is 8 hours, equivalent to 32 default collection intervals. It must never be refreshed merely because Prometheus scraped the cached metrics.

A successful empty platform response may produce an empty newest snapshot. The processing backend observation windows prevent immediate inventory removal.

### 9.4 Collection overlap

Do not run overlapping collection cycles for the same collector or ESXi target.

If an interval arrives while the previous run is active:

- skip the overlapping run
- expose a health or skipped-run counter if useful

## 10. Source Collection Requirements

## 10.1 Linux host

Collect directly from the host without depending on node_exporter for inventory.

Required sources include:

- `/etc/os-release`
- `/proc`
- `/sys`
- mount and filesystem APIs
- block-device APIs
- local networking APIs

Required host fields:

- configured host ID
- description
- geo
- hostname
- distribution and version
- kernel version
- architecture
- non-loopback, non-link-local IP addresses
- CPU model
- sockets
- physical cores
- hardware threads
- CPU usage ratio
- physical memory total
- Linux `MemAvailable`
- hugepage totals and free values by page size
- physical disks
- local ext4 and Btrfs filesystems

## 10.2 Libvirt/KVM

Collect:

- running domains only
- stable domain UUID
- name
- description or domain metadata
- configured vCPU count
- configured RAM
- configured virtual disks and capacities
- interface addresses where available
- guest operating-system information
- active logical storage pools only
- logical-pool total and free capacity

Guest OS precedence:

1. QEMU guest agent
2. libvirt domain metadata
3. unavailable

The collector should try guest-agent access first but degrade cleanly when unavailable.

The implementation must prove that the selected pure-Go libvirt client supports the required operations. Do not silently add `virsh` output parsing or CGO.

If guest-agent enrichment cannot be implemented cleanly with the selected client, omit it and fall back to metadata.

## 10.3 LXD

Collect:

- running containers only
- project and name as stable identity inputs
- instance description
- IP addresses
- architecture
- operating-system/image metadata
- explicit CPU limits
- explicit memory limits
- root-disk limit
- backing storage pool
- all active non-system storage pools
- pool driver
- pool total and available capacity

Ephemeral-instance behavior does not require special V1 handling beyond the running-resource rule.

## 10.4 ESXi

Collect from standalone ESXi APIs:

- host product name
- version
- build
- host IPs known to ESXi
- CPU model and topology
- host CPU usage ratio
- memory total and available values where supported
- physical disks
- accessible local VMFS datastores (NFS, vSAN, VVol excluded)
- powered-on VMs only
- VM stable platform ID
- VM name
- annotation/description
- configured vCPU
- configured RAM
- configured virtual disks and capacities
- guest IPs known to ESXi/VMware Tools
- guest OS

Guest OS precedence:

1. VMware Tools guest information
2. configured guest OS identifier
3. unavailable

Do not infer IPs through MAC, ARP, switch tables, or network scans.

## 11. Data Normalization Rules

### 11.1 Descriptions

Normalize descriptions before metrics emission:

- valid UTF-8
- remove control characters
- collapse whitespace
- trim
- maximum 1024 bytes
- truncate on a UTF-8 boundary

### 11.2 IP addresses

- one metric series per IP address, bounded per resource (§12.7)
- exclude loopback
- exclude link-local
- deduplicate in the processor
- sort IPv4 before IPv6
- a resource's management address (the one embedded in its name) is emitted first
- API and UI expose addresses only, without interface names

### 11.3 Unknown values

- exporter omits unavailable value metrics or provides explicit info-state labels where required
- normalized JSON uses `null`
- zero remains a real numeric zero
- UI renders `null` as `—`

### 11.4 Stable IDs

Host IDs must be globally unique.

Resource internal identity:

```text
host_id + resource_kind + platform_source_id
```

Examples:

```text
hv-kvm-01 + libvirt-vm + domain-uuid      (libvirt domain UUID, `virsh list --uuid`)
esxi-01   + esxi-vm    + vm-bios-uuid     (VMX uuid.bios)
lxd-01    + lxd        + instance-uuid    (volatile.uuid)
```

The `platform_source_id` must be an identifier the platform assigns and keeps, not
one derived from a name or a handle. Names track renames and addresses change;
a re-keyed resource loses its observation history and briefly appears twice while
the previous series is still within Prometheus' lookback window.

- **libvirt/KVM** — the domain UUID, stored in the domain XML (`<uuid>`). Stable
  across rename, reboot and re-define; regenerated on clone.
- **ESXi** — the BIOS UUID (`config.uuid`, VMX `uuid.bios`). The
  ManagedObjectReference is *not* an identity: it is allocated from a small
  sequential pool and released on delete, so a new VM can inherit a retired VM's
  reference and overwrite its record. Used only as a fallback when the BIOS UUID
  is absent, since an empty ID would collide across every such VM.
- **LXD** — the instance UUID (`volatile.uuid`), globally unique across servers
  and projects. Instances created before LXD 4.9 have no such key and fall back
  to `project/name`.

Internal IDs are never shown in the UI or Confluence.

A VM moving to another host changes its internal identity and is treated as an inventory change.

## 12. Metric Contract

The exact label set should remain bounded and stable. Per-resource address
series are explicitly bounded (§12.7).

### 12.1 Host identity

```text
inventory_host_info{host_id,description,geo,platform,hostname,os_name,os_version,kernel,architecture} 1
inventory_host_ip_info{host_id,address,family} 1
```

### 12.2 Host CPU

```text
inventory_host_cpu_info{host_id,model} 1
inventory_host_cpu_sockets{host_id} N
inventory_host_cpu_cores{host_id} N
inventory_host_cpu_threads{host_id} N
inventory_host_cpu_usage_ratio{host_id} R
```

`inventory_host_cpu_usage_ratio` is a normalized point-in-time ratio from `0` to `1`.

### 12.3 Host memory

```text
inventory_host_memory_total_bytes{host_id} N
inventory_host_memory_available_bytes{host_id} N
inventory_host_hugepages_total_bytes{host_id,page_size_bytes} N
inventory_host_hugepages_free_bytes{host_id,page_size_bytes} N
```

Linux available memory uses `MemAvailable`.

### 12.4 Physical disks

```text
inventory_host_block_device_info{host_id,device_id,device_name,model} 1
inventory_host_block_device_bytes{host_id,device_id} N
```

### 12.5 Filesystems

```text
inventory_host_filesystem_info{host_id,filesystem_id,filesystem_type} 1
inventory_host_filesystem_mount_info{host_id,filesystem_id,mountpoint} 1
inventory_host_filesystem_total_bytes{host_id,filesystem_id} N
inventory_host_filesystem_available_bytes{host_id,filesystem_id} N
```

A filesystem has one capacity identity and may have several mountpoint relationship series. This prevents Btrfs subvolumes or bind mounts from duplicating capacity.

### 12.6 Platform storage pools and datastores

```text
inventory_host_storage_pool_info{host_id,pool_id,pool_name,pool_type} 1
inventory_host_storage_pool_total_bytes{host_id,pool_id} N
inventory_host_storage_pool_available_bytes{host_id,pool_id} N
```

`pool_type` distinguishes:

- libvirt-lvm
- lxd-btrfs
- lxd-lvm
- lxd-zfs
- lxd-dir
- esxi-datastore
- other supported LXD pool types

### 12.7 Resources

```text
inventory_resource_info{
  inventory_id,
  host_id,
  name,
  title,
  kind,
  description,
  guest_os,
  architecture
} 1

inventory_resource_ip_info{inventory_id,address,family} 1
inventory_resource_cpu_count{inventory_id,capacity_source} N
inventory_resource_memory_bytes{inventory_id,capacity_source} N
inventory_resource_disk_bytes{inventory_id,disk_id,disk_name,capacity_source,source_name} N
```

Kinds:

- `libvirt_vm`
- `esxi_vm`
- `lxd_container`

`title` is an optional human-readable display name (libvirt domain title). It is
absent or empty when the platform has no title concept (ESXi, LXD). When set, the
UI shows it in place of `name`; `name` remains the stable identifier for search
and joins.

A single resource emits at most 5 `inventory_resource_ip_info` series. The bound
is applied by the exporter, because some guests legitimately hold entire address
blocks (load generators, routers) and would otherwise dominate the exporter's
series count. The resource's management address — the one embedded in the
resource name (`loadgen-01_192.0.2.120`) — is emitted first and always retained;
the remaining slots take the lowest addresses in canonical order. Resources at
or below the bound keep every address, still management-first.

Capacity-source values:

- `configured`
- `host-capacity`
- `storage-pool`
- `unknown`

### 12.8 Collector health

```text
inventory_collector_up{exporter_host,collector,source_host} 0|1
inventory_collector_last_success_timestamp_seconds{exporter_host,collector,source_host} N
inventory_collector_snapshot_timestamp_seconds{exporter_host,collector,source_host} N
inventory_collector_snapshot_expires_timestamp_seconds{exporter_host,collector,source_host} N
inventory_collector_snapshot_valid{exporter_host,collector,source_host} 0|1
```

Standard Prometheus `up` remains the scrape-level health signal.

The snapshot timestamp is the authoritative observation time for all entities in that collector snapshot. Prometheus sample timestamps are transport timestamps and must not be treated as fresh observations of cached inventory.

## 13. Prometheus Configuration

### 13.1 Scraping

Prometheus scrapes exporters at a cadence compatible with the 15-minute exporter collection interval.

A scrape interval such as 1 to 5 minutes is acceptable because scrapes are cheap cached reads.

### 13.2 Alerting

Prometheus owns alerting for:

- exporter scrape down
- collector failure
- stale last-success timestamp
- invalid collector snapshot
- missing expected exporter targets

The processing backend does not generate operational alerts.

### 13.3 No business recording rules

Do not require Prometheus recording rules for:

- joins
- grouping
- deduplication
- resource fallback
- geo grouping
- disk aggregation
- Confluence hashing

These belong in the processing backend.

## 14. Processing Backend Architecture

### 14.1 Major modules

- Prometheus client
- metric decoder
- snapshot validator
- observation-index manager
- normalizer
- host/resource joiner
- UI snapshot builder
- Confluence snapshot builder
- Confluence renderer and client
- HTTP API
- static-file server
- JSON state store

### 14.2 In-memory observation index

The backend maintains:

```text
stable ID
latest complete normalized record
last seen timestamp
last observed refresh identifier
source collector or target
```

It persists nothing (§20): the index is rebuilt from Prometheus at startup.

### 14.3 Startup recovery

On startup:

1. Query Prometheus over the previous 8 hours.
2. Pair each entity sample with its collector's `inventory_collector_snapshot_timestamp_seconds` value at that scrape.
3. Reconstruct the most recent coherent observation for each stable host and resource ID.
4. Rebuild relationships between hosts, storage, VMs, and containers.
5. Discard observations whose collector snapshot timestamp is older than 8 hours.
6. Populate the in-memory observation index.

Do not combine arbitrary values from different snapshots into one record. Prometheus scrape timestamps do not refresh the age of cached exporter data.

The reconstruction algorithm must select the most recent coherent set of metric families associated with one collector snapshot timestamp.

### 14.4 Regular refresh

On each refresh:

1. Query current Prometheus inventory metrics.
2. Validate host IDs and required relationships.
3. Normalize observed hosts and resources.
4. Read the authoritative observation time from the corresponding collector snapshot timestamp.
5. Update `last_seen` only when that snapshot timestamp is newer than the stored value.
6. Keep missing observations in the index until their liveness windows expire.
7. Build a 1-hour UI view.
8. Keep the 8-hour history available for Confluence publication.

### 14.5 Refresh interval

Default backend cache refresh interval: 10 minutes.

Exporter collection interval: 15 minutes.

The backend refresh cadence may be shorter than exporter collection because Prometheus queries are cheap and may observe newly scraped exporter snapshots promptly.

## 15. Liveness Windows

### 15.1 UI window

The UI presents three view windows, selected by the `window` parameter of
`/api/inventory` (§17.2):

| view | lookback |
|---|---|
| `now` (default) | 1 hour |
| `month` | 30 days |
| `all` | 365 days |

The view window decides how far back the snapshot looks. Whether an item found
in that window is *current* is decided by one rule that does not vary with the
view:

```text
now - last_seen <= 1 hour
```

`last_seen` is the exporter collector snapshot timestamp, not the Prometheus scrape time.

An item inside the view window but outside the liveness window is marked
retained and carries its `last_seen`; the UI dims it and labels when it was last
seen. A repeatedly scraped old exporter cache does not make the item current.

Widening the view therefore never makes a departed instance read as live: it
only reveals items that have already been retired.

**Re-labelled identities.** A series is keyed by its platform source id, so an
exporter that begins reading a more durable id — an ESXi host moving from a
managed-object reference to the guest's BIOS UUID — retires every old series of
that fleet at once while the instances keep running. As far as Prometheus is
concerned those series genuinely departed, and the backfill (§15.4) recovers
them as departures. A snapshot therefore drops a retained resource when a
current resource of the same kind and name is present on the same host: the
instance did not leave, it was re-keyed. A guest that appears on another host is
never suppressed — that is a migration, and both the departure and the arrival
are shown.

### 15.2 Confluence window

```text
publication_time - last_seen <= 8 hours
```

The Confluence snapshot includes the item. The exporter also expires cached inventory at 8 hours, providing a second boundary against indefinite stale series.

### 15.3 Usage freshness

Identity and specification may be retained.

Transient usage values are shown only from a host snapshot whose authoritative snapshot timestamp is no more than 1 hour old. The UI displays the snapshot age.

For a host retained only from older observation history or omitted by a newer logical host snapshot:

- CPU usage unavailable
- RAM usage unavailable
- filesystem free/used unavailable
- pool/datastore free/used unavailable

This prevents data older than the UI liveness window from appearing live.

The freshness window is 1 hour in every view (§15.1). A wider view does not
widen it: an item's usage is emitted only while the item is current, and a
retained item reports its identity and specification with its usage values
absent (`null`).

This holds for the Confluence snapshot too. Its eight-hour window (§15.2)
decides which items are included; the one-hour freshness window still decides
which of them are current, so an item included by its age but not observed
within the last hour is published as retained without usage readings.

### 15.4 Retention and history backfill

The observation index is the only place a departed instance can be remembered.
Prometheus keeps the raw series for its own retention period, but the index
learns from an instant query, which by definition sees only what is being
scraped now.

**Backfill.** At startup, and at the end of every refresh — the periodic cycle
and the manual refresh alike — the backend asks Prometheus for the newest sample
time of every `inventory_host_info` and `inventory_resource_info` series in the
retention window:

```text
max_over_time(timestamp(<metric>)[<lookback>:<step>])
```

`timestamp()` sits inside a subquery because `last_over_time` reports the time
of the evaluation rather than of the last sample, which would date every
departed series to now.

A subquery costs one evaluation per series per step, so the pass is tiered: the
recent week at an hourly step, then the whole retention window at a step coarse
enough to hold the evaluation count near 200. A year at an hourly step across
every resource series runs past the Prometheus client's timeout; the same year
at a 48-hour step finishes in seconds, dating an old departure to the day it was
collected.

Running the pass only at startup is not enough: a host that departs while the
process is up would be found by no view at all, and the index would have no
entry for it to dim. So a refresh repeats it, which is what makes a departure
visible from the next refresh rather than from the next restart. The startup
pass runs in the background, so that serving does not wait on it; the manual
refresh runs it before answering, so that a refresh that reports success means
the snapshot the UI reloads next is already complete.

Backfilled observations merge by last-seen and never overwrite a newer one. Each
result carries the labels of whichever series was live at the time, and a kernel
or OS upgrade re-labels the series, so applying an old result over a running
host would misreport its OS. Results within a pass are applied oldest first for
the same reason: the newest labels win the record.

**Retention.** The index prunes hourly at the retention window — the widest view
plus one day — so an instance survives for as long as the `all` view can ask for
it. This is the one part of the index that grows with elapsed time rather than
with the size of the fleet, and it is bounded by the number of identities the
fleet has used in a year.

## 16. Processing Calculations

### 16.1 CPU smoothing

Prometheus provides `inventory_host_cpu_usage_ratio`.

The backend queries a long smoothing window, default 30 minutes:

```promql
avg_over_time(inventory_host_cpu_usage_ratio[30m])
```

Then calculates:

```text
used_thread_equivalents = total_threads * smoothed_ratio
free_thread_equivalents = total_threads - used_thread_equivalents
```

Clamp ratio to `[0,1]`.

### 16.2 Linux RAM

```text
normal_free = MemAvailable
hugepage_free = sum(free hugepages by page size)
used = max(0, physical_total - normal_free - hugepage_free)
```

The UI bar is:

```text
used | hugepage_free | normal_free
```

### 16.3 Physical disk grouping

Algorithm:

1. Sort physical disks by capacity.
2. Build groups where capacity difference remains within 8% of the group reference.
3. Use the smallest disk capacity as the displayed group size.
4. Display `size × count`.
5. Keep individual records internally.

Do not group by model.

### 16.4 Filesystem deduplication

Deduplicate local ext4 and Btrfs capacity by stable backing identity.

Preferred identity sources:

- filesystem UUID
- block device major/minor
- platform-provided stable filesystem identifier

Bind mounts and Btrfs subvolumes must not duplicate backing capacity.

### 16.5 LXD capacity fallback

CPU:

- explicit count or CPU set -> configured
- otherwise -> host hardware threads

RAM:

- explicit memory limit -> configured
- otherwise -> host physical memory

Disk:

- explicit root-disk limit -> configured
- otherwise -> backing LXD pool

Every fallback retains provenance.

## 17. Normalized JSON API

The API is the stable contract for the frontend and future integrations.

### 17.1 Endpoints

```http
GET  /api/inventory
GET  /api/status
POST /api/refresh
POST /api/confluence/publish
```

### 17.2 Inventory response

```json
{
  "schema_version": 1,
  "generated_at": "2026-07-14T12:00:00Z",
  "geos": [
    {
      "name": "belgrade",
      "hosts": [],
      "virtual_machines": [],
      "lxd_containers": []
    }
  ]
}
```

The optional `window` parameter selects the view window (§15.1): `now` (the
default when the parameter is absent), `month`, or `all`. Any other value is
answered with `400 unknown_window`. Each window is rendered and validated
separately — an `ETag` from one window is never served for another.

Rules:

- byte values are integer bytes
- ratios are numeric
- timestamps are RFC 3339 UTC
- unknown fields are `null`
- arrays are deterministically sorted
- frontend performs no joins
- include `observation_state` and `last_seen`
- include capacity provenance
- a wider window contains everything a narrower one does, with
  `observation_state` and `last_seen` separating what is current from what is
  history

### 17.3 Status response

```json
{
  "cache_generated_at": "2026-07-14T12:00:00Z",
  "last_prometheus_refresh": "2026-07-14T12:00:00Z",
  "last_refresh_status": "ok",
  "last_confluence_update": "2026-07-14T06:00:00Z",
  "last_confluence_status": "unchanged"
}
```

The refresh and publication fields are process-local (§20): a restart reports
them as absent until the next refresh or publication. The inventory fields are
rebuilt immediately from Prometheus.

### 17.4 Refresh endpoint

`POST /api/refresh`:

- performs an immediate Prometheus refresh
- returns after completion
- serializes concurrent refreshes
- may return `409 refresh_in_progress`
- leaves the old cache intact on failure

### 17.5 Confluence endpoint

`POST /api/confluence/publish`:

- uses the same publication path as scheduled execution
- serializes concurrent publications
- returns `published`, `unchanged`, or `failed`

### 17.6 HTTP caching

Support `ETag` for `/api/inventory`.

Return `304 Not Modified` when the client sends a matching `If-None-Match`.

## 18. Frontend Architecture

Use plain:

- HTML
- CSS
- JavaScript

No frontend framework or build pipeline is required for V1.

The backend serves static assets.

The frontend:

- fetches `/api/inventory`
- fetches `/api/status`
- renders the page
- performs client-side search
- calls refresh and publication endpoints
- displays operational states

CORS should remain disabled. The UI and API are same-origin.

## 19. Confluence Publication Architecture

### 19.1 Target

- Confluence Server / Data Center REST API
- Page title: `VM Inventory`
- Page: identified by `CONFLUENCE_PAGE_ID`; the page must already exist
- Authentication: `CONFLUENCE_TOKEN`, a Personal Access Token sent as Bearer

### 19.2 Publication trigger

Publication may be invoked by:

- external scheduler calling the HTTP hook
- manual UI action

The scheduling mechanism is outside the application unless a simple internal timer is added later.

### 19.3 Prometheus requirement

Before publication:

1. Query Prometheus.
2. Confirm Prometheus API availability.
3. Refresh the observation index.
4. Build the 8-hour snapshot.

If Prometheus is unavailable:

- abort publication
- preserve existing Confluence content
- preserve stored hash
- preserve last successful publication timestamp

Individual exporter or collector failures do not block publication if Prometheus is reachable.

### 19.4 Canonical content hash

The hash covers the **rendered storage-format body**, not the snapshot behind it.
Two properties follow, both deliberate:

- A renderer change — a new column, a relabelled heading — is a content change
  and republishes the page. Hashing the snapshot instead would pin the published
  page to whatever layout was live when the hash was first stored, because the
  inventory underneath it had not moved.
- Volatile values cannot enter the hash, because the renderer never emits them:
  DESIGN §20.4 excludes usage, free capacity, per-item last-seen timestamps and
  current/retained state from the page.

Nothing time-varying may be added to the page. A publication timestamp would
change the body on every run, so the hash would never match and the gate would
never fire. DESIGN §20 lists a generated inventory timestamp in the page
structure; it is not rendered today, and adding it requires the hash to cover
the body with that line excluded.

An unchanged inventory must therefore render byte-identically. That requires the
renderer to walk a canonically ordered snapshot:

- geos sorted case-insensitively by name
- hosts sorted by host ID
- resources sorted by host, then name, with stable identity as the final
  tie-breaker

The hash is the SHA-256 of the rendered body, hex-encoded. It is recorded on the
page itself as the content property `inventory-hash`:

```json
{
  "hash": "<hex sha256 of the rendered body>"
}
```

The property is written after a successful page update, so a recorded hash always
describes content that is actually on the page.

### 19.5 Publication result

Publication reads the page's current version and its stored hash before writing
anything:

1. read the page version (§19.4 successor rule)
2. read the `inventory-hash` property
3. render the current 8-hour snapshot and hash it
4. write only if the stored hash is missing or different

- stored hash matches the rendered body -> `unchanged`, no page write
- hash missing or different, and the page update succeeds -> `published`
- Prometheus unavailable or a Confluence call fails -> `failed`

A page that was updated but whose hash could not be recorded is reported as
`failed`: the content is correct, the record is not, and the next publication
will rewrite the page.

A hash that cannot be read — the property API refusing, an unparsable body — is
treated as absent and the page is rewritten. Refusing to publish because
bookkeeping is unreadable would be the worse failure.

Two consequences are known and accepted:

- **A hand-edited page is not detected.** The property survives a body edit, so
  the hash still matches and publication reports `unchanged`; the manual edit
  stands until the inventory itself changes. To force a rewrite, delete the
  `inventory-hash` property.
- **The hash is per page, not per backend.** Two backends publishing the same
  page agree, because the answer they read is the same one.

## 20. Runtime State

The backend persists nothing and mounts no volume:

- The observation index is rebuilt from Prometheus at startup and refreshed
  every ten minutes (§14).
- Last successful refresh, last publication time and last publication result
  live in process memory and are reported by the status endpoint (§17.3). A
  restart loses the status line, not data.
- What was last published is recorded on the Confluence page itself (§19.4), so
  a replacement backend reads the same answer the original would have.

A Confluence page is the only durable artifact the backend writes.

## 21. Security and Privacy

### 21.1 Exporter HTTP security

Use Prometheus exporter-toolkit web configuration, compatible with the pattern used by node_exporter.

Support:

- TLS
- basic authentication
- bcrypt password hashes

Example execution pattern:

```text
inventory-exporter -config.file=/etc/inventory-exporter/config.yaml -web.config.file=/etc/inventory-exporter/web-config.yaml
```

### 21.2 ESXi credentials

- stored in mounted YAML configuration
- file permissions restricted
- never logged
- `insecure_skip_verify` defaults to `true`
- set `insecure_skip_verify: false` to enforce TLS verification

### 21.3 Confluence credentials

- environment variables only
- never written to disk (§20)
- never returned by API
- never exposed to frontend JavaScript

### 21.4 UI mutation endpoints

V1 has no UI authentication, but still require:

- same-origin requests
- JSON content type
- custom same-origin request header
- no permissive CORS

Deployment should place the application only on a trusted network or behind an external authentication proxy if needed.

## 22. Reliability and Failure Handling

### 22.1 Prometheus unavailable

- continue serving previous backend cache
- show stale-cache warning
- manual refresh fails without clearing cache
- block Confluence publication

### 22.2 Exporter or collector unavailable

- exporter continues serving the previous complete snapshot only until `max_snapshot_age`
- the original snapshot timestamp remains unchanged
- collector health reports failure
- backend retains observations according to that original observation time
- exporter removes expired inventory series after 8 hours while retaining health metrics
- Prometheus alerts separately

### 22.3 Successful empty platform result

- exporter may publish an empty current platform snapshot with a new snapshot timestamp
- resource series absent from that snapshot are not considered newly observed
- processor does not immediately remove historical resources from its observation index
- UI removal occurs only after 1 hour since the resource's last real observation
- Confluence removal occurs only after 8 hours since the resource's last real observation

### 22.4 Partial collection

- reject incomplete snapshot
- keep prior complete snapshot
- set collector health to failed or invalid

### 22.5 Backend restart

- rebuild 8-hour observation history from Prometheus
- read the last published hash from the Confluence page (§19.4)
- lose process-local refresh and publication status, which is a status line and
  not data (§20)
- do not require an inventory database

### 22.6 Duplicate host IDs

- treat as validation error
- never silently merge hosts
- preserve previous valid backend cache if a new refresh is invalid
- expose error in status endpoint and logs

## 23. Scalability

Expected scale is modest: many preset hosts, small numbers of active resources per host.

The architecture scales by:

- cached exporter scrapes
- one ESXi collector process supporting many targets
- in-memory normalized inventory
- deterministic JSON snapshots
- ETag-based browser caching
- bounded metric labels
- no full inventory database

If scale grows substantially, likely future changes are:

- paginated or segmented JSON endpoints
- server-side search
- split ESXi target workers
- persistent observation storage

These are not required for V1.

## 24. Observability

Backend logs to stdout.

Recommended backend metrics:

- refresh duration
- refresh success/failure
- Prometheus query duration
- normalized host/resource counts
- duplicate-ID validation failures
- UI snapshot age
- Confluence publish duration and result
- observation-index size

Recommended exporter metrics:

- collection duration
- collection result
- last successful timestamp
- cached snapshot age and expiry
- skipped overlapping collection count
- entity counts per collector

## 25. Testing Strategy

### 25.1 Exporter unit tests

- description normalization
- IP filtering
- disk grouping inputs
- Linux memory parsing
- hugepage parsing
- filesystem filtering
- Btrfs deduplication identity
- mutually exclusive config schemas
- collection snapshot replacement
- failed collection retaining prior snapshot without changing its timestamp
- expired snapshot removing inventory series while health remains
- no overlapping collection

### 25.2 Backend unit tests

- metric-family joins
- stable identity construction
- duplicate host-ID rejection
- 1-hour UI retention
- 8-hour Confluence retention
- current versus retained status
- startup reconstruction from range data
- coherent latest-observation selection
- Confluence hash exclusion of volatile values
- publication skipped when the stored hash matches the rendered body
- publication of identical content produces a byte-identical body
- deterministic sorting
- capacity provenance
- ETag behavior

### 25.3 Integration tests

- Linux exporter -> Prometheus -> backend -> UI JSON
- ESXi exporter mock -> Prometheus -> backend
- VM disappears for less than 1 hour
- VM disappears for more than 1 hour
- VM disappears for more than 8 hours
- host restarts during collection
- libvirt returns a temporary empty list
- LXD unavailable while host collection succeeds
- one ESXi target fails while others succeed
- Prometheus unavailable during publication
- Confluence unchanged publication
- Confluence changed publication

### 25.4 Required failure scenarios

| Scenario | Required behavior |
|---|---|
| One exporter scrape missed | No visible inventory change |
| Host restarts for 30 minutes | UI retains with last-seen state; Confluence retains |
| All VMs temporarily stopped | Default view retains for 1 hour; `month`/`all` keep showing them as retained; Confluence retains for 8 hours |
| VM intentionally stopped | Removed after windows expire |
| Exporter down for 2 hours | Gone from the `now` view after 1 hour, still listed as retained in `month`/`all`; retained for Confluence |
| Exporter down for 9 hours | Removed from new Confluence snapshots; still listed as retained while inside the index retention window |
| Backend restarts | Rebuilds history from Prometheus |
| Prometheus unavailable | Old UI cache with warning; publication blocked |
| Collector returns partial data | Prior complete snapshot remains |
| CPU usage changes only | UI changes; Confluence hash unchanged |
| Filesystem free changes only | UI changes; Confluence hash unchanged |
| VM moves between hosts | Inventory hash changes |
| Btrfs subvolumes repeat capacity | Backing capacity displayed once |
| QEMU guest agent unavailable | Metadata fallback; no guessing |
| VMware Tools unavailable | Configured guest type fallback; IP may be missing |
| LXD limit missing | Host/pool-backed value with provenance |
| Duplicate host IDs | Refresh rejected; no silent merge |

### 25.5 Prometheus alert tests

Use `promtool test rules` for scrape, collector, and staleness alerts.

## 26. Rejected Alternatives

### Browser queries Prometheus directly

Rejected because it exposes Prometheus details, duplicates join logic in JavaScript, complicates authentication, and makes the UI contract unstable.

### Persistent inventory database in V1

Rejected because Prometheus already retains the required eight-hour observation history and current scale does not justify another stateful service.

### Publication state inside the container

Rejected. Keeping the last published hash in a state file or a Prometheus metric
would make the container the authority on what the page contains, and would make
a replaced or duplicated backend disagree with the page it owns. The hash belongs
with the artifact it describes, as a Confluence content property (§19.4), so the
backend stays replaceable and the container mounts no volume (§20).

### Immediate removal after successful empty collection

Rejected because host and platform restart windows would create unacceptable inventory flicker.

### Eight-hour UI window

Rejected because the web UI should become more current than Confluence. One hour is the selected compromise.

### One-hour Confluence window

Rejected because documentation should remain stable during longer maintenance windows.

### React, Vue, or another frontend framework

Rejected because the V1 page is a straightforward long document with search, tables, bars, and a few actions.

### Docker inventory in the same system

Rejected because Docker semantics differ materially from VM and LXD inventory. It should be reconsidered as a separate instrument or after a future redesign.

### Libvirt directory and network storage pools

Network-backed libvirt pools (NFS/netfs, iSCSI, SCSI) are rejected for V1 and filtered out by the normalizer. Local pool types (dir, fs, logical/LVM, disk, zfs) are shown. The libvirt auto-created "default" pool is skipped (it cannot be removed and is unused when LVM pools are configured); LXD pools named "default" are kept since "default" is the standard LXD pool name.

### Node exporter as the host inventory source

Rejected because the inventory exporter must provide a complete coherent contract and should not depend on a separate exporter for host inventory fields.

## 27. Risks

### Pure-Go libvirt capability

The chosen library may not support every required guest-agent and storage operation.

Mitigation:

- prototype required calls first
- degrade guest OS enrichment cleanly
- do not introduce command parsing or CGO silently

### Prometheus historical reconstruction complexity

Prometheus range data can produce mismatched timestamps across metric families.

Mitigation:

- define coherent observation selection explicitly
- require and use exporter collector snapshot timestamps for every historical join
- test restart reconstruction heavily

### Label cardinality

Descriptions, IPs, disk IDs, and filesystem relationships create additional series.

Mitigation:

- bounded description length
- one series per IP/disk
- stable IDs
- avoid volatile labels
- keep interface names out of the public contract

### Unauthenticated mutation endpoints

Refresh and publication actions can be triggered by any trusted-network user.

Mitigation:

- same-origin only
- no CORS
- external reverse-proxy authentication when required
- trusted-network deployment

## 28. Open Technical Questions

None material for V1.

The pure-Go libvirt proof of capability is an implementation validation task, not a product-design decision.
