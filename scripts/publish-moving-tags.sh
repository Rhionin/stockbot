#!/usr/bin/env bash
# Point :latest and :master at this commit when it is still the tip of master.
# The immutable sha tags are already in the registry. Compare to the tip:
# a commit that master has moved past is still an ancestor of master, so an
# ancestry check would still move :latest.

set -euo pipefail

# Set by the Actions step that runs this script.
# shellcheck disable=SC2154
sha="${GITHUB_SHA:?}"
# shellcheck disable=SC2154
output="${GITHUB_OUTPUT:?}"
# shellcheck disable=SC2154
summary="${GITHUB_STEP_SUMMARY:?}"

record() {
  printf '%s\n' "$1"
  printf '%s\n' "::notice::$1"
  printf '%s\n' "$1" >> "$summary"
}

tip="$(git ls-remote origin refs/heads/master | awk 'NR == 1 { print $1 }')"
if [ -z "${tip}" ]; then
  echo "Could not read refs/heads/master from origin." >&2
  exit 1
fi

if [ "${tip}" != "${sha}" ]; then
  echo "publish=false" >> "$output"
  record "${sha} is not the tip of master (${tip}). Pushed the immutable tag only; leaving :latest and :master unchanged and skipping the deploy hook. If ${tip}'s run did not publish, re-run it."
  exit 0
fi

# Sidecar first. A failure on the pantry move then leaves pantry on the
# previous tip while the sidecar has already moved. The step still fails;
# the summary has to name that split, because nothing else does.
sidecar_moved=0
on_publish_error() {
  local code=$?
  local msg
  trap - ERR
  if [ "$sidecar_moved" -eq 1 ]; then
    msg="Moved pantry-grocy-sync :latest and :master to ${sha}, then failed to move pantry :latest and :master. The two images are on different versions."
    printf '%s\n' "$msg" >> "$summary"
    printf '%s\n' "$msg" >&2
  fi
  exit "$code"
}
trap on_publish_error ERR
docker buildx imagetools create \
  --tag "ghcr.io/rhionin/pantry-grocy-sync:latest" \
  --tag "ghcr.io/rhionin/pantry-grocy-sync:master" \
  "ghcr.io/rhionin/pantry-grocy-sync:${sha}"
sidecar_moved=1
docker buildx imagetools create \
  --tag "ghcr.io/rhionin/pantry:latest" \
  --tag "ghcr.io/rhionin/pantry:master" \
  "ghcr.io/rhionin/pantry:${sha}"
trap - ERR

echo "publish=true" >> "$output"
record "Moved :latest and :master to ${sha}, the tip of master."
