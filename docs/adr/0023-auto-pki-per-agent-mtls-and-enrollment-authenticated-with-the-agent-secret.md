# 0023. Auto-PKI, per-agent mTLS, and enrollment authenticated with the agent secret

- **Status:** Proposed
- **Date:** 2026-11-19
- **Deciders:** Arkeep maintainers

Supersedes [0006](0006-auto-pki-and-per-agent-mtls.md).

## Context

The gRPC channel carries jobs, repository credentials and logs
([0002](0002-server-agent-architecture-with-agent-initiated-grpc.md)). It must
be encrypted and authenticated in both directions, without asking self-hosters
to run a PKI, and agents must not be able to impersonate each other.

ADR 0006 set up a private CA and per-agent client certificates, obtained by
enrolling once over the server's HTTP API. The agent sent the shared agent
secret in the request and trusted whatever CA certificate came back. Over plain
HTTP, which is the default (`http://<server>:8080`, the compose files, the
packages, the Home Assistant app), anyone on the network path could take the
secret, valid for every agent, and hand the agent a CA of their own, then read
the gRPC channel with every repository password and credential. A forged 405
also made the agent give up its client certificate. ADR 0006 only recommended
HTTPS, which most installations on a local network do not have.

## Decision

- On first start the server generates its own **private CA** (ECDSA P-256) and
  a gRPC server certificate under `<data-dir>/grpc/`.
- Agents verify the server with that CA and a **fixed TLS server name**
  (`arkeep-grpc`), so verification does not depend on how the server is
  addressed.
- An agent **enrolls** once over HTTP or HTTPS (`POST /api/v1/agents/enroll`)
  with a **CSR for a key it generates locally**. The private key never leaves
  the agent.
- **The agent secret never travels during enrollment.** The agent and the
  server use it as a pre-shared key (`shared/enrollproof`, HMAC-SHA256, one
  label per message, every message bound to the CSR):
  - the request carries the CSR and the agent's proof of the secret over it;
  - a successful answer carries the CA certificate, the client certificate and
    the server's proof over the CSR and both certificates;
  - a server without auto-PKI answers 405 with its proof over the CSR.
  The agent accepts an answer only with a valid server proof. Otherwise it
  stores nothing and tries again later.
- The server still accepts the request of older agents, which send the secret
  itself, and logs a warning. A new agent refuses an older server, whose answer
  has no proof: servers are upgraded before agents.
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

- mTLS works out of the box with no PKI to operate, and enrollment is safe over
  plain HTTP: someone on the path learns no secret, cannot substitute the CA
  and cannot make the agent drop its client certificate.
- The agent secret is a bootstrap credential: whoever holds it can enroll a new
  agent. It must be protected accordingly, and be long and random: an observer
  of an enrollment can try to guess it offline from the HMACs.
- Upgrading an agent before its server stops its enrollment, with a log line
  that says to upgrade the server. Agents already enrolled keep their
  certificate and are not affected.
- The legacy request keeps the old exposure for agents that are not upgraded.
  It can be removed once those agents are gone, with a decision of its own.
- The external-certificate mode trades per-agent identity for a certificate the
  operator already manages. The insecure mode drops encryption as well, and is
  only acceptable where the network itself is trusted.

## Alternatives considered

- **Refuse enrollment over plain HTTP to another host** — simple, but most
  installations on a local network have no TLS on the HTTP port and would need
  a flag or a certificate.
- **Pin the CA fingerprint in the agent configuration** — protects the channel,
  but over HTTP the secret still travels in clear, and every installation has
  one more value to copy.
- **Shared secret only, no mTLS** — no per-agent identity, and the secret
  travels with every connection.
- **Server-generated client keys** — the behavior before ADR 0006: the private
  key crossed the network during enrollment.
- **Require an operator-managed PKI** — too much friction for the target users.

## References

- `shared/enrollproof/`, `server/internal/api/enroll.go`,
  `agent/internal/connection/` (`Enroll`).
- `server/internal/grpc/certs.go`, `server/internal/grpc/identity.go`.
- `SECURITY.md` (agent authentication).
- [Security](https://arkeep.io/docs/concepts/security/) and
  [Reverse proxy and TLS](https://arkeep.io/docs/installation/reverse-proxy-tls/)
  on arkeep.io/docs.
