# Infrastructure Inventory — Product and UI Design

## 1. Product Summary

Infrastructure Inventory is a read-only web application and Confluence publisher for infrastructure currently or recently observed across multiple geographic groups.

The product inventories:

- Linux hosts running libvirt/QEMU
- Standalone ESXi hosts
- Running KVM/libvirt virtual machines
- Powered-on ESXi virtual machines
- Linux hosts running LXD
- Running LXD containers

Docker is explicitly out of scope for V1.

Prometheus is the only inventory data source used by the application. Exporters collect platform data and expose Prometheus metrics. The processing application reads those metrics, normalizes the inventory, serves a JSON API, renders the web UI, and publishes one long Confluence page.

The product is an operational inventory, not a real-time monitoring dashboard. Short maintenance windows, host restarts, and temporary empty platform responses are intentionally masked through observation-retention windows.

## 2. Goals

- Provide one readable inventory of hosts, VMs, and LXD containers.
- Group all information first by geographic label.
- Keep host capacity and host identity visible before guest resources.
- Show current host capacity indicators in the web UI.
- Publish stable, low-churn capacity and inventory data to Confluence.
- Preserve host and resource context during search.
- Tolerate short platform outages, restarts, and inventory flicker.
- Use a simple long-page interface without complex navigation or collapsed sections.

## 3. Non-Goals

- Docker inventory.
- Per-VM or per-container current CPU and RAM usage.
- Performance monitoring or alerting.
- Editing infrastructure.
- Host or resource lifecycle management.
- Authentication or authorization in the V1 web UI.
- Guest filesystem utilization.
- Global resource regrouping that removes host context.
- Guessing missing guest operating systems, IP addresses, or descriptions.

## 4. Users and Roles

### Infrastructure operator

Uses the web UI to:

- find a host, VM, or container
- inspect host capacity
- see where a VM or container runs
- inspect host IPs and guest IPs
- manually refresh the application cache
- manually publish the current stable inventory to Confluence

### Documentation reader

Uses the generated Confluence page to:

- read a stable infrastructure inventory
- navigate by geo and host
- inspect host specifications
- inspect active or recently observed VMs and containers

### Monitoring operator

Uses Prometheus and alerting separately to detect:

- exporter scrape failures
- failed collectors
- stale collectors
- missing exporters

The inventory UI does not replace Prometheus alerting.

## 5. Core Product Semantics

### 5.1 Host is the primary item

A host is always presented as a complete item with its own identity, capacity, storage, platform information, and IP addresses.

Resources are not extracted into a global inventory detached from hosts.

### 5.2 Geographic grouping

Each host has a free-text `geo` property.

- Default: `general`
- Geo names are sorted case-insensitively.
- Hosts are sorted by host ID.
- Resources are sorted by host, then name.

A host belongs to exactly one geo.

### 5.3 Observation retention

The product uses recent observation windows rather than immediate deletion after an empty scrape.

- Web UI liveness window: 1 hour
- Confluence liveness window: 8 hours

A resource remains visible until it has not been observed for longer than the relevant window.

The web UI chooses how far back it looks. A view switch offers `now` (1 hour),
`month` (30 days) and `all` (365 days); `now` is the default and the payload
carries only the items the chosen view contains. Which of those items are still
current is decided by the liveness window alone and does not move with the view,
so a wider view reveals departures and never presents one as running.

This intentionally masks:

- short host restarts
- temporary libvirt or LXD restarts
- brief ESXi access failures
- maintenance periods
- temporary all-VM shutdown states
- missed exporter collections or Prometheus scrapes

### 5.4 Current and retained states

Each host and resource has one of two user-facing observation states:

- `current`: present in the newest logical collector snapshot and observed within the applicable liveness window
- `retained`: absent from a newer logical snapshot, or available only from an older cached snapshot, but still inside the applicable liveness window

Prometheus scrape time does not make old cached exporter data current. Freshness is based on the exporter's collector snapshot timestamp.

The web UI shows `Last seen ... ago` only for retained items. A retained row
carries the stamp in the columns its capacity readings would occupy: those three
columns collapse into one cell, holding the capacity the item was last seen with
(when known) and the stamp itself, set in italic monospace so it reads as a note
about the row rather than a reading from it.

Confluence does not show per-item last-seen timestamps.

## 6. Information Architecture

The product uses one long page.

```text
Sticky page header
  Search
  Geo navigation
  Host navigation
  Cache status
  Manual refresh
  Publish to Confluence

Geo: <geo name>
  Hosts
    Host details
    Host details
    ...

  Virtual machines
    Combined KVM and ESXi table

  LXD containers
    LXD table

Geo: <next geo>
  ...
```

There are no collapsed host sections in V1.

## 7. Navigation Model

### 7.1 Sticky header

The header remains visible while scrolling.

It contains:

- application title
- global search input
- geo anchor links
- host anchor links
- cache age and refresh status
- Refresh button
- Publish to Confluence button

A table header stays visible while its table is in view, pinning directly beneath
the masthead — but only while that table fits its column. A table wide enough to
need sideways scrolling keeps its own scroll box, and a sticky header inside such
a box would be placed against the box instead of the viewport. Whether a table
fits depends on the viewport width and on the longest visible cell, so it is
measured after every render and filter rather than assumed from a breakpoint.
The masthead height the table header parks beneath is measured from the masthead
itself, so it follows the masthead as the viewport re-wraps its controls.

### 7.2 Geo navigation

Each geo has an anchor.

Selecting a geo link scrolls to the geo section.

### 7.3 Host navigation

Each host has a stable page anchor.

Host navigation may be rendered as a compact list grouped by geo.

## 8. Search Design

Search matches:

- geo
- host ID
- host description
- host IP address
- VM or container name
- VM or container description
- VM or container IP address
- guest operating-system text

Search results retain their original structure.

Rules:

- A geo match shows the whole geo.
- A host match shows the host card and all resources belonging to that host.
- A resource match shows the matching row and its host card.
- Empty geo sections are hidden while filtering.
- A no-results state is displayed when nothing matches.

Search never creates a detached flat result list.

### 8.1 Filter Dropdowns

Two mutually exclusive dropdown filters sit above the host cards:

- **Geo filter** — shows only hosts, VMs, and LXD containers belonging to the selected geo. Selecting a geo resets the host filter and search.
- **Host filter** — shows only the selected host card and only VMs/LXD containers belonging to that host. Selecting a host resets the geo filter and search.

Both filters apply across all three categories (hosts, VMs, LXDs) simultaneously. When a host filter is active, the VM and LXD container tables show only resources whose `host_id` matches the selected host.

## 9. Host Presentation

Each host is always visible in full within its geo section.

### 9.1 Host header

Fields:

- host ID
- description
- platform type
- geo
- host IP addresses
- operating-system or hypervisor version
- observation state
- last seen, only when retained

Missing values display as `—`.

### 9.2 Linux host identity

Display when available:

- distribution name
- distribution version
- kernel version
- architecture
- hostname

### 9.3 ESXi host identity

Display when available:

- product name
- version
- build number
- architecture where available

### 9.4 Host IPs

Display all source-known, non-loopback, non-link-local host IP addresses.

Display addresses only. Do not display interface names in V1.

Deduplicate and sort:

1. IPv4
2. IPv6

## 10. CPU Design

### 10.1 Host CPU specification

Display:

- CPU model
- socket count
- physical core count
- hardware thread count

### 10.2 Host CPU capacity indicator

The UI shows smoothed current hardware-thread-equivalent usage.

Default smoothing window: 30 minutes.

Display example:

```text
11.2 of 32 thread-equivalents used
20.8 available
30-minute average
```

A horizontal usage bar shows used and available capacity.

This value is an operational capacity indicator only. It is not a scheduler guarantee, admission-control calculation, or vCPU overcommit model.

### 10.3 Retained hosts

If a host is retained rather than current:

- show CPU specification
- do not show stale current CPU usage
- render current CPU usage as `—`

## 11. RAM Design

### 11.1 Linux RAM bar

The UI RAM bar has exactly three segments:

```text
[ used | hugepage free | normal free ]
```

Definitions:

- `normal free`: Linux `MemAvailable`
- `hugepage free`: sum of free reserved hugepage memory across all page sizes
- `used`: physical total minus normal free minus hugepage free

The displayed value is clamped so no segment becomes negative.

### 11.2 Hugepage details

Hugepage pools are also listed separately by page size.

Example:

```text
2 MiB pages: 32 GiB total, 8 GiB free
1 GiB pages: 64 GiB total, 20 GiB free
```

### 11.3 ESXi RAM

Use the closest available ESXi host-memory values.

If there is no meaningful hugepage equivalent:

- hugepage free is zero
- show used and free segments only

### 11.4 Retained hosts

For retained hosts:

- show total physical memory
- show configured hugepage totals
- do not present old current free or used values as live
- current usage bar becomes unavailable

## 12. Physical Disk Design

Physical disks are a hardware specification, not a free-space source.

Display grouped disk capacities.

Grouping rules:

- group by approximate capacity only
- ignore disk model for grouping
- use an 8% tolerance
- show the smallest size in the group

Examples:

```text
1.92 TB × 4
240 GB × 2
```

Individual disk details may remain in the JSON API for future use but do not need to be expanded in V1 UI.

Never sum physical disks with filesystems, LVM pools, LXD pools, or datastores.

## 13. Filesystem Design

### 13.1 Linux filesystems

V1 displays local mounted filesystems using:

- `ext4`
- `btrfs`

Exclude:

- pseudo filesystems
- temporary filesystems
- container overlay mounts
- system-only mounts
- namespace duplicates
- mounts not usable as infrastructure capacity

### 13.2 Btrfs deduplication

Btrfs subvolumes can expose the same backing filesystem through several mountpoints.

The UI must show backing capacity once and list relevant mountpoints together.

Example:

```text
Btrfs filesystem <id>
Mountpoints: /, /opt
Total: 4 TiB
Available: 2 TiB
```

### 13.3 Filesystem display

Current hosts show:

- mountpoint or mountpoint list
- filesystem type
- total capacity
- available capacity
- usage bar

Default text format:

```text
2.0 TiB free of 4.0 TiB (50%)
```

Retained hosts show specification but not stale current free-space bars.

## 14. Platform Storage Design

### 14.1 KVM/libvirt storage pools

Display only active local libvirt storage pools. Network-backed pool types (netfs/NFS, iscsi, scsi) are filtered out. The libvirt auto-created "default" pool is skipped (it cannot be removed and is unused when LVM pools are configured); LXD pools named "default" are kept since "default" is the standard LXD pool name.

Fields:

- pool name
- total capacity
- free capacity

All local libvirt pool types (dir, fs, logical/LVM, disk, zfs) are in scope. Network-backed pool types are out of scope for V1.

Do not display thin-provisioning fields. The target environment uses thick allocation.

### 14.2 LXD storage pools

Display all active non-system LXD storage pools reported by LXD.

Fields:

- pool name
- driver
- total capacity
- available capacity, when reported

Btrfs pools are explicitly supported.

### 14.3 ESXi datastores

Display accessible local VMFS datastores (NFS, vSAN, VVol excluded).

Fields:

- datastore name
- datastore type
- total capacity
- free capacity

### 14.4 Storage separation

Keep these sections distinct:

- physical disks
- Linux filesystems
- libvirt LVM pools
- LXD storage pools
- ESXi datastores

Do not calculate a combined storage total.

## 15. Virtual Machine Table

KVM and ESXi VMs share one table per geo.

Columns:

| Host | Name | Platform | IP addresses | Description | Guest OS | vCPU | RAM | Disk |
|---|---|---|---|---|---|---:|---:|---:|

Rules:

- Host and Name are always separate columns.
- Platform is `KVM` or `ESXi`.
- Display all known non-loopback, non-link-local guest IPs.
- IP addresses are displayed without interface names.
- Disk is configured virtual-disk capacity.
- Multiple virtual disks may be rendered as individual values plus a total.
- Internal UUIDs and platform IDs are not displayed.
- Missing values display as `—`.
- Retained rows show `Last seen ... ago` in place of the capacity columns (§5.4).

### 15.1 Guest OS precedence

KVM:

1. QEMU guest agent
2. libvirt domain metadata
3. unavailable

ESXi:

1. VMware Tools guest information
2. configured guest OS identifier
3. unavailable

Do not infer guest OS from names, disk paths, or IP addresses.

## 16. LXD Container Table

LXD containers have a separate table per geo.

Columns:

| Host | Name | IP addresses | Description | OS/Image | CPU capacity | RAM capacity | Disk capacity |
|---|---|---|---|---|---:|---:|---:|

Rules:

- Do not combine LXD with VM rows.
- Do not combine LXD with Docker or other container runtimes.
- Show running containers only, subject to the observation window.
- Missing values display as `—`.
- Internal identifiers are not displayed.

### 16.1 LXD OS/image precedence

1. instance OS metadata
2. image properties
3. image description or fingerprint
4. unavailable

### 16.2 LXD capacity provenance

CPU:

- explicit CPU count or CPU set: configured capacity
- otherwise: host hardware-thread capacity

RAM:

- explicit memory limit: configured capacity
- otherwise: host physical RAM

Disk:

- explicit root-disk limit: configured capacity
- otherwise: backing LXD storage-pool capacity

Fallback values must be visibly marked as host-backed or pool-backed so they are not mistaken for hard limits.

## 17. Status and Operational UI

### 17.1 Cache status

Display:

- inventory generated time
- cache age
- last successful Prometheus refresh
- current refresh state
- stale-cache warning when appropriate

### 17.2 Manual refresh

Refresh button behavior:

- starts a synchronous application refresh
- disables while refresh is running
- shows success or failure
- retains the old UI snapshot when refresh fails

A refresh re-reads what Prometheus is scraping at that moment, and an instance
that has stopped being scraped is simply absent from that answer. Absence is not
retirement: the index goes on holding the instance, dated to when it was last
seen, so the wider views can still show it as retained. Only the observation
windows decide when an instance stops being visible (§5.3), and they alone apply
to both the automatic and the manual refresh.

A refresh is therefore two passes over Prometheus, run by both triggers: what is
being scraped now, and the history pass of §15.4, which recovers an instance the
index has never heard of so that it can be shown as retained rather than not at
all. The manual refresh waits for both, so a refresh that reports success means
the reload that follows it is already complete.

### 17.3 Confluence publication

Publish button behavior:

- invokes the same backend hook used by scheduled publication
- disables while publication runs
- shows one of:
  - published
  - unchanged
  - failed
  - Prometheus unavailable

Concurrent publication requests are serialized.

## 18. Loading, Empty, Error, and Edge States

Required states:

- initial loading
- no active or recently observed hosts
- no VMs in a geo
- no LXD containers in a geo
- cached data with failed refresh
- retained host or resource
- current host with partially unavailable fields
- refresh in progress
- publish in progress
- publish succeeded
- publish unchanged
- publish failed
- no search results

Unknown values remain visible as `—`; an item is not removed merely because one field is unavailable.

## 19. Accessibility

- Do not rely on color alone for RAM, CPU, or storage bars.
- Every bar must include text values.
- Use semantic headings matching geo and host hierarchy.
- Use real tables for VM and LXD data.
- Preserve keyboard focus after refresh and publication actions.
- Anchor links must have visible focus states.
- Search must have a persistent label.
- Status messages should be exposed through an ARIA live region.
- Ensure sufficient contrast for used, hugepage-free, and normal-free RAM segments.

## 20. Confluence Page Design

The application publishes one long Confluence page.

The page mirrors the web hierarchy:

```text
VM Inventory
  Generated inventory timestamp
  Observation-window note
  Table of contents

  Geo
    Hosts
      Host
      Host
    Virtual machines
    LXD containers

  Geo
    ...
```

The generated timestamp is not rendered today. When it is added it must be
excluded from the publication hash (ARCHITECTURE §19.4), or every publication
would look like a content change and the page version would climb on schedule
alone.

### 20.1 Table of contents

Include links to:

- every geo
- every host
- each geo VM table
- each geo LXD table

### 20.2 Confluence observation note

Include a note near the page heading:

> Active inventory includes resources observed during the eight hours before publication.

### 20.3 Confluence host content

Include stable specification and total-capacity values:

- host identity
- geo
- host IPs
- operating-system or hypervisor version
- CPU model and topology
- total physical RAM
- total hugepage pools by page size
- physical disks
- filesystem total capacity
- LVM pool total capacity
- LXD pool total capacity
- datastore total capacity

### 20.4 Values excluded from Confluence

Do not include:

- CPU usage
- free CPU thread-equivalents
- normal RAM available
- hugepage free
- filesystem free or used
- storage-pool free or used
- datastore free or used
- per-item last-seen timestamps
- current/retained state
- exporter or collector health

### 20.5 Confluence resource tables

Use the same VM and LXD table structures as the web UI, but without interactive
controls or transient status. Description is the last column of both tables; the
capacity columns keep their place regardless of how long a description runs.

## 21. Success Criteria

The design is successful when:

- all hosts are grouped by geo
- each host appears once per geo
- KVM and ESXi VMs appear together
- LXD containers remain separate
- host and resource names are never treated as globally unique
- short outages do not cause visible inventory churn
- resources disappear from the web UI after one hour unseen
- resources disappear from a newly generated Confluence snapshot after eight hours unseen
- volatile capacity usage never causes Confluence updates
- search preserves geo and host context
- missing fields do not hide otherwise valid items
- one long page remains readable through anchors and search

## 22. Unresolved Design Decisions

None material for V1.
