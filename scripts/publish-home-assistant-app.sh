#!/usr/bin/env bash
# Publishes the Home Assistant app repository for a release (ADR 0018).
#
# Usage: scripts/publish-home-assistant-app.sh <tag> [<remote>]
#
# Takes deploy/home-assistant from the current checkout, sets the version of
# each app to <tag>, the tag of the agent image Home Assistant then pulls, and
# commits the result on top of the home-assistant branch of <remote>. That
# branch holds nothing else: it is the repository users add to Home Assistant.
#
# Without <remote> nothing is pushed: the commit is created locally and its ID
# printed, to inspect it with `git show --stat <commit>`.
#
# Run it only once the images of <tag> are published: Home Assistant offers
# the new version to users as soon as the branch moves. Used by
# .github/workflows/release.yml, and by hand from the tag if that step failed.
set -euo pipefail

branch=home-assistant
source_dir=deploy/home-assistant
repository=arkeep-io/arkeep

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <tag> [<remote>]" >&2
  exit 2
fi
tag=$1
remote=${2:-}
# Stable releases only: Home Assistant has no notion of a pre-release.
if [[ ! "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "'${tag}' is not a stable release tag (vX.Y.Z)" >&2
  exit 2
fi

cd "$(git rev-parse --show-toplevel)"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
tree_dir="${work}/tree"
mkdir "$tree_dir"
cp -R "${source_dir}/." "$tree_dir/"

apps=0
for config in "$tree_dir"/*/config.yaml; do
  [ -e "$config" ] || continue
  if [ "$(grep -c '^version:' "$config")" -ne 1 ]; then
    echo "${config#"$tree_dir"/}: expected exactly one top-level version" >&2
    exit 1
  fi
  sed -i -E "s/^version:.*/version: \"${tag}\"/" "$config"
  # Shown by Home Assistant next to the update.
  cat >"$(dirname "$config")/CHANGELOG.md" <<EOF
# Changelog

The app runs the agent of the Arkeep release with the same version.

## ${tag}

Release notes: https://github.com/${repository}/releases/tag/${tag}
EOF
  apps=$((apps + 1))
done
if [ "$apps" -eq 0 ] || [ ! -f "${tree_dir}/repository.yaml" ]; then
  echo "${source_dir} holds no app repository (repository.yaml and <app>/config.yaml)" >&2
  exit 1
fi

# Build the commit without touching the checkout or its index. --force keeps
# files that the .gitignore of this repository would leave out.
export GIT_INDEX_FILE="${work}/index"
git --work-tree="$tree_dir" add --all --force
tree=$(git write-tree)

parent=""
if [ -n "$remote" ] && git ls-remote --exit-code --heads "$remote" "$branch" >/dev/null; then
  git fetch --quiet --no-tags "$remote" "$branch"
  parent=$(git rev-parse FETCH_HEAD)
  if [ "$(git rev-parse "${parent}^{tree}")" = "$tree" ]; then
    echo "${branch} already holds this content (${parent}), nothing to publish"
    exit 0
  fi
fi

sign=()
if [ "$(git config --bool commit.gpgsign || true)" = "true" ]; then
  sign=(-S)
fi
commit=$(git commit-tree "$tree" ${parent:+-p "$parent"} "${sign[@]}" \
  -m "Arkeep ${tag}" \
  -m "Built from ${source_dir} at $(git rev-parse HEAD) by scripts/publish-home-assistant-app.sh.")

if [ -z "$remote" ]; then
  echo "$commit"
  exit 0
fi
git push "$remote" "${commit}:refs/heads/${branch}"
echo "published ${tag} on ${branch} (${commit})"
