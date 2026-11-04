#!/usr/bin/env bash
# Sets the short and full description of a Docker Hub repository.
#
# Usage: scripts/dockerhub-description.sh <namespace/repo> <short description> <markdown file>
#
# Requires DOCKERHUB_USERNAME and DOCKERHUB_TOKEN (a personal access token
# with the Read, Write, Delete scope) in the environment, plus curl and jq.
# Used by .github/workflows/dockerhub.yml. Calls the Docker Hub API directly
# so that the token is never handed to third-party code.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <namespace/repo> <short description> <markdown file>" >&2
  exit 2
fi
repo=$1
short=$2
file=$3

# Docker Hub limits: 100 characters for the short description, 25,000 for the
# full one. Fail here rather than let the API truncate or reject them.
if [ "${#short}" -gt 100 ]; then
  echo "short description of ${repo} is ${#short} characters, the limit is 100" >&2
  exit 1
fi
size=$(wc -c <"$file")
if [ "$size" -gt 25000 ]; then
  echo "${file} is ${size} bytes, Docker Hub accepts at most 25000" >&2
  exit 1
fi

token=$(jq -n --arg id "$DOCKERHUB_USERNAME" --arg secret "$DOCKERHUB_TOKEN" \
    '{identifier: $id, secret: $secret}' \
  | curl -fsS -X POST https://hub.docker.com/v2/auth/token \
      -H "Content-Type: application/json" --data @- \
  | jq -r '.access_token // empty')
if [ -z "$token" ]; then
  echo "Docker Hub login returned no access token" >&2
  exit 1
fi

jq -n --arg short "$short" --rawfile full "$file" \
    '{description: $short, full_description: $full}' \
  | curl -fsS -o /dev/null -X PATCH "https://hub.docker.com/v2/repositories/${repo}/" \
      -H "Authorization: Bearer ${token}" \
      -H "Content-Type: application/json" --data @-

echo "updated ${repo} from ${file}"
