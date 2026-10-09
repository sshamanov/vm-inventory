# AGENTS.md — Project Rules for AI Coding Agents

## Authority Documents

**[ARCHITECTURE.md](ARCHITECTURE.md)** and **[DESIGN.md](DESIGN.md)** are the **binding authority documents** for this project. Every implementation decision, code structure, and design choice must align with them. When in doubt, re-read the relevant section before writing code. If a document is silent on a topic, prefer the simplest approach consistent with the stated goals and non-goals.

## Project Identity

- **Name:** Infrastructure Inventory (repo: `vm-inventory`)
- **Language:** Go (backend + exporter), plain HTML/CSS/JS (frontend)
- **Data source:** Prometheus only
- **V1 scope:** Linux hosts (libvirt/KVM, LXD), standalone ESXi hosts

## Operational Environment

Real instance addresses and host names are deployment-specific and are not kept
in this repository. Keep them in `CLAUDE.local.md` (gitignored), which agents
load alongside this file, and reference them from there when diagnosing issues or
suggesting deployment actions.

### Instances
- **Backend** — a Docker container serving the API and web UI (`LISTEN_ADDR`,
  default `:8080`). A local dev instance runs as `inventory-backend-dev` with
  `--network host` and `LISTEN_ADDR=:8081`.
- **Exporters** — one per Linux host, serving metrics on `:9171` (`:9172` for
  the ESXi collector). `/api/inventory` lists every host with its addresses.
- **Prometheus** — scrapes the exporter `/metrics` endpoints; the backend
  queries it to rebuild its observation index.

### Key facts
- After pushing to `main`, CI builds and uploads binaries to the file service within ~3 minutes.
- Exporter hosts auto-update within 1 hour via `inventory-exporter-update.timer`.
- The backend does NOT auto-update — it must be redeployed manually (`docker-compose up -d --build` or `docker restart`).
- Backend index rebuild: click **Refresh** in UI, POST `/api/refresh`, or wait for periodic refresh.
- Exporter configs are host-local (`/etc/inventory-exporter/config.yaml`) — changes to collection logic require code changes + binary update, not config edits.

## Build Rules

### Docker-First Build

1. **All builds must happen inside Docker.** No external toolchain installation on the host.
2. **Use `--network host`** for Docker build and run commands within the sandbox.
3. The Docker build environment must produce:
   - Linux AMD64 exporter static binary
   - Docker image for the exporter
   - Docker image for the processing backend
4. Avoid CGO in the exporter binary where practical (§6.1).

### Dockerfiles Location

- `docker/exporter.Dockerfile` — Go exporter build + runtime image
- `docker/backend.Dockerfile` — Processing backend build + runtime image

### Makefile Targets

- `make build-exporter` — builds `bin/inventory-exporter` inside Docker
- `make build-backend` — builds `bin/inventory-backend` inside Docker
- `make build` (or `make`) — builds both
- `make docker-backend` — builds backend Docker image
- `make test` / `make lint` — runs test suite / linters inside Docker

## Deployment

**Every push to `main` triggers CI (`.github/workflows/ci.yml`):**
1. Builds exporter + backend static binaries (Linux AMD64, CGO disabled)
2. Builds + pushes Docker images to `ghcr.io/sshamanov/inventory-exporter` and `ghcr.io/sshamanov/inventory-backend`
3. Uploads binaries + deploy script to the file service (repo variable `FILESERVICE_URL`):
   - `inventory-exporter` — the exporter binary
   - `inventory-backend` — the backend binary
   - `inventory-exporter-deploy` — `config/deploy.sh` (the bootstrap script)

### Exporter deployment (Linux hosts)

Exporter hosts auto-update via systemd timer — **no manual intervention needed after pushing to main.**

**Bootstrap a new host:**
```sh
curl -fsSL <file-service>/f/inventory-exporter-deploy/raw | sh
```
This installs: the exporter binary to `/usr/local/bin/`, example config to `/etc/inventory-exporter/`, and systemd units for the exporter + hourly self-update timer. Then edit `/etc/inventory-exporter/config.yaml` with the host's ID, description, and geo. Start with `systemctl start inventory-exporter`.

**Self-update mechanism (already installed on every host):**
- `config/bin-update.sh` — compares local binary SHA256 against the remote hash on the file service (`BIN_SERVER_URL`); downloads new binary only if changed, then exits 0. Called as: `bin-update.sh inventory-exporter /usr/local/bin/inventory-exporter`
- `config/inventory-exporter-update.timer` — systemd timer, fires **every hour** (with 5min randomized delay). Runs `bin-update.sh` followed by `systemctl try-restart inventory-exporter`.
- After a push to main, hosts pick up the new exporter binary within **1 hour** automatically.

**Manual update on a specific host (bypasses the timer):**
```sh
ssh <host> '/usr/local/bin/bin-update.sh inventory-exporter /usr/local/bin/inventory-exporter && sudo systemctl restart inventory-exporter'
```

**Exporter metrics endpoint:** `http://<host>:9171/metrics` (Linux, default) or `:9172` (ESXi).

### Backend deployment

The backend runs as a Docker container. Configuration via environment variables:
- `PROMETHEUS_URL` (required) — Prometheus HTTP API
- `LISTEN_ADDR` (default `:8080`) — API/web UI listen address
- `CONFLUENCE_URL`, `CONFLUENCE_TOKEN`, `CONFLUENCE_PAGE_ID` (optional, all three needed to publish)
- `WEB_DIR` — path to static web assets

The backend holds no state across restarts: refresh and publication status are
process-local, and the hash identifying what was last published lives as a
content property on the Confluence page (§19.4, §20). The container mounts no
volume.

**Local dev:** `docker run -d --name inventory-backend-dev --network host -e PROMETHEUS_URL=... -e LISTEN_ADDR=:8081 inventory-backend:dev`

**Production:** `docker-compose -f docker-compose.backend.yml up -d` (uses `ghcr.io/sshamanov/inventory-backend:latest`).

**After deploying a new backend image**, click **Refresh** in the UI or POST to `/api/refresh` to rebuild the observation index from Prometheus.

### Key files in `config/`

| File | Purpose |
|---|---|
| `config/deploy.sh` | Bootstrap script — installed on every exporter host via `curl \| sh` |
| `config/bin-update.sh` | Per-binary self-updater — compares SHA256, downloads if changed |
| `config/inventory-exporter.service` | systemd unit for the exporter |
| `config/inventory-exporter-update.service` | systemd oneshot that runs `bin-update.sh` |
| `config/inventory-exporter-update.timer` | Hourly timer that triggers the update check |
| `config/exporter-linux.yaml.example` | Example Linux host config |
| `config/exporter-esxi.yaml.example` | Example ESXi collector config |

## Code Organization

```
/
├── cmd/
│   ├── inventory-exporter/    # Exporter binary entrypoint
│   └── inventory-backend/     # Backend binary entrypoint
├── internal/
│   ├── exporter/              # Linux and ESXi collection logic
│   │   ├── linux/             # Host, libvirt, LXD collectors
│   │   └── esxi/              # ESXi target collector
│   ├── backend/               # Processing backend modules (§13.1)
│   │   ├── prometheus/        # Prometheus client + query builder
│   │   ├── decoder/           # Metric family decoder
│   │   ├── index/             # Observation-index manager
│   │   ├── normalizer/        # Data normalization
│   │   ├── api/               # HTTP handlers + JSON API
│   │   ├── confluence/        # Confluence renderer + client
│   │   └── state/             # process-local runtime state (no persistence)
│   └── shared/                # Shared types, normalizers, validation
├── web/                       # Static frontend assets
│   ├── index.html
│   ├── style.css
│   ├── app.js
│   └── fonts/                 # Self-hosted WOFF2 subsets
├── config/                    # Deploy scripts, systemd units, example configs
├── test/                      # Integration and E2E test helpers
├── ARCHITECTURE.md            # Authority document
├── DESIGN.md                  # Authority document
└── README.md
```

Place new Go packages under `internal/` unless there is a compelling reason to expose them publicly.

## Go Code Conventions

- Standard library first; add external dependencies only when necessary.
- Use `go fmt`, `go vet`, `golangci-lint` in CI.
- Error handling: never ignore errors silently. Wrap with context using `fmt.Errorf("context: %w", err)`.
- Logging: structured logging to stdout (§23). Use `log/slog`.
- Configuration: environment variables for secrets (Confluence credentials §21.3), YAML files for exporter config (§7), flags or env vars for backend settings.
- Tests: table-driven tests. Follow the testing strategy in §24.
- Mutex-guard shared state (observation index, cached snapshots).

## Metric Contract (Binding)

The Prometheus metric names, labels, and semantics defined in ARCHITECTURE.md §11 are the **binding contract**. Do not rename metrics, change label semantics, or add labels without updating the architecture document first. The processor, tests, and alerting rules all depend on this contract.

Key constraints:
- Label sets must remain bounded and stable (§11 preamble).
- One metric series per IP address / disk (§26 Label cardinality).
- Snapshot timestamps come from exporter collector time, never Prometheus scrape time (§11.8).

## Data Normalization (Binding)

Follow ARCHITECTURE.md §10 exactly for:
- Description: valid UTF-8, no control chars, collapsed whitespace, trimmed, max 1024 bytes.
- IP addresses: exclude loopback and link-local, deduplicate, sort IPv4 before IPv6.
- Unknown values: omit metric or use info-state labels; JSON uses `null`; UI renders `—`.
- Stable IDs: `host_id + resource_kind + platform_source_id` — never shown in UI/Confluence.

## Frontend Rules

- **No framework, no build pipeline** (§17). Plain HTML, CSS, JS only.
- No CORS (§16.6, §17).
- Same-origin API calls only.
- The frontend never queries Prometheus directly (§5).
- The frontend never performs metric joins (§16.2).
- Fonts are self-hosted under `web/fonts/` — the UI loads no third-party origins at
  runtime, so it works on an air-gapped trusted network. Do not swap them for a CDN.

## Testing Requirements

- Follow the testing strategy in ARCHITECTURE.md §24.
- Write unit tests for: normalization, IP filtering, metric joins, stable ID construction, duplicate detection, liveness windows, Confluence hashing, ETag behavior.
- Write integration tests for: exporter → Prometheus → backend → JSON pipeline.
- Cover every failure scenario in §24.4.
- Use `promtool test rules` for Prometheus alert tests (§24.5).

## Change Discipline (Binding)

- **Tests travel with the code.** Every change that adds or alters behavior ships its tests in the same commit — unit tests co-located in the package (`*_test.go`), integration tests under `test/` when the change crosses a process boundary. Do not defer tests to a later commit or PR.
- **Docs travel with the code.** Any change to the metric contract, labels, JSON/API shape, or architecture must update ARCHITECTURE.md (and DESIGN.md when relevant) in the same commit. Never let the authority documents drift from the implementation.
- A commit that changes behavior without tests or docs updates must state why neither applies.

## Git Conventions

- Commit on every meaningful step. One logical change per commit.
- Commit messages: imperative mood, describe what and why.

## Security (Binding)

- ESXi credentials: mounted YAML, restricted permissions, never logged (§21.2).
- Confluence credentials: environment variables only (§21.3).
- Exporter HTTP: TLS + basic auth via exporter-toolkit web config (§21.1).
- Mutation endpoints: same-origin, JSON content type, custom header, no CORS (§21.4).
- The application must only be deployed on a trusted network or behind an auth proxy.

## Non-Goals (Do Not Implement in V1)

From ARCHITECTURE.md §3 and DESIGN.md §3:
- Docker inventory
- Real-time monitoring or alerting
- Inventory editing or lifecycle management
- vCenter orchestration
- Per-guest performance metrics
- Persistent inventory database
- Global storage-total calculation
- Authentication/authorization in the web UI
- Guest filesystem utilization
- Remote/network libvirt storage pools
- React, Vue, or any frontend framework

## Decision Checklist

Before implementing any feature:
1. Is it consistent with ARCHITECTURE.md?
2. Is it consistent with DESIGN.md?
3. Is it a V1 goal or a non-goal?
4. Does it respect the metric contract?
5. Is it covered by the testing strategy?
6. Can it be built inside Docker?
