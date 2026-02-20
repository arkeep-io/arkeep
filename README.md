# Arkeep

> Your infrastructure's ark — centralized backup management for self-hosted infrastructure.

Arkeep is an open-core backup management tool with a server/agent architecture.
Deploy the server once, install lightweight agents on every machine you want to back up,
and manage everything from a single web interface.

## Why Arkeep?

- **Centralized management** — one dashboard for all your servers, no more managing backup configs machine by machine
- **Docker-aware** — automatically discovers containers and volumes, adapts when you add or remove services without restarts
- **OIDC ready** — integrates with Zitadel, Keycloak, Authentik, or any standard identity provider
- **Multi-destination** — apply the 3-2-1 rule with multiple backup destinations per policy
- **End-to-end encryption** — all backups are encrypted, credentials are never stored in plain text
- **Real-time** — live logs and status updates while backups run, from any device

## Status

> ⚠️ Arkeep is currently in active development and not yet ready for production use.
> Star the repository to follow progress.

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
         │ REST             │ gRPC (persistent, Pull)
         │                  │
    ┌────┴────┐      ┌──────┴──────┐
    │   GUI   │      │   Agent(s)  │
    │  (PWA)  │      │  (one per   │
    └─────────┘      │   machine)  │
                     └─────────────┘
```

Agents connect to the server — they never expose ports.
This makes deployment behind NAT and corporate firewalls effortless.

## Features

| Feature | Free | Enterprise |
|---|---|---|
| Server/agent architecture | ✓ | ✓ |
| Web GUI (PWA, mobile-first) | ✓ | ✓ |
| Local auth + OIDC | ✓ | ✓ |
| Multi-destination (3-2-1) | ✓ | ✓ |
| Docker volume discovery | ✓ | ✓ |
| Pre/post hooks (pg_dump, etc.) | ✓ | ✓ |
| Integrity verification | ✓ | ✓ |
| Restore & restore test | ✓ | ✓ |
| Retention policies | ✓ | ✓ |
| Email + webhook notifications | ✓ | ✓ |
| RBAC with custom roles | — | ✓ |
| Audit logs (ISO 27001) | — | ✓ |
| Multi-tenant (MSP) | — | ✓ |
| Priority support | — | ✓ |

## Supported Destinations

- Local filesystem
- S3-compatible (AWS S3, MinIO, Backblaze B2, and more)
- SFTP
- Restic REST Server
- Any rclone-supported provider (40+ backends)

## Roadmap

- **v1.0** — Production-ready core, all free features complete
- **v1.x** — Proxmox integration (VM and LXC backup)
- **v2.0** — Bandwidth throttling, BYOK encryption key management

## Development

### Prerequisites

- Go 1.26+
- Node.js 22+
- Docker
- [Task](https://taskfile.dev) — `go install github.com/go-task/task/v3/cmd/task@latest`
- `protoc` with `protoc-gen-go` and `protoc-gen-go-grpc`

### Getting started
```bash
git clone https://github.com/arkeep/arkeep
cd arkeep
task proto
task run:server
```

### Available tasks

| Task | Description |
|---|---|
| `task build` | Build all binaries |
| `task test` | Run all tests |
| `task lint` | Run linters |
| `task proto` | Regenerate gRPC code from .proto |
| `task tidy` | Tidy all Go modules |
| `task clean` | Remove build artifacts |

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

## License

Arkeep core is licensed under [AGPLv3](LICENSE).
Enterprise features are available under a commercial license — contact us at hello@arkeep.io.