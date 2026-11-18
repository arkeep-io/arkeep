# 0019. Send notifications to push and chat services through shoutrrr

- **Status:** Proposed
- **Date:** 2026-11-16
- **Deciders:** Arkeep maintainers

## Context

Arkeep delivers notification events (job succeeded or failed, agent offline or
back online) by email and through one generic webhook that POSTs a JSON
payload. Users who rely on push or chat services such as Gotify, ntfy,
Telegram or Discord have to run a relay between the webhook and the service
([discussion #433](https://github.com/arkeep-io/arkeep/discussions/433),
[issue #446](https://github.com/arkeep-io/arkeep/issues/446)). They also want
a different priority per event, for example a silent success and a loud
failure on Gotify.

Each service has its own API, authentication and priority scale (Gotify 0–10,
ntfy 1–5 or min…max, Pushover −2…2). Writing and maintaining a client for each
one in Arkeep would be a large, ongoing effort.

## Decision

We will send notifications to push and chat services through
[`github.com/nicholas-fedor/shoutrrr`](https://github.com/nicholas-fedor/shoutrrr),
the maintained fork of `containrrr/shoutrrr`, which addresses each service with
a URL (`gotify://host/token`, `telegram://token@telegram?chats=…`).

- A **notification provider** (`notification_providers` table) is one service.
  It stores its type, its non-secret fields as JSON (`config`) and its secret
  fields as JSON encrypted at rest (`secrets`,
  [0007](0007-encrypt-credentials-at-rest.md)), plus a priority per event type.
  Secret fields are never returned by the API.
- **The server builds the shoutrrr URL** from these fields at send time
  (`server/internal/notification/provider.go`). Users fill in what the service
  needs (server URL, token, chat IDs, the webhook URL the service gives them);
  they never write shoutrrr URLs, except with the `custom` type, kept for the
  services without a dedicated form. The stored data is ours, not shoutrrr's
  URL format.
- The server is the authority on validation: a provider is saved only if its
  URL is accepted by shoutrrr and every priority is accepted by the service,
  checked without sending anything.
- shoutrrr and net/http errors may quote the URL, which holds the secrets:
  every error from a provider is redacted before it is logged, stored in
  `notification_delivery_queue.last_error` or returned by the API.
- Deliveries to providers use the existing queue and retrier, with type
  `provider` and a `provider_id` deleted with the provider. They follow the
  global event toggles and the per-policy overrides, like email and webhook.
- shoutrrr is confined to the `notification` package and pinned to an exact
  version ([0013](0013-pin-and-verify-every-dependency.md)).

## Consequences

- One dependency covers the services users ask for, and more through the
  `custom` type.
- Adding a service with a dedicated form means a type in `providerTypes`
  (server) and `PROVIDER_TYPES` (GUI), with its fields; no migration.
- shoutrrr brings transitive dependencies (MQTT, XMPP clients) into the server
  build, reviewed and pinned like any other module.
- Replacing shoutrrr would mean rewriting `providerURL` and the send path, but
  not the stored providers, since they do not hold shoutrrr URLs (except the
  `custom` type).
- The generic webhook is unchanged: it stays the channel for custom
  integrations that need the structured, HMAC-signed payload.

## Alternatives considered

- **`containrrr/shoutrrr`** — the original project; its last release dates
  from 2023.
- **`nikoksr/notify`** — popular, but has no Gotify or ntfy support and no URL
  format to cover further services.
- **A client per service in Arkeep** — full control, but a large amount of
  code to write and keep in step with each service's API.
- **Storing raw shoutrrr URLs** — less code, but users would have to learn the
  URL syntax of each service, and the token would sit in a value the GUI
  cannot show in part.

## References

- [Discussion #433](https://github.com/arkeep-io/arkeep/discussions/433),
  [issue #446](https://github.com/arkeep-io/arkeep/issues/446)
- `server/internal/notification/provider.go`,
  `server/internal/api/notification_providers.go`,
  `gui/src/lib/notificationProviders.ts`
