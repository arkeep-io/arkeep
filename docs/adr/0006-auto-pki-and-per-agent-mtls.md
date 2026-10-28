# 0006. Auto-PKI and per-agent mTLS identity

- **Status:** Accepted
- **Date:** 2026-10-12 (retro-documented; the decision predates this record)
- **Deciders:** Arkeep maintainers

## Context

The gRPC channel carries jobs, repository credentials and logs
([0002](0002-server-agent-architecture-with-agent-initiated-grpc.md)). It must
be encrypted and authenticated in both directions, without asking self-hosters
to run a PKI, and agents must not be able to impersonate each other.

## Decision

- On first start the server generates its own **private CA** (ECDSA P-256) and
  a gRPC server certificate under `<data-dir>/grpc/`.
- Agents verify the server with that CA and a **fixed TLS server name**
  (`arkeep-grpc`), so verification does not depend on how the server is
  addressed.
- An agent **enrolls** once over HTTP(S) (`POST /api/v1/agents/enroll`) with the
  shared agent secret and a **CSR for a key it generates locally**. The server
  returns the CA certificate and a signed client certificate. The private key
  never leaves the agent.
- Each agent row records the **fingerprint of its client certificate**. Every
  RPC that names an agent, or reports on a job, must come from that agent's
  certificate.
- Two other modes remain available, and in both agents are authenticated by the
  shared agent secret only, with no per-agent identity:
  - an **external certificate** (`ARKEEP_GRPC_TLS_CERT`/`KEY`, e.g. from
    Let's Encrypt): the connection is still TLS end to end between the agent
    and the server;
  - an **insecure mode** (`--grpc-insecure`, `ARKEEP_GRPC_INSECURE`), only for
    agents on the same host or on a trusted private network.
- With auto-PKI, the gRPC port (9090) must reach the server unchanged: a proxy
  in front of it must pass TLS through, never terminate it.

## Consequences

- mTLS works out of the box with no PKI to operate.
- The agent secret is a bootstrap credential: whoever holds it can enroll a new
  agent. It must be protected accordingly.
- Enrollment over plain HTTP exposes the secret and the CA certificate to a
  network attacker. HTTPS (or loopback) should be used for enrollment.
- The external-certificate mode trades per-agent identity for a certificate the
  operator already manages. The insecure mode drops encryption as well, and is
  only acceptable where the network itself is trusted.

## Alternatives considered

- **Shared secret only, no mTLS** — no per-agent identity, and the secret
  travels with every connection.
- **Server-generated client keys** — the previous behavior: the private key
  crossed the network during enrollment.
- **Require an operator-managed PKI** — too much friction for the target users.

## References

- `server/internal/grpc/certs.go`, `server/internal/grpc/identity.go`,
  `server/internal/api/enroll.go`, `agent/internal/connection/`.
- [Security](https://arkeep.io/docs/concepts/security/) and
  [Reverse proxy and TLS](https://arkeep.io/docs/installation/reverse-proxy-tls/)
  on arkeep.io/docs; `SECURITY.md`.
