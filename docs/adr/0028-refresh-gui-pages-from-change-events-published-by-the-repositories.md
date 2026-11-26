# 0028. Refresh GUI pages from change events published by the repositories

- **Status:** Accepted
- **Date:** 2026-11-25
- **Deciders:** Arkeep maintainers

## Context

Following a backup, a draining queue or a snapshot sync meant clicking the
refresh button over and over (#524). The WebSocket only carried per-object
topics (`job:<id>`, `agent:<id>`) and the user's notifications: no list page
could learn that a job was created, that a policy ran or that a snapshot
appeared. A refresh timer chosen per page (#526, first version) was
considered: it polls while nothing changes, lags when something does, and adds
a preference users have to understand.

Writes to jobs, agents, policies, destinations, snapshots, users and the audit
log happen in many packages (API handlers, gRPC server, schedulers, watchdog,
destination queue, snapshot sync). Publishing an event at each call site would
leave some of them out, and every new one would have to remember it.

## Decision

- **Events, not data.** After an object changes the server publishes a
  `change` message `{type, id}` on the WebSocket: the type (`jobs`, `agents`,
  `policies`, `destinations`, `snapshots`, `users`, `audit`) and the object's
  ID, empty when a write changed several objects. The message carries no other
  data: a page that shows the type fetches it again through the REST API, with
  the rights of its session.
- **Published by the repositories.** Each repository of those types is wrapped
  in a decorator (`server/internal/changes`) that publishes after every
  successful write, and only then: a failed write, or one that reports it
  changed nothing, publishes nothing. Transactions live inside the repository
  methods, so a page refreshing on the event reads the committed state.
  - Every write to those tables must go through these repositories, and the
    server must build them with the decorators (`cmd/server/main.go`).
  - **A new write method on one of these repository interfaces must also be
    implemented by its decorator.** The decorator embeds the interface, so a
    method it does not override compiles and silently publishes nothing.
  - Job logs are not announced (the job page gets them as `job.log` on
    `job:<id>`). An agent's heartbeat rewrites its status every 30 seconds:
    only a status that differs from the last one written is announced.
- **Coalesced.** The first change of a type is published at once; the next
  ones within a second are published as one at the end of the second (with an
  ID only if they all concern the same object). A burst of writes then never
  fills a client's send buffer, which would disconnect it.
- **Topics by role.** `changes` carries the types every role can read.
  `changes:admin` carries `users` and `audit`, which only admins can read. The
  server subscribes every connection to `changes`, and only admin connections
  (by the role of the access token the socket opened with) to `changes:admin`.
  A client cannot ask for either in the `topics` query parameter.
- **GUI.** A page declares which types it follows (`useLiveRefresh`) and gets
  its fetch function called with `{ quiet: true }`: data updated in place,
  without loading skeletons, at most once per second, once more if a change
  arrives during a refresh, and once after the socket reconnects (events sent
  while it was down are lost). A page whose requests can overlap drops an
  answer older than the one it shows (`requestOrder`). When the socket stays
  down for 30 seconds, the refresh button of a live page says so; there is no
  polling fallback.

## Consequences

- Pages update as soon as something changes and send no request while nothing
  does. Adding a live page is one `useLiveRefresh` call.
- Every client of a role hears every change of its topics and refreshes the
  pages it shows: with many open tabs, one change costs one list request per
  tab, at most once per second and type.
- Contributors adding a write to these repositories must update the decorator
  and its test (`server/internal/changes/repositories_test.go` checks each
  write publishes once, and not on failure). Reviewers check it on any change
  to the repository interfaces.
- A write that bypasses the repositories (raw SQL in another package) is
  invisible to open pages. Migrations are not concerned: they run before any
  client connects.
- Live updates need WebSocket connections to reach the server through any
  reverse proxy.

## Alternatives considered

- **Refresh timer per page** (#526, first version) — polls when nothing
  changes, lags when something does, and adds a per-page preference.
- **Publish at each call site** — about sixty places, spread over many
  packages, and nothing would catch a missing one.
- **Database triggers or change data capture** — different on SQLite and
  PostgreSQL ([0005](0005-sqlite-by-default-postgresql-supported.md)), and
  much heavier than wrapping the repositories.
- **Full objects in the events** — every type would need its own payload, and
  the server would have to filter the data per role; with the ID only, the
  REST API keeps applying the permissions.

## References

- Issue #524, pull request #526.
- `server/internal/changes/`, `server/internal/api/ws.go`,
  `server/internal/websocket/message.go`.
- `gui/src/composables/useLiveRefresh.ts`, `gui/src/lib/requestOrder.ts`,
  `gui/src/services/websocket.ts`.
