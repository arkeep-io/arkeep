# 0018. Publish the agent as a Home Assistant app from a branch of this repository

- **Status:** Accepted
- **Date:** 2026-11-18
- **Deciders:** Arkeep maintainers

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

## Decision

- **The app is a description of the released agent image, not a second
  image.** `deploy/home-assistant/arkeep-agent/config.yaml` names
  `ghcr.io/arkeep-io/arkeep-agent` and sets `legacy: true`, the Supervisor mode
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
- `GITHUB_TOKEN` must be allowed to push to `home-assistant`: a branch
  protection rule or ruleset on that branch has to let the release workflow
  through. A release whose publication fails has its images and binaries; the
  app keeps offering the previous version until the script is run again.
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

## References

- `deploy/home-assistant/`, `scripts/publish-home-assistant-app.sh`,
  `.github/workflows/release.yml`, `.github/workflows/deps-pins.yml`.
- Home Assistant documentation for app developers:
  [app configuration](https://contributors.home-assistant.io/apps/reference/configuration/),
  [publishing](https://contributors.home-assistant.io/apps/guides/publishing/),
  [security](https://contributors.home-assistant.io/apps/guides/security/).
- ADR 0013 (dependency pinning), ADR 0014 (Helm chart, published the same
  way), ADR 0016 (release tags), ADR 0017 (attestations).
