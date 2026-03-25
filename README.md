# Arkeep

> Your infra's Ark — back up everything, keep it yours.

Arkeep is an open-source backup management tool with a server/agent architecture.
Deploy the server once, install lightweight agents on every machine you want to back up,
and manage everything from a single web interface — built on top of
[Restic](https://restic.net/) and [Rclone](https://rclone.org/).

> 🚧 Arkeep is in early access — core features are working and ready for testing.
> Not yet recommended for production use. Star the repository to follow progress.

---

## Table of Contents

- [Why Arkeep](#why-arkeep)
- [Architecture](#architecture)
- [Features](#features)
- [Supported Destinations](#supported-destinations)
- [Deployment](#deployment)
  - [Docker Compose](#docker-compose)
  - [Standalone Binary](#standalone-binary)
  - [Agent via systemd](#agent-via-systemd)
- [Configuration](#configuration)
  - [Server](#server-configuration)
  - [Agent](#agent-configuration)
- [Development](#development)
  - [Prerequisites](#prerequisites)
  - [Getting Started](#getting-started)
  - [Project Structure](#project-structure)
  - [Available Tasks](#available-tasks)
- [FAQ](#faq)
- [Roadmap](#roadmap)
- [Telemetry](#telemetry)
- [Contributing](#contributing)
- [License](#license)

---

## Why Arkeep?

Managing backups across multiple machines means juggling separate Restic configs, cron jobs,
and shell scripts on every host. There is no central view, no unified alerting, and no easy
way to verify everything ran successfully. Arkeep fixes this.

- **Centralized management** — one dashboard for all your servers, no more managing backup configs machine by machine
- **Docker-aware** — automatically discovers containers and volumes, adapts when you add or remove services without restarts
- **OIDC ready** — integrates with Zitadel, Keycloak, Authentik, or any standard identity provider
- **Multi-destination** — apply the 3-2-1 rule with multiple backup destinations per policy
- **End-to-end encryption** — all backups are encrypted client-side; credentials are never stored in plain text
- **Real-time** — live logs and status updates while backups run, accessible from any device
- **No vendor lock-in** — built on [Restic](https://restic.net/) and [Rclone](https://rclone.org/); your data is always accessible even without Arkeep

---

## Architecture

```
┌─────────────────────────────────────────┐
│              Arkeep Server              │
│  ┌──────────┐  ┌──────────────────────┐ │
│  │ REST API │  │     gRPC Server      │ │
│  │  :8080   │  │       :9090          │ │
│  └──────────┘  └──────────────────────┘ │
│  ┌────────────────────────────────────┐ │
│  │ Scheduler│Auth│DB│Notif│WebSocket  │ │
│  └────────────────────────────────────┘ │
└─────────────────────────────────────────┘
         ▲                  ▲
         │ REST/WS          │ gRPC (persistent, Pull)
         │                  │
    ┌────┴────┐      ┌──────┴──────┐
    │   GUI   │      │   Agent(s)  │
    │  (PWA)  │      │  (one per   │
    └─────────┘      │   machine)  │
                     └─────────────┘
```

**Server** exposes a REST API for the GUI (port 8080) and a gRPC server for agents (port 9090). It handles scheduling, notifications, and stores all state in SQLite (default) or PostgreSQL.

**Agent** runs on each machine to be backed up. It initiates a persistent outbound gRPC connection to the server — it never listens on any port. This makes deployment behind NAT and corporate firewalls effortless.

**GUI** is a Vue 3 PWA served directly by the server as embedded static files. No separate web server required.

---

## Features

| Feature | Status |
|---|---|
| Server/agent architecture | ✓ |
| Web GUI (PWA, mobile-first) | ✓ |
| Local auth + OIDC | ✓ |
| Multi-destination (3-2-1) | ✓ |
| Docker volume discovery | ✓ |
| Pre/post hooks (`pg_dump`, etc.) | ✓ |
| Integrity verification | ✓ |
| Retention policies | ✓ |
| Email + webhook notifications | ✓ |
| Restore & restore test | ✓ |
| Helm chart | ✓ |
| Proxmox / VMware integration | 🗓 planned |
| Bandwidth throttling | 🗓 planned |
| BYOK encryption key management | 🗓 planned |

---

## Supported Destinations

| Type | Notes |
|---|---|
| Local filesystem | Direct path on the agent's host |
| S3-compatible | AWS S3, MinIO, Backblaze B2, Cloudflare R2, and more |
| SFTP | Any SSH server |
| Restic REST Server | Self-hosted [rest-server](https://github.com/restic/rest-server) |
| Rclone | 40+ backends including Google Drive, OneDrive, Azure Blob, and more |

---

## Deployment

### Docker Compose

The simplest way to get started. All images are published to both
[GitHub Packages](https://github.com/orgs/arkeep-io/packages?repo_name=arkeep) and
[Docker Hub](https://hub.docker.com/repositories/arkeepio).

**Server only** (GUI included):

```bash
curl -O https://raw.githubusercontent.com/arkeep-io/arkeep/main/deploy/docker/docker-compose.yml
curl -O https://raw.githubusercontent.com/arkeep-io/arkeep/main/deploy/docker/.env.example
cp .env.example .env
# Edit .env — at minimum set ARKEEP_SECRET_KEY and ARKEEP_AGENT_SECRET
docker compose up -d
```

The GUI is available at `http://localhost:8080`.

**gRPC TLS with Docker:**

The agent connects with TLS by default. Two options for the server:

- **Reverse proxy (recommended):** put Caddy or Nginx in front and let it handle TLS termination for HTTP (port 8080) and gRPC (port 9090). No cert config inside the containers.
- **Direct TLS:** mount a certificate and set `ARKEEP_GRPC_TLS_CERT`/`ARKEEP_GRPC_TLS_KEY` on the server container (see the commented lines in `docker-compose.yml` and `.env.example`).

The **all-in-one** compose file sets `ARKEEP_GRPC_INSECURE=true` on the agent automatically, since server and agent share the same private Docker network.

**Agent only** (on the machines you want to back up):

```bash
curl -O https://raw.githubusercontent.com/arkeep-io/arkeep/main/deploy/docker/docker-compose.agent.yml
# Set ARKEEP_SERVER_ADDR and ARKEEP_AGENT_SECRET in your environment or .env
docker compose -f docker-compose.agent.yml up -d
```

**All-in-one** (server + agent on the same host):

```bash
curl -O https://raw.githubusercontent.com/arkeep-io/arkeep/main/deploy/docker/docker-compose.all.yml
docker compose -f docker-compose.all.yml up -d
```

---

### Standalone Binary

Pre-built binaries for Linux, macOS, and Windows are available on the
[Releases](https://github.com/arkeep-io/arkeep/releases) page.

**Server:**

```bash
# Download and extract the server binary for your platform
curl -L https://github.com/arkeep-io/arkeep/releases/latest/download/arkeep-server_linux_amd64.tar.gz | tar xz

# Generate secrets
export ARKEEP_SECRET_KEY=$(openssl rand -hex 32)
export ARKEEP_AGENT_SECRET=$(openssl rand -hex 32)

./arkeep-server \
  --db-dsn /var/lib/arkeep/arkeep.db \
  --data-dir /var/lib/arkeep/data \
  --http-addr :8080 \
  --grpc-addr :9090 \
  --grpc-tls-cert /etc/arkeep/server.crt \
  --grpc-tls-key  /etc/arkeep/server.key
```

> **TLS:** provide a certificate for `--grpc-tls-cert`/`--grpc-tls-key` in production.
> The simplest approach is to put Caddy or Nginx in front and let them handle
> TLS termination for both ports. When running without a reverse proxy, obtain a
> certificate with `certbot` or use a self-signed one (`openssl req -x509 ...`).

**Agent:**

```bash
curl -L https://github.com/arkeep-io/arkeep/releases/latest/download/arkeep-agent_linux_amd64.tar.gz | tar xz

./arkeep-agent \
  --server-addr your-server:9090 \
  --agent-secret your-agent-secret \
  --state-dir /var/lib/arkeep-agent
```

> **TLS:** the agent connects with TLS by default using the system certificate pool.
> No extra flags are needed when the server certificate is from a trusted CA (Let's Encrypt).
> For self-signed certs add `--grpc-tls-ca /path/to/ca.crt`.

---

### Agent via systemd

A systemd unit file is provided at
[`deploy/systemd/arkeep-agent.service`](deploy/systemd/arkeep-agent.service).

```bash
# Copy the binary
sudo cp arkeep-agent /usr/local/bin/arkeep-agent
sudo chmod +x /usr/local/bin/arkeep-agent

# Copy and edit the unit file
sudo cp deploy/systemd/arkeep-agent.service /etc/systemd/system/
sudo systemctl daemon-reload

# Create an environment file with your credentials
sudo mkdir -p /etc/arkeep
sudo tee /etc/arkeep/agent.env > /dev/null <<EOF
ARKEEP_SERVER_ADDR=your-server:9090
ARKEEP_AGENT_SECRET=your-agent-secret
# For self-signed server certs only — leave empty for Let's Encrypt/trusted CAs:
# ARKEEP_GRPC_TLS_CA=/etc/arkeep/ca.crt
EOF
sudo chmod 600 /etc/arkeep/agent.env

sudo systemctl enable --now arkeep-agent
sudo journalctl -u arkeep-agent -f
```

---

## Configuration

All options can be set via CLI flags or environment variables. CLI flags take
precedence over environment variables when both are provided.

### Server Configuration

| Flag | Env | Default | Description |
|---|---|---|---|
| `--http-addr` | `ARKEEP_HTTP_ADDR` | `:8080` | HTTP API and GUI listen address |
| `--grpc-addr` | `ARKEEP_GRPC_ADDR` | `:9090` | gRPC listen address for agents |
| `--grpc-tls-cert` | `ARKEEP_GRPC_TLS_CERT` | — | Path to PEM certificate for gRPC TLS (requires `--grpc-tls-key`) |
| `--grpc-tls-key` | `ARKEEP_GRPC_TLS_KEY` | — | Path to PEM private key for gRPC TLS (requires `--grpc-tls-cert`) |
| `--db-driver` | `ARKEEP_DB_DRIVER` | `sqlite` | Database driver (`sqlite` or `postgres`) |
| `--db-dsn` | `ARKEEP_DB_DSN` | `./arkeep.db` | SQLite file path or PostgreSQL DSN |
| `--secret-key` | `ARKEEP_SECRET_KEY` | — | **Required.** Master key for AES-256-GCM credential encryption |
| `--agent-secret` | `ARKEEP_AGENT_SECRET` | — | Shared secret for gRPC agent authentication |
| `--data-dir` | `ARKEEP_DATA_DIR` | `./data` | Directory for RSA JWT keys and server state |
| `--log-level` | `ARKEEP_LOG_LEVEL` | `info` | Log level (`debug`, `info`, `warn`, `error`) |
| `--secure-cookies` | `ARKEEP_SECURE_COOKIES` | `false` | Set `Secure` flag on auth cookies (enable in production over HTTPS) |
| `--telemetry` | `ARKEEP_TELEMETRY` | `true` | Send anonymous usage stats (opt-out) |

**Generating secrets:**

```bash
# Secret key (AES-256, must be kept stable — changing it invalidates all stored credentials)
openssl rand -hex 32

# Agent secret (any random string)
openssl rand -hex 24
```

**PostgreSQL DSN example:**

```
postgres://arkeep:password@localhost:5432/arkeep?sslmode=require
```

### Agent Configuration

| Flag | Env | Default | Description |
|---|---|---|---|
| `--server-addr` | `ARKEEP_SERVER_ADDR` | `localhost:9090` | Server gRPC address (`host:port`) |
| `--agent-secret` | `ARKEEP_AGENT_SECRET` | — | Shared secret (must match server) |
| `--state-dir` | `ARKEEP_STATE_DIR` | `~/.arkeep` | Directory for agent state and extracted binaries |
| `--docker-socket` | `ARKEEP_DOCKER_SOCKET` | *(platform default)* | Docker socket path |
| `--log-level` | `ARKEEP_LOG_LEVEL` | `info` | Log level |
| `--grpc-tls-ca` | `ARKEEP_GRPC_TLS_CA` | — | Path to CA certificate for gRPC TLS (only needed for self-signed server certs) |
| `--grpc-insecure` | `ARKEEP_GRPC_INSECURE` | `false` | Disable TLS for gRPC transport (development only) |

---

## Development

### Prerequisites

| Tool | Version | Install |
|---|---|---|
| Go | 1.26+ | [go.dev](https://go.dev/dl/) |
| Node.js | 22+ | [nodejs.org](https://nodejs.org/) |
| pnpm | 9+ | `corepack enable` |
| Docker | any | [docker.com](https://www.docker.com/) |
| Task | latest | `go install github.com/go-task/task/v3/cmd/task@latest` |
| protoc | latest | `apt install protobuf-compiler` / `brew install protobuf` |
| protoc-gen-go | latest | `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` |
| protoc-gen-go-grpc | latest | `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` |

### Getting Started

```bash
git clone https://github.com/arkeep-io/arkeep
cd arkeep

# Download restic and rclone binaries (embedded into the agent at build time)
task deps:download

# Generate gRPC code from .proto definitions
task proto

# Start the server (http://localhost:8080, gRPC :9090)
task run:server

# In a separate terminal — start the GUI dev server with HMR (http://localhost:5173)
task run:gui

# In a separate terminal — start an agent pointing to the local server
task run:agent
```

The GUI dev server proxies API requests to the server, so you can work on frontend
and backend simultaneously with hot reload on both sides.

> **Note:** In development, the GUI runs as a separate Vite dev server on port 5173.
> In production (binary or Docker), the GUI is compiled and embedded directly inside
> the server binary — no separate process or web server is needed.

**First login:**

Open `http://localhost:8080` in your browser. On first access you will be
redirected to the setup page where you can create the initial admin account.

### Project Structure

```
arkeep/
├── agent/                      # Agent binary
│   ├── cmd/agent/              # Entry point
│   └── internal/
│       ├── connection/         # gRPC client, job stream, state persistence
│       ├── executor/           # Job queue and execution orchestration
│       ├── restic/             # Restic/rclone wrapper (binary extraction, backup, forget, check)
│       ├── docker/             # Docker volume discovery
│       ├── hooks/              # Pre/post backup hook runner
│       └── metrics/            # Host metrics (CPU, RAM, disk) via gopsutil
├── server/                     # Server binary
│   ├── cmd/server/             # Entry point
│   └── internal/
│       ├── api/                # Chi router, HTTP handlers, middleware
│       ├── auth/               # JWT (RS256), local auth, OIDC
│       ├── db/                 # GORM setup, migrations, EncryptedString type
│       ├── repositories/       # Data access layer (explicit queries, no GORM Preload)
│       ├── grpc/               # gRPC server — receives agent streams, dispatches jobs
│       ├── agentmanager/       # In-memory registry of connected agents
│       ├── scheduler/          # gocron-based backup scheduler
│       ├── notification/       # Email (SMTP) and webhook notification senders
│       └── websocket/          # WebSocket hub for real-time GUI updates
├── shared/                     # Code shared between server and agent
│   ├── proto/                  # Protobuf definitions and generated Go code
│   └── types/                  # Shared type definitions
├── gui/                        # Vue 3 PWA frontend
│   └── src/
│       ├── components/         # Reusable UI components (shadcn-vue based)
│       ├── pages/              # Route-level page components
│       ├── stores/             # Pinia state stores
│       ├── services/           # API client, WebSocket client
│       ├── composables/        # Vue composables
│       ├── router/             # Vue Router configuration
│       └── types/              # TypeScript interfaces
├── deploy/
│   ├── docker/                 # Docker Compose files
│   ├── systemd/                # systemd unit file for the agent
│   └── helm/                   # Helm chart
├── go.work                     # Go workspace (agent + server + shared)
└── Taskfile.yml                # Task runner
```

### Available Tasks

```bash
task build          # Build all binaries (server + agent, GUI included)
task build:server   # Build server binary (builds GUI first, then embeds it)
task build:agent    # Build agent binary (downloads restic + rclone first)
task build:gui      # Build the Vue GUI only (output to gui/dist/)
task test           # Run all tests (Go + GUI)
task lint           # Run linters (golangci-lint + vue-tsc)
task proto          # Regenerate gRPC code from .proto definitions
task tidy           # Tidy all Go modules
task clean          # Remove build artifacts

task run:server     # Run the server in development mode (GUI via task run:gui)
task run:agent      # Run the agent in development mode
task run:gui        # Run the GUI dev server with HMR (proxies API to :8080)

task deps:download  # Download restic and rclone binaries for the current platform
```

---

## FAQ

**Why Restic under the hood?**

Restic is battle-tested, content-addressable, and has excellent deduplication.
It handles encryption, chunking, and repository integrity natively. Arkeep adds
the management layer on top — scheduling, multi-machine coordination, a GUI,
notifications — without reinventing the storage engine.

**Can I access my backups without Arkeep?**

Yes. Since the underlying engine is Restic, you can always use the `restic` CLI
directly against any repository that Arkeep has created. Your data is never locked in.

**Does the agent need root privileges?**

No. The agent runs as an unprivileged user. The only exception is Docker volume backup:
to access volume mountpoints at `/var/lib/docker/volumes/` on Linux, the agent
needs to be in the `docker` group (or run as root). The Docker Compose deployment
handles this automatically via the socket mount.

**Why does the agent connect to the server, not the other way around?**

Pull architecture means agents work behind NAT, firewalls, and dynamic IPs without
any port-forwarding or VPN. The server never needs to reach out to agents — agents
maintain a persistent gRPC stream and receive jobs through it.

**SQLite or PostgreSQL?**

SQLite is the default and works well for most deployments. Switch to PostgreSQL if
you need high concurrency (many agents running jobs simultaneously) or if you want
to run multiple server replicas behind a load balancer.

**Is there a Kubernetes deployment?**

Yes. A Helm chart is available in `deploy/helm/`. Set `grpc.tls.existingSecret` to the name of a TLS Secret (type `kubernetes.io/tls`) to enable TLS on the gRPC port — cert-manager with Let's Encrypt is the recommended approach. For simpler setups, Docker Compose on a single node is also supported.

---

## Roadmap

### v1.0 — Production-ready core
- [x] Restore & restore test
- [x] Helm chart
- [ ] Comprehensive test coverage (server + agent + GUI)
- [ ] Full documentation site

### v1.x — Integrations
- [ ] Proxmox backup (VM and LXC)
- [ ] VMware vSphere integration

### v2.0 — Advanced features
- [ ] Bandwidth throttling
- [ ] BYOK encryption key management

---

## Telemetry

Arkeep sends anonymous usage statistics once per day to help prioritize
development. **No personal data, backup contents, credentials, or hostnames
are ever transmitted.**

What is sent: a stable random instance ID, Arkeep version, OS, number of
connected agents, and number of active policies.

Aggregate stats are public at: https://telemetry.arkeep.io/stats

To opt out: set `ARKEEP_TELEMETRY=false` or pass `--telemetry=false`.

---

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

## License

Arkeep is licensed under the [Apache License 2.0](LICENSE).

Copyright 2026 Filippo Crotti / Arkeep Contributors