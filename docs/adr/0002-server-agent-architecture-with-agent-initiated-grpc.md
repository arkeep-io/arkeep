# 0002. Server/agent architecture with agent-initiated gRPC

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

Arkeep manages backups on many machines: containers, VMs and bare-metal hosts,
often behind NAT, corporate firewalls or dynamic IP addresses. A central
component must schedule jobs, store state and serve the GUI, while the work
itself (reading files, running restic) has to happen on each machine.

## Decision

- A single **server** holds all state, scheduling, notifications and
  authentication. It exposes a REST API and WebSocket for the GUI (port 8080)
  and a gRPC service for agents (port 9090).
- One **agent** runs on each machine to back up. The agent **opens a persistent
  outbound gRPC connection** to the server and receives its jobs on it. The
  agent never listens on a port, and the server never connects to agents.
- The gRPC contract lives in `shared/proto/agent.proto` and is shared by both
  sides.
- Transport limits are fixed in the binaries: 16 MB per message, keepalive
  pings every 30 s with a 10 s timeout.

## Consequences

- Agents work behind NAT and firewalls without port forwarding or a VPN. Only
  the server needs to be reachable.
- The server can only reach an agent while it is connected: jobs for an
  offline agent wait or fail (see the pending-job timeout).
- Agents trust the server to send them work, which makes the server's security
  critical (see [0006](0006-auto-pki-and-per-agent-mtls.md) and
  [0009](0009-hooks-run-on-the-agent-admin-only.md)).
- Changes to `agent.proto` must stay compatible with agents of the previous
  release, which keep running during upgrades.

## Alternatives considered

- **Server pushes to agents (agents listen)** — requires inbound connectivity
  to every machine, which is impractical behind NAT and firewalls.
- **Agents poll a REST API** — higher latency for job dispatch and live logs,
  and more load than a single persistent stream.

## References

- `README.md` → Architecture, FAQ "Why does the agent connect to the server".
- `shared/proto/agent.proto`, `agent/internal/connection/`, `server/internal/grpc/`.
