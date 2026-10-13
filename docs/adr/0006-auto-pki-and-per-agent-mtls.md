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
- An external certificate (`ARKEEP_GRPC_TLS_CERT`/`KEY`) and an insecure mode
  (`ARKEEP_GRPC_INSECURE`) remain available. In both, agents share one secret
  and no per-agent identity is enforced.

## Consequences

- mTLS works out of the box with no PKI to operate.
- The agent secret is a bootstrap credential: whoever holds it can enroll a new
  agent. It must be protected accordingly.
- Enrollment over plain HTTP exposes the secret and the CA certificate to a
  network attacker. HTTPS (or loopback) should be used for enrollment.
- The external-certificate and insecure modes trade per-agent identity for
  simpler setups behind TLS-terminating proxies or on a single machine.

## Alternatives considered

- **Shared secret only, no mTLS** — no per-agent identity, and the secret
  travels with every connection.
- **Server-generated client keys** — the previous behavior: the private key
  crossed the network during enrollment.
- **Require an operator-managed PKI** — too much friction for the target users.

## References

- `server/internal/grpc/certs.go`, `server/internal/grpc/identity.go`,
  `server/internal/api/enroll.go`, `agent/internal/connection/`.
- `SECURITY.md`.
