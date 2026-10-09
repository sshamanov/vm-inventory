# VM Inventory — Infrastructure Inventory Application

A read-only web application and Confluence publisher for infrastructure inventory currently or recently observed across multiple geographic groups.

## Overview

VM Inventory inventories Linux hosts (libvirt/KVM, LXD) and standalone ESXi hosts, their virtual machines, and LXD containers. Prometheus is the sole inventory data source. A Go exporter binary collects platform data and exposes Prometheus metrics. A processing backend reads those metrics, normalizes the inventory, serves a JSON API, renders the web UI, and publishes one Confluence page.

Docker inventory is explicitly out of scope for V1.

## Authority Documents

All implementation decisions must align with these binding documents:

- **[ARCHITECTURE.md](ARCHITECTURE.md)** — System architecture, metric contract, data normalization rules, deployment model, security, testing strategy
- **[DESIGN.md](DESIGN.md)** — Product and UI design, information architecture, navigation model, search design, presentation rules

## Project Structure

```
/
├── cmd/
│   ├── inventory-exporter/    # Exporter binary entrypoint
│   └── inventory-backend/     # Backend binary entrypoint
├── internal/
│   ├── exporter/              # Linux and ESXi collection logic
│   │   ├── linux/             # Host, libvirt, LXD collectors
│   │   └── esxi/              # ESXi target collector
│   ├── backend/               # Processing backend modules
│   │   ├── prometheus/        # Prometheus client + query builder
│   │   ├── decoder/           # Metric family decoder
│   │   ├── index/             # Observation-index manager
│   │   ├── normalizer/        # Data normalization
│   │   ├── api/               # HTTP handlers + JSON API
│   │   ├── confluence/        # Confluence renderer + client
│   │   └── state/             # process-local runtime state (no persistence)
│   └── shared/                # Shared types, normalizers, validation
├── web/                       # Static frontend assets (HTML, CSS, JS)
├── docker/                    # Dockerfiles for exporter and backend
├── config/                    # Example configuration files
├── test/                      # Integration and E2E test helpers
├── ARCHITECTURE.md            # Authority document — system architecture
├── DESIGN.md                  # Authority document — product and UI design
├── AGENTS.md                  # Rules for AI coding agents
└── README.md                  # This file
```

## Quick Start

### Prerequisites

- Docker (all builds happen inside Docker — no external Go toolchain required)
- Prometheus instance (scraping the exporters)
- Confluence Server/Data Center instance (for publication)

### Pull pre-built images (ghcr.io)

```bash
docker pull ghcr.io/sshamanov/inventory-exporter:latest
docker pull ghcr.io/sshamanov/inventory-backend:latest
```

### Build locally (Docker only)

All builds use Docker with `--network host`. No builds occur outside the Docker sandbox.

```bash
make build-exporter build-backend  # binaries in bin/
make docker-exporter docker-backend  # images locally
```

### Run with Docker Compose

```bash
# Backend with Prometheus and Confluence configuration
PROMETHEUS_URL=http://prometheus:9090 \
CONFLUENCE_URL=https://confluence.internal \
CONFLUENCE_TOKEN=<personal access token> \
CONFLUENCE_PAGE_ID=123456 \
docker-compose -f docker-compose.backend.yml up -d
```

### Exporter Configuration

#### Linux mode

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

#### ESXi mode

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
    insecure_skip_verify: false
```

## API Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/inventory` | Normalized inventory JSON |
| `GET` | `/api/status` | Cache and publication status |
| `POST` | `/api/refresh` | Trigger immediate Prometheus refresh |
| `POST` | `/api/confluence/publish` | Trigger Confluence publication |

For details see [ARCHITECTURE.md §17](ARCHITECTURE.md#17-normalized-json-api).

## Architecture

### System Diagram

```
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
                 - publication state
```

### Key Design Decisions

- **Prometheus as sole data source** — no separate inventory database. The backend queries Prometheus for current and historical observations.
- **1-hour UI liveness, 8-hour Confluence liveness** — masks short maintenance windows and platform restarts.
- **No frontend framework** — plain HTML, CSS, JavaScript for V1.
- **Docker-only builds** — no external toolchain; everything builds inside Docker with `--network host`.
- **Observation retention** — resources are not immediately removed when a platform returns empty results; they persist through the liveness window.

For the full architecture, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Technical Stack

| Component | Choice |
|---|---|
| Language | Go 1.22+ |
| Module path | `vm-inventory` |
| Libvirt client | `github.com/digitalocean/go-libvirt` (pure Go, no CGO) |
| ESXi client | `github.com/vmware/govmomi` |
| Frontend | Plain HTML, CSS, JavaScript — no framework, no build pipeline |
| Data source | Prometheus |
| Confluence API | Server / Data Center REST API |
| Container registry | `ghcr.io/sshamanov/` |

## Environment Variables

### Backend

| Variable | Required | Description |
|---|---|---|
| `PROMETHEUS_URL` | Yes | Prometheus base URL (e.g., `http://prometheus:9090`) |
| `CONFLUENCE_URL` | For publish | Confluence Server/Data Center base URL |
| `CONFLUENCE_TOKEN` | For publish | Confluence Personal Access Token (Bearer auth) |
| `CONFLUENCE_PAGE_ID` | For publish | Numeric ID of the page to update |

### Exporter

Configured via YAML files only. See [ARCHITECTURE.md §8](ARCHITECTURE.md#8-exporter-configuration).

## Testing

```bash
# Unit tests
go test ./internal/...

# Integration tests (requires Prometheus test instance)
go test ./test/...

# Prometheus alert tests
promtool test rules ./config/alerts/
```

See [ARCHITECTURE.md §25](ARCHITECTURE.md#25-testing-strategy) for the full testing strategy and required failure scenarios.

## Non-Goals (V1)

- Docker inventory
- Real-time monitoring or alerting
- Inventory editing or lifecycle management
- vCenter orchestration
- Per-guest performance metrics
- Persistent inventory database
- Authentication/authorization in the web UI
- Guest filesystem utilization

## For AI Coding Agents

See [AGENTS.md](AGENTS.md) for project rules, code conventions, and the decision checklist.
