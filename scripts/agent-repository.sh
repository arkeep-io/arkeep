#!/usr/bin/env bash
# agent-repository.sh — builds and publishes the APT and RPM repository of the
# agent packages at https://pkg.arkeep.io (ADR 0020).
#
# Usage:
#   scripts/agent-repository.sh fetch <releases dir>
#   scripts/agent-repository.sh build <releases dir> <repository dir>
#   scripts/agent-repository.sh upload <repository dir>
#
# fetch   Downloads the agent packages and checksums.txt of the three most
#         recent stable releases that have packages, one directory per tag,
#         after checking the attestation of checksums.txt against the release
#         workflow and the packages against it. Needs gh and GH_TOKEN.
# build   Generates the repository tree from those directories, and signs it
#         with the key in ARKEEP_PACKAGES_SIGNING_KEY_FILE (an ASCII-armored
#         private key). Needs gpg, dpkg-deb, apt-ftparchive, createrepo_c.
# upload  Uploads the tree to the R2 bucket: packages first, then the metadata,
#         then deletes what is no longer in the tree. Needs rclone (or
#         RCLONE=<path>) and PACKAGES_R2_ENDPOINT, PACKAGES_R2_ACCESS_KEY_ID,
#         PACKAGES_R2_SECRET_ACCESS_KEY.
set -euo pipefail

repo=arkeep-io/arkeep
workflow=arkeep-io/arkeep/.github/workflows/release.yml
bucket=arkeep-packages
base_url=https://pkg.arkeep.io
keep=3
# Release asset architecture -> Debian and RPM architecture.
archs="amd64 arm64 arm"
deb_arch() { case $1 in arm) echo armhf ;; *) echo "$1" ;; esac; }
rpm_arch() { case $1 in amd64) echo x86_64 ;; arm64) echo aarch64 ;; arm) echo armv7hl ;; esac; }

usage() {
  sed -n '4,8p' "$0" | sed 's/^# //' >&2
  exit 2
}

fetch() {
  local out=$1 tag dir found=0
  mkdir -p "$out"
  # Stable releases only, newest first. Releases older than the packages
  # (up to v0.11.0) have none and are skipped.
  while read -r tag; do
    [ "$found" -lt "$keep" ] || break
    dir="${out}/${tag}"
    mkdir -p "$dir"
    if ! gh release download "$tag" --repo "$repo" --dir "$dir" --clobber \
      --pattern 'arkeep-agent_linux_*.deb' --pattern 'arkeep-agent_linux_*.rpm' \
      --pattern checksums.txt 2>/dev/null || ! ls "$dir"/*.deb >/dev/null 2>&1; then
      echo "${tag}: no agent packages, skipped"
      rm -rf "$dir"
      continue
    fi
    # Only what the release workflow of this repository built gets signed.
    gh attestation verify "${dir}/checksums.txt" --repo "$repo" \
      --signer-workflow "$workflow" >/dev/null
    (
      cd "$dir"
      for f in arkeep-agent_linux_*.deb arkeep-agent_linux_*.rpm; do
        grep -E "^[0-9a-f]{64}  ${f}\$" checksums.txt | sha256sum --check --quiet - ||
          { echo "${tag}: ${f} does not match checksums.txt" >&2; exit 1; }
      done
    )
    echo "${tag}: packages verified"
    found=$((found + 1))
  done < <(gh release list --repo "$repo" --exclude-drafts --exclude-pre-releases \
    --limit 100 --json tagName --jq '.[].tagName' | sort -V -r)
  [ "$found" -gt 0 ] || { echo "no release with agent packages" >&2; exit 1; }
}

build() {
  local releases=$1 out=$2 dir arch version f
  : "${ARKEEP_PACKAGES_SIGNING_KEY_FILE:?ARKEEP_PACKAGES_SIGNING_KEY_FILE is not set}"
  rm -rf "$out"
  mkdir -p "${out}/apt/pool/main/a/arkeep-agent" "${out}/rpm"

  GNUPGHOME=$(mktemp -d)
  export GNUPGHOME
  trap 'rm -rf "$GNUPGHOME"' EXIT
  gpg --batch --quiet --import "$ARKEEP_PACKAGES_SIGNING_KEY_FILE"
  local key
  key=$(gpg --batch --with-colons --list-secret-keys | awk -F: '$1 == "fpr" { print $10; exit }')
  [ -n "$key" ] || { echo "no secret key in ${ARKEEP_PACKAGES_SIGNING_KEY_FILE}" >&2; exit 1; }
  local sign=(gpg --batch --yes --local-user "$key" --digest-algo SHA512)
  gpg --batch --armor --export "$key" >"${out}/gpg.key"

  # Packages, under versioned names: the release assets have none.
  for dir in "$releases"/*/; do
    for arch in $archs; do
      f="${dir}arkeep-agent_linux_${arch}.deb"
      [ -f "$f" ] || continue
      version=$(dpkg-deb --field "$f" Version)
      cp "$f" "${out}/apt/pool/main/a/arkeep-agent/arkeep-agent_${version}_$(deb_arch "$arch").deb"
      f="${dir}arkeep-agent_linux_${arch}.rpm"
      [ -f "$f" ] && cp "$f" "${out}/rpm/arkeep-agent-${version}.$(rpm_arch "$arch").rpm"
    done
  done

  # APT: one suite, stable, with the indexes of each architecture.
  (
    cd "${out}/apt"
    local deb_archs="" a
    for arch in $archs; do
      a=$(deb_arch "$arch")
      deb_archs+="${deb_archs:+ }${a}"
      mkdir -p "dists/stable/main/binary-${a}"
      apt-ftparchive --arch "$a" packages pool/main >"dists/stable/main/binary-${a}/Packages"
      gzip -9 --no-name --keep "dists/stable/main/binary-${a}/Packages"
    done
    apt-ftparchive \
      -o APT::FTPArchive::Release::Origin=Arkeep \
      -o APT::FTPArchive::Release::Label=Arkeep \
      -o APT::FTPArchive::Release::Suite=stable \
      -o APT::FTPArchive::Release::Codename=stable \
      -o APT::FTPArchive::Release::Components=main \
      -o "APT::FTPArchive::Release::Architectures=${deb_archs}" \
      -o "APT::FTPArchive::Release::Description=Arkeep agent packages" \
      release dists/stable >Release
    mv Release dists/stable/Release
    "${sign[@]}" --clearsign --output dists/stable/InRelease dists/stable/Release
    "${sign[@]}" --armor --detach-sign --output dists/stable/Release.gpg dists/stable/Release
  )

  # RPM: gzip metadata, which every dnf and yum still in support reads.
  createrepo_c --quiet --general-compress-type=gz "${out}/rpm"
  "${sign[@]}" --armor --detach-sign --output "${out}/rpm/repodata/repomd.xml.asc" "${out}/rpm/repodata/repomd.xml"
  cat >"${out}/rpm/arkeep.repo" <<EOF
[arkeep]
name=Arkeep
baseurl=${base_url}/rpm
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=${base_url}/gpg.key
EOF
  echo "repository built in ${out}, signed by ${key}"
}

upload() {
  local tree=$1 rclone=${RCLONE:-rclone}
  : "${PACKAGES_R2_ENDPOINT:?}" "${PACKAGES_R2_ACCESS_KEY_ID:?}" "${PACKAGES_R2_SECRET_ACCESS_KEY:?}"
  [ -f "${tree}/apt/dists/stable/InRelease" ] && [ -f "${tree}/rpm/repodata/repomd.xml.asc" ] ||
    { echo "${tree} is not a built repository" >&2; exit 1; }
  # rclone reads the remote from the environment: nothing is written to disk.
  export RCLONE_CONFIG_R2_TYPE=s3 RCLONE_CONFIG_R2_PROVIDER=Cloudflare
  export RCLONE_CONFIG_R2_ENDPOINT=$PACKAGES_R2_ENDPOINT
  export RCLONE_CONFIG_R2_ACCESS_KEY_ID=$PACKAGES_R2_ACCESS_KEY_ID
  export RCLONE_CONFIG_R2_SECRET_ACCESS_KEY=$PACKAGES_R2_SECRET_ACCESS_KEY
  # The token is limited to the bucket: it cannot list or create buckets.
  export RCLONE_CONFIG_R2_NO_CHECK_BUCKET=true
  local packages=(--include '*.deb' --include '*.rpm')
  # Packages have versioned names and never change: long cache.
  "$rclone" copy "$tree" "r2:${bucket}" "${packages[@]}" --checksum \
    --header-upload 'Cache-Control: public, max-age=31536000, immutable'
  # Then the metadata, which now only lists packages already uploaded, then
  # the deletions (rclone deletes after transferring).
  "$rclone" sync "$tree" "r2:${bucket}" --checksum \
    --header-upload 'Cache-Control: no-cache'
  echo "uploaded to ${bucket}"
}

[ "$#" -ge 1 ] || usage
cmd=$1
shift
case "$cmd" in
  fetch) [ "$#" -eq 1 ] || usage; fetch "$1" ;;
  build) [ "$#" -eq 2 ] || usage; build "$1" "$2" ;;
  upload) [ "$#" -eq 1 ] || usage; upload "$1" ;;
  *) usage ;;
esac
