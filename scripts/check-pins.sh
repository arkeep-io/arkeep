#!/usr/bin/env bash
# check-pins.sh — fail when a dependency reference violates ADR 0013
# (docs/adr/0013-pin-and-verify-every-dependency.md).
#
# Checks what Arkeep consumes: GitHub Actions, container images, CI toolchains,
# tool versions, npm manifests and Helm chart dependencies. Exceptions are
# listed in the ADR; a line can opt out with a trailing "# pin-exempt: <reason>"
# comment, which is then reviewed like code.
#
# Usage: scripts/check-pins.sh   (from anywhere in the repository)
set -uo pipefail

cd "$(git rev-parse --show-toplevel)"
failed=0

fail() {
  echo "::error file=$1::$2"
  failed=1
}

# grep_violations <message> <ERE> <files...> — reports each matching line
# that is not a comment and has no pin-exempt marker.
grep_violations() {
  local msg=$1 re=$2; shift 2
  local hit
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    local file=${hit%%:*} rest=${hit#*:}
    local line=${rest%%:*} text=${rest#*:}
    [[ "$text" =~ ^[[:space:]]*# ]] && continue
    [[ "$text" == *"pin-exempt:"* ]] && continue
    fail "$file,line=$line" "$msg: $(echo "$text" | sed 's/^[[:space:]]*//')"
  done < <(grep -nHE "$re" "$@" 2>/dev/null)
}

workflows=(.github/workflows/*.yml)
dockerfiles=(server/Dockerfile* agent/Dockerfile*)
composes=(deploy/docker/docker-compose*.yml)

# 1. Actions by full commit SHA.
grep_violations "action not pinned to a commit SHA" \
  'uses:[[:space:]]*[^.[:space:]][^@[:space:]]*@([^0-9a-f]|[0-9a-f]{0,39}([^0-9a-f]|$))' "${workflows[@]}"

# 2. No floating tool, toolchain or image versions.
grep_violations "floating 'latest' reference" \
  '(@latest|version:[[:space:]]*"?latest|:latest([[:space:]"@]|$))' \
  "${workflows[@]}" "${dockerfiles[@]}" "${composes[@]}" Taskfile.yml deploy/helm/arkeep/values.yaml
grep_violations "toolchain given by major/minor version: use go-version-file / node-version-file" \
  '(go|node)-version:' "${workflows[@]}"

# 3. Base images: tag and digest. A FROM naming an earlier build stage is fine.
for file in "${dockerfiles[@]}"; do
  stages=" "
  while IFS= read -r hit; do
    line=${hit%%:*}; text=${hit#*:}
    read -r _ ref _ alias <<<"$text"
    if [[ "$ref" != \$* && "$stages" != *" $ref "* && "$ref" != *"@sha256:"* ]]; then
      fail "$file,line=$line" "base image without @sha256 digest: $ref"
    fi
    [[ -n "${alias:-}" ]] && stages+="$alias "
  done < <(grep -nE '^FROM[[:space:]]' "$file")
done

# 4. Compose images: third-party images by digest, Arkeep images by release tag.
while IFS= read -r hit; do
  file=${hit%%:*}; rest=${hit#*:}; line=${rest%%:*}; text=${rest#*:}
  ref=$(sed -E 's/^[[:space:]#]*image:[[:space:]]*//; s/[[:space:]]+#.*$//; s/"//g' <<<"$text")
  if [[ "$ref" == ghcr.io/arkeep-io/* ]]; then
    [[ "$ref" =~ :v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "$file,line=$line" "Arkeep image must use a release tag (:vX.Y.Z): $ref"
  else
    [[ "$ref" == *"@sha256:"* ]] || fail "$file,line=$line" "image without @sha256 digest: $ref"
  fi
done < <(grep -nHE '^[[:space:]#]*image:' "${composes[@]}")

# 5. npm manifests: exact versions only.
python3 - gui/package.json <<'EOF' || failed=1
import json, re, sys
path = sys.argv[1]
pkg = json.load(open(path))
bad = False
for section in ("dependencies", "devDependencies", "optionalDependencies", "peerDependencies"):
    for name, spec in pkg.get(section, {}).items():
        if not re.fullmatch(r"\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?", spec):
            print(f"::error file={path}::{section}.{name} is not an exact version: {spec}")
            bad = True
if not re.fullmatch(r"pnpm@\d+\.\d+\.\d+\+sha512\.[0-9a-f]{128}", pkg.get("packageManager", "")):
    print(f"::error file={path}::packageManager must be pnpm@X.Y.Z+sha512.<hash>")
    bad = True
sys.exit(1 if bad else 0)
EOF

# 6. Helm chart dependencies: exact versions.
grep_violations "Helm dependency version is a range" \
  '^[[:space:]]+version:[[:space:]]*"?[^0-9"[:space:]]|^[[:space:]]+version:[[:space:]]*"?[0-9]+(\.[0-9x*]+)?(\.[x*])' \
  deploy/helm/arkeep/Chart.yaml

if (( failed )); then
  echo "Dependency pinning check failed — see docs/adr/0013-pin-and-verify-every-dependency.md" >&2
  exit 1
fi
echo "Dependency pinning check passed."
