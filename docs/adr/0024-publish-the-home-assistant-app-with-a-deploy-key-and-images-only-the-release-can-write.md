# 0024. Publish the Home Assistant app with a deploy key, and images only the release can write

- **Status:** Accepted
- **Date:** 2026-11-19
- **Deciders:** Arkeep maintainers

Supersedes [0018](0018-publish-the-agent-as-a-home-assistant-app.md).

## Context

Home Assistant OS runs third-party software as *apps* (called add-ons before
Home Assistant 2026.2): containers started and updated by its Supervisor. Its
users have no Docker access and usually no shell, so neither the compose file
nor the systemd unit of the agent can be used there. An app is the only way to
run the agent on such a machine.

Apps are installed from *app repositories*. There is no central catalog for
third parties: a user adds the URL of a Git repository to the app store. The
Supervisor clones it (depth 1, optionally a branch given as `URL#branch`),
expects `repository.yaml` at its root and treats every folder with a
`config.yaml` as an app. Three properties of this mechanism shape the
decision:

- the `version` of `config.yaml` is the tag of the image the Supervisor pulls
  and the value it compares to offer an update. The image must therefore exist
  before the version is published;
- the Supervisor identifies a repository by a hash of its URL, branch
  included. Changing the URL later turns the apps into different apps, which
  users have to install again;
- an app gets Home Assistant's folders, not the host filesystem. Access to
  Docker requires the user to turn off the app's protection mode and gives the
  app the lowest security rating.

The agent is already released as a multi-platform image (`linux/amd64`,
`linux/arm64`, the two architectures Home Assistant supports), configured by
environment variables.

ADR 0018 published the app from the release workflow with `GITHUB_TOKEN`,
pointing it at the GHCR image, which the release also pushed with
`GITHUB_TOKEN`. A workflow on any branch can request `contents: write` and
`packages: write` for that token, without the `release` environment (ADR
0017): anyone able to push a branch, or holding a stolen token of someone
who can, could fast-forward `home-assistant` (another image, `full_access`,
the Docker socket, host network) or overwrite a GHCR tag, and every Home
Assistant installation would be offered the result. The ruleset on the
branch only blocked deletion and force pushes (P-13).

## Decision

- **The app is a description of the released agent image, not a second
  image.** `deploy/home-assistant/arkeep-agent/config.yaml` names
  `arkeepio/arkeep-agent` on Docker Hub and sets `legacy: true`, the Supervisor mode
  for images without Home Assistant labels, which passes each option to the
  container as an environment variable. Options are therefore named after the
  agent's variables (`ARKEEP_SERVER_ADDR`, …); translations give them readable
  labels. Nothing is built for Home Assistant.
- **The source lives in `deploy/home-assistant`** and is reviewed like the
  other deployment files.
- **Each stable release publishes it on the `home-assistant` branch**, from
  `release.yml`, after the images are published and attested.
  `scripts/publish-home-assistant-app.sh` copies the folder to the root of
  that branch and sets `version` to the release tag. The branch holds nothing
  else and is never committed to by hand. Users add
  `https://github.com/arkeep-io/arkeep#home-assistant`. Pre-releases are not
  published: Home Assistant would offer them to everyone.
- **Only the release pushes the branch.** It pushes over SSH with a deploy key
  of this repository, with write access, whose private key is the
  `HOME_ASSISTANT_DEPLOY_KEY` secret of the `release` environment; GitHub's
  host key is pinned. The `protect-home-assistant` ruleset restricts creating,
  updating and deleting the branch and force pushes, with deploy keys as the
  only bypass. No other deploy key has write access.
- **Images only the release can write.** The app and the compose examples use
  Docker Hub (`arkeepio/arkeep-server`, `arkeepio/arkeep-agent`), pushed with
  the organization token of the `release` environment. GHCR keeps the same
  images and the Helm chart, but the repository's `GITHUB_TOKEN` has the Read
  role on those packages: the release writes them with `GHCR_TOKEN`, a classic
  token of an organization owner with `write:packages`, with `GHCR_USERNAME`
  as a variable, both in the `release` environment. This removes the GHCR
  exception noted in ADR 0017.
- **The app is confined to Home Assistant's folders**: `/backup`, `/share` and
  `/media` read-write; `/homeassistant`, `/app_configs` and `/ssl` read-only.
  It asks for no Docker access, no host network, no port and no extra
  capability. `ARKEEP_DOCKER_HOST_ROOT` is set to `/`, so paths entered in
  Arkeep are used as they are instead of being looked up under `/hostfs`.
  State goes to `/data`, the app's persistent folder.
- **The agent's environment variables and a host root of `/` are part of the
  app's interface.** Renaming a variable that is an option, or changing what
  `/` means, breaks installed apps: such a change updates
  `deploy/home-assistant` in the same pull request.

## Consequences

- Home Assistant users get each agent release as an update, with the same
  version number. There is no separate app version to maintain. A fix to the
  app alone waits for the next release, or is published by running the script
  by hand from the tag.
- No image to build, scan or attest for Home Assistant: the app runs the image
  covered by ADR 0013 and ADR 0017.
- Docker volume sources and hooks that call `docker` do not work on Home
  Assistant. Restoring to the read-only folders is refused by the system. Both
  are documented in the app.
- The `version` in the source is kept on the latest release by the dependency
  pins workflow, like the compose examples. The published value is always set
  by the release.
- A workflow on another branch can neither move `home-assistant` nor
  overwrite an image or the chart on GHCR or Docker Hub: every write needs a
  secret of the `release` environment.
- The deploy key, `GHCR_TOKEN` and the ruleset are GitHub settings outside the
  repository, set up once by an administrator. `GHCR_TOKEN` belongs to a
  person and expires: it must be renewed, and moved if that person leaves.
  Changing the Read role of the GHCR packages back would reopen the gap.
- Apps installed before this decision switch from the GHCR image to the same
  image on Docker Hub at their next update. Anonymous Docker Hub pulls are rate
  limited per address, far above what one installation needs.
- The app is not exercised by CI: no Supervisor runs there. The agent tests
  cover a host root of `/`. Before a change to `deploy/home-assistant` is
  merged, it is tried on a Home Assistant installation as a local app.
- If the Supervisor drops `legacy` mode, the agent image needs the
  `io.hass.*` labels and has to read `/data/options.json` itself. This would
  supersede the first point of the decision, not the repository layout.
- The repository URL is permanent in practice. Moving the app to another
  repository would make users install it again.

## Alternatives considered

- **A separate repository** (`arkeep-io/home-assistant-apps`), the usual
  layout — a second repository to maintain, and a token able to push to it in
  the release environment, to gain a URL without `#home-assistant`.
- **`repository.yaml` at the root of `main`** — the Supervisor would clone the
  whole monorepo, and the version on `main` would move when the release is
  merged, before its images exist.
- **A dedicated image on Home Assistant's base images** (S6 overlay, Bashio) —
  a second image to build, scan and attest at every release, for a script
  that turns `/data/options.json` into environment variables.
- **Building on the device** (a `Dockerfile` and no `image`) — slow and
  fragile, and discouraged by Home Assistant once an app has users.
- **Giving the app the Docker socket or full access** — only possible with
  protection mode off. Home Assistant keeps app data in folders, not in named
  volumes, so there is little to gain.
- **An independent app version** — a second number to bump, with no
  information the release tag does not carry.
- **A GitHub App instead of a deploy key** — a bypass limited to the app in
  the ruleset, at the cost of an app to create and an action to pin.
- **Leave GHCR writable by `GITHUB_TOKEN` and document verification** — the
  release notes already verify attestations with `--source-ref`, but only
  users who run the check would notice an overwritten tag.

## References

- `deploy/home-assistant/`, `scripts/publish-home-assistant-app.sh`,
  `.github/workflows/release.yml`, `.github/workflows/deps-pins.yml`.
- Home Assistant documentation for app developers:
  [app configuration](https://contributors.home-assistant.io/apps/reference/configuration/),
  [publishing](https://contributors.home-assistant.io/apps/guides/publishing/),
  [security](https://contributors.home-assistant.io/apps/guides/security/).
- ADR 0013 (dependency pinning), ADR 0014 (Helm chart, published the same
  way), ADR 0016 (release tags), ADR 0017 (attestations).
- ADR 0018, superseded by this record.
