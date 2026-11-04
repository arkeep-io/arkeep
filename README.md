# Arkeep

> Your infra's Ark — back up everything, keep it yours.

[![Latest release](https://img.shields.io/github/v/release/arkeep-io/arkeep?sort=semver)](https://github.com/arkeep-io/arkeep/releases/latest)
[![CI](https://github.com/arkeep-io/arkeep/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/arkeep-io/arkeep/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/arkeep-io/arkeep)](LICENSE)
[![Docker pulls](https://img.shields.io/docker/pulls/arkeepio/arkeep-server)](https://hub.docker.com/r/arkeepio/arkeep-server)

**Manage backups across all your servers from a single dashboard.**

Arkeep is an open-source, self-hosted backup manager built on [Restic](https://restic.net/)
and [Rclone](https://rclone.org/). Run one central server, deploy a lightweight agent on each
machine, and manage backup schedules, destinations, notifications and restores from one web interface.

Use it to bring backups for your Docker hosts, databases and directories into one place.
Your backup repositories remain standard Restic repositories, accessible even without Arkeep.

**[Get started: back up and restore your first file](https://arkeep.io/docs/quickstart/)**

[Website](https://arkeep.io) · [Documentation](https://arkeep.io/docs/) · [Installation options](https://arkeep.io/docs/installation/docker-compose/) · [Discussions](https://github.com/arkeep-io/arkeep/discussions)

![Arkeep dashboard](docs/images/1.jpg)

---

## Features

- **One policy, many destinations** — local disk, S3-compatible storage, SFTP, restic REST server and 40+ Rclone backends, for a real 3-2-1 setup
- **Docker-aware** — volumes are discovered on their own; databases stream straight into restic with `pg_dump`, `mysqldump` and friends
- **Retention and integrity checks** — per destination, on their own schedule
- **Browse and restore** — open any snapshot, download a file or a folder, restore a selection in place or elsewhere
- **Know when it fails** — email, webhooks (Slack, Discord, n8n…) and Healthchecks.io pings
- **Your login, your rules** — local accounts with TOTP 2FA, or single sign-on with any OIDC provider
- **No lock-in** — repositories stay plain restic, readable without Arkeep

More on the [website](https://arkeep.io/#features).

## How it works

The **server** hosts the web GUI and the REST API (port 8080) and schedules every job. An **agent** runs on
each machine you back up: it dials out to the server over gRPC (port 9090), enrolls on first start and
switches to mTLS on its own. Agents never listen on a port, so they work behind NAT and firewalls. State lives
in SQLite by default, or PostgreSQL.

Details in [Architecture](https://arkeep.io/docs/concepts/architecture/).

## Quickstart

Start with the **[step-by-step quickstart](https://arkeep.io/docs/quickstart/)**.
It runs the server and an agent on the same machine with Docker Compose, backs up a folder
to a local destination, and walks you through restoring a file to a separate location.

You need a Linux machine, or Docker Desktop, with Docker and Docker Compose.
The guide covers the commands, secrets and filesystem permissions needed for the trial.

By the end, you will have:

1. Started the server and connected your first agent.
2. Created the admin account and a backup destination.
3. Configured a policy and completed your first backup.
4. Browsed a snapshot and restored a file.

The local destination is for trying the workflow. For an off-site copy, add an
[S3, SFTP or Rclone destination](https://arkeep.io/docs/guides/destinations/).

### Deploy across your servers

Once you have tried the workflow, follow the
[Docker Compose installation guide](https://arkeep.io/docs/installation/docker-compose/)
to deploy the server and add agents on other machines.

Other deployment options:
[Standalone binary](https://arkeep.io/docs/installation/binary/) ·
[Agent via systemd](https://arkeep.io/docs/installation/agent-systemd/) ·
[Kubernetes (Helm)](https://arkeep.io/docs/installation/kubernetes/) ·
[Reverse proxy and TLS](https://arkeep.io/docs/installation/reverse-proxy-tls/).

Container images are available on [Docker Hub](https://hub.docker.com/u/arkeepio) and
[GitHub Packages](https://github.com/orgs/arkeep-io/packages?repo_name=arkeep).

## Documentation

Everything else lives at **[arkeep.io/docs](https://arkeep.io/docs/)**:

| | |
|---|---|
| **Install** | [Docker Compose](https://arkeep.io/docs/installation/docker-compose/) · [Binary](https://arkeep.io/docs/installation/binary/) · [Kubernetes (Helm)](https://arkeep.io/docs/installation/kubernetes/) · [Agent via systemd](https://arkeep.io/docs/installation/agent-systemd/) · [Reverse proxy and TLS](https://arkeep.io/docs/installation/reverse-proxy-tls/) |
| **Operate** | [Upgrading](https://arkeep.io/docs/installation/upgrading/) · [Backing up Arkeep itself](https://arkeep.io/docs/installation/backing-up-arkeep/) · [Health and metrics](https://arkeep.io/docs/reference/health-and-metrics/) |
| **Use** | [Destinations](https://arkeep.io/docs/guides/destinations/) · [Policies](https://arkeep.io/docs/guides/policies/) · [Databases](https://arkeep.io/docs/guides/databases/) · [Docker volumes](https://arkeep.io/docs/guides/docker-volumes/) · [Snapshots and restore](https://arkeep.io/docs/guides/snapshots-and-restore/) · [Notifications](https://arkeep.io/docs/guides/notifications/) |
| **Reference** | [Server configuration](https://arkeep.io/docs/reference/server-config/) · [Agent configuration](https://arkeep.io/docs/reference/agent-config/) · [Webhook events](https://arkeep.io/docs/reference/webhook-events/) · [FAQ](https://arkeep.io/docs/reference/faq/) |

## Roadmap

Backups and recovery come first. The next stages build on that foundation:

- **v1.0 — Production-ready core:** complete automated restore testing and broaden test coverage.
  Snapshot browsing, file download and restore, the Helm chart and the documentation site are already available.
- **v1.x — Inventory:** hosts, containers and VMs in one view, with a backup coverage map.
- **v2.0 — Update orchestration:** Docker, OS and Proxmox updates gated by a recent verified backup, with rollback.
- **v2.x — Intelligence:** CVE correlation, AI reports, bandwidth throttling and BYOK keys.

See the [roadmap](https://arkeep.io/#roadmap) for shipped and planned items.
The inventory, update orchestration and intelligence stages describe future work.

## Telemetry

Arkeep sends an anonymous daily ping (random instance ID, version, OS, number of agents and policies). No
personal data, backup contents, credentials or hostnames. Turn it off with `ARKEEP_TELEMETRY=false`; see
[Telemetry](https://arkeep.io/docs/reference/telemetry/).

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and the
[development setup](https://arkeep.io/docs/contributing/development/) first. Security issues: see
[SECURITY.md](SECURITY.md).

## Support Arkeep

Hey 👋 I’m Filippo, the independent maintainer of Arkeep.

Arkeep is built around a simple idea: before touching production, you should know you have a backup you can actually recover from.

If Arkeep saves you time, helps your workflow, or gives you more confidence managing your infrastructure, you can support the work behind it through GitHub Sponsors.

Sponsoring is a bit like buying me a coffee for open source: it helps me spend more time on bug fixes, documentation, releases, restore testing, and the next pieces of the roadmap, from inventory to safer update orchestration.

[Become a sponsor](https://github.com/sponsors/arkeep-io)

## Sponsors

Thank you to everyone who has sponsored Arkeep, past and present. Your support keeps the project independent and moving forward.

<!-- sponsors --><a href="https://github.com/123-soleil"><img src="https://github.com/123-soleil.png?size=120" width="60" height="60" alt="123soleil" /></a><!-- sponsors -->

## License

Arkeep is licensed under the [Apache License 2.0](LICENSE).

Copyright 2026 Filippo Crotti / Arkeep Contributors
