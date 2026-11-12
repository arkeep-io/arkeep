# Arkeep agent

> Your infra's Ark — back up everything, keep it yours.

Open-source, self-hosted control for [Restic](https://restic.net/) and [Rclone](https://rclone.org/): one server, a lightweight agent on every machine, one dashboard.

This image runs the **Arkeep agent** on a machine you want to back up. It dials out to the [Arkeep server](https://hub.docker.com/r/arkeepio/arkeep-server) over gRPC, enrolls on first start and switches to mTLS on its own. The agent never listens on a port, so it works behind NAT and firewalls. restic and rclone are embedded: nothing else to install.

**[Website](https://arkeep.io)** · **[Documentation](https://arkeep.io/docs/)** · **[Quickstart](https://arkeep.io/docs/quickstart/)** · **[GitHub](https://github.com/arkeep-io/arkeep)**

## Quick start

You need a running Arkeep server and the `ARKEEP_AGENT_SECRET` you set on it.

```bash
mkdir arkeep-agent && cd arkeep-agent
curl -O https://raw.githubusercontent.com/arkeep-io/arkeep/main/deploy/docker/docker-compose.agent.yml
cat > .env <<'EOF'
ARKEEP_SERVER_ADDR=your-server:9090
ARKEEP_AGENT_SECRET=same-value-as-on-the-server
EOF
docker compose -f docker-compose.agent.yml up -d
```

The agent shows up under **Agents** within a few seconds. Then add a destination, create a policy and run it.

Server behind a reverse proxy: see [Reverse proxy and TLS](https://arkeep.io/docs/installation/reverse-proxy-tls/). Without Docker (systemd, Windows, macOS): see [Binaries](https://arkeep.io/docs/installation/binary/).

## Tags

| Tag | Meaning |
|---|---|
| `vX.Y.Z` | A specific release. **Use this in production**, the same version as the server. |
| `latest` | The most recent release. |

Every tag is a multi-platform image for `linux/amd64` and `linux/arm64`. The same images are published on GitHub Container Registry as `ghcr.io/arkeep-io/arkeep-agent`. Linux armv7, mips and mipsle agents are available as [standalone binaries](https://github.com/arkeep-io/arkeep/releases/latest).

## Volumes

| Mount | Purpose |
|---|---|
| `/var/lib/arkeep-agent` | Agent identity and certificates. Persist it, or the agent enrolls again. |
| `/:/hostfs:ro` | Host filesystem, so the paths you enter in the GUI resolve. Use `:rw` to restore in place. |
| `/var/run/docker.sock` | Docker volume discovery and pre/post-backup hooks. Optional: it gives full control of Docker. Without it, in-place restores skip all Docker volumes and can't protect bind mounts of running containers. |
| `/var/lib/docker/volumes` | Back up Docker named volumes (same path as on the host). |

The agent runs as root by default, to read files owned by any user. Set `PUID` (and optionally `PGID`) to drop to a specific user.

## Main environment variables

| Variable | Required | Description |
|---|---|---|
| `ARKEEP_SERVER_ADDR` | yes | gRPC address of the server, `host:port`, no scheme |
| `ARKEEP_AGENT_SECRET` | yes | Must match the server |
| `ARKEEP_SERVER_HTTP_ADDR` | no | Server HTTPS URL for enrollment, required behind a reverse proxy |
| `ARKEEP_AGENT_HOSTNAME` | no | Name shown in the GUI (default: the host's name) |
| `PUID` / `PGID` | no | Run as this user instead of root |

Every option is in the [agent configuration reference](https://arkeep.io/docs/reference/agent-config/).

## Security

Images are built by GoReleaser in GitHub Actions from a digest-pinned Alpine base and scanned with Trivy. The embedded restic and rclone binaries are pinned by SHA-256 and verified against their upstream GPG signatures. Report vulnerabilities privately as described in [SECURITY.md](https://github.com/arkeep-io/arkeep/blob/main/SECURITY.md).

## License

[Apache-2.0](https://github.com/arkeep-io/arkeep/blob/main/LICENSE)
