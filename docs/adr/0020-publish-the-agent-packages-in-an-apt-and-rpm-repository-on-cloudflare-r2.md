# 0020. Publish the agent packages in an APT and RPM repository on Cloudflare R2

- **Status:** Proposed
- **Date:** 2026-11-18
- **Deciders:** Arkeep maintainers

## Context

Every release attaches `.deb` and `.rpm` packages of the agent for Linux
amd64, arm64 and armv7 (#472). They install the agent as a systemd service,
but each upgrade still means downloading the new package on every machine. A
user with several machines asked for agent updates that do not need a login on
each of them (discussion #450). The agent runs as root, so whatever updates it
must not become a way to run code on every machine it is installed on.

Package managers already solve this: once a machine trusts a repository and
its key, `apt upgrade` or `dnf upgrade`, and the automatic updates built on
them, install new versions and check their signatures. That needs a
repository: metadata listing the packages, signed with a key the machines
trust, served over HTTPS.

`arkeep.io` is a Cloudflare zone, and the website is served from it. GitHub
Pages was set aside: the project does not want its distribution tied to Pages.

## Decision

- **Hosting.** The repository is the R2 bucket `arkeep-packages` of the
  Cloudflare account that holds `arkeep.io`, served at
  `https://pkg.arkeep.io` through an R2 custom domain:
  - `gpg.key`: the public key;
  - `apt/`: an APT repository, suite `stable`, component `main`,
    architectures `amd64`, `arm64` and `armhf`;
  - `rpm/`: an RPM repository, and `arkeep.repo` for `dnf`.
  Metadata is served with `Cache-Control: no-cache`, so that clients never see
  an index that lists packages they cannot download yet, or no longer can.
  Package files have versioned names and are cached for a year.
- **Content.** The repository holds the agent packages of the three most
  recent stable releases that have them. Pre-releases are never published to
  it. The server has no package.
- **Built from the releases, from scratch.** `scripts/agent-repository.sh`
  rebuilds the whole repository on every publication: it downloads the
  packages of those releases from GitHub, checks the attestation of their
  `checksums.txt` against the release workflow (ADR 0017) and their checksums
  against it, generates and signs the metadata, then uploads the packages
  before the metadata and deletes what no longer belongs. The bucket holds no
  state of its own: rebuilding it gives the same content.
- **Signing.** A dedicated GPG key, RSA 4096, which RHEL 8 and 9 accept on
  packages, signs every `.rpm` at build time (GoReleaser, nfpm), the APT
  `InRelease` and `Release.gpg`, and the RPM `repomd.xml`. It is used for
  nothing else, expires after three years, and has no passphrase: a
  passphrase stored next to it would protect nothing. Its revocation
  certificate is kept offline by the maintainers. `.deb` files are not signed
  one by one: APT checks them through the signed index.
- **Secrets.** The private key, and an R2 API token limited to Object Read &
  Write on `arkeep-packages`, are secrets of the `release` environment
  (ADR 0017), with the R2 endpoint as a variable of the same environment.
- **Publication.** The release workflow publishes the repository after the
  attestation, on stable releases only. The `Agent repository` workflow,
  started by hand from `main`, rebuilds it with the same script: after a
  failed publication, a key rotation, or to repair the bucket.

## Consequences

- Machines configured once upgrade the agent with their package manager, and
  with the automatic updates of their distribution if they use them.
  Self-update of standalone binaries is left to a separate decision (#450).
- The release workflow signs only what the release workflow built and
  attested: a package replaced on a release page fails the attestation check
  and is not published.
- Whoever holds the private key can publish an agent that every configured
  machine installs as root. It lives only in the `release` environment, which
  only `v*` tags and `main` can deploy to.
- The key expires: before it does, extend its expiry or replace it, publish
  the new public key at `gpg.key`, and announce it. Machines that imported the
  old key must import the new one.
- Packages of releases older than the last three are only on their release
  pages. The repository can be rebuilt at any time from the releases.
- R2, its custom domain and the API token are Cloudflare settings outside the
  repository, set up once by an administrator; the token has an expiry to
  renew. Storage stays far below the R2 free tier (about 300 MB for three
  releases), and R2 charges no egress.
- `.github/workflows/packages.yml` builds signed packages with a throwaway key,
  builds a repository from them, and installs from it with `apt` and `dnf`.

## Alternatives considered

- **GitHub Pages** — free and already next to the releases, but set aside to
  keep distribution off Pages.
- **A hosted package service** (Cloudsmith, packagecloud, Gemfury) — no
  infrastructure, but a third party would hold the signing key, and free plans
  for open source projects can change.
- **A server running aptly or createrepo** — full control, but one more
  machine to maintain and secure, for static files.
- **openSUSE Build Service or a Launchpad PPA** — they rebuild packages from
  source with their own tooling, made for distribution packages rather than a
  Go binary already built and attested by the release workflow.
- **Keeping every version** — the repository and each publication would grow
  with every release, for downgrades that the release pages already serve.

## References

- Issue #472, discussions #450 and #453.
- `.goreleaser.yml` (`nfpms`, `rpm.signature`), `deploy/packaging/agent`.
- `scripts/agent-repository.sh`, `scripts/test-agent-packages.sh`.
- `.github/workflows/release.yml` (steps "Prepare the package signing key" and
  "Publish the agent package repository"), `.github/workflows/agent-repository.yml`,
  `.github/workflows/packages.yml`.
- ADR 0011, ADR 0013, ADR 0017.
