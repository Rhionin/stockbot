#!/usr/bin/env bash
# Pull and recreate the Stockbot stack.
#
# When PUBLIC_HOST is set and auth.caddy exists, the public HTTPS proxy
# (compose profile "public") is part of the same update. A plain
# `docker compose up` omits profiled services, which would leave certificate
# renewal and proxy config out of automatic updates. A LAN-only install
# leaves PUBLIC_HOST empty, so Caddy is never started. Without auth.caddy,
# starting the profile would publish the hostname with no password file.
# The hash is copied into auth/household before containers are recreated,
# and an existing PANTRY_SESSION_SECRET is kept.
#
# A non-empty CLOUDFLARE_TUNNEL_TOKEN selects the tunnel profile instead.
# That profile does not publish ports 80 or 443. The certificate profile
# is stopped so a token cannot leave both paths running.
#
# The timer and the deploy-hook path unit both start this script. A lock
# keeps those from pulling at the same time. If a hook lands while a pull
# is already running, the running copy sees the new trigger file and pulls
# again instead of dropping it.
#
# A pull that does not change the image id of a running service stops there.
# The timer fires every minute, and recreating a healthy stack each time
# would drop the scanner session for nothing.
#
# When an image does change, the new pantry container is kept only if
# GET /health is ok and GET /api/build's commit equals the image label
# org.opencontainers.image.revision (see the Dockerfile). Both are read on
# 127.0.0.1:$HOST_PORT. The public hostname asks for the household password
# on /health, so a check there would look like a failed deploy.
#
# On timeout or a mismatch, each service that had a running image is
# re-pinned to that digest and started with --pull never. The local tag
# compose uses (ghcr.io/rhionin/pantry:$PANTRY_IMAGE_TAG, and the same for
# any other service whose digest changed) is moved back to that previous
# digest. A later manual `docker compose up -d` or setup.sh would otherwise
# start the rejected image with no health check. .env is not rewritten.
# PANTRY_IMAGE_TAG and the setup last-known-good sha (#152) stay whatever
# they already were. The rejected digest is recorded so the next minute
# does not start that same image again. Comparison happens after pull, so
# the restored local tag is not treated as the remote image: a new digest
# is still deployed, and the rejected one is still skipped.
#
# Re-pinning the image does not undo a migration. The migration runner is
# not transactional (#249). This script does not copy pantry-pre-*.db back
# over the database.

set -euo pipefail

PANTRY_DIR=${PANTRY_DIR:-/opt/pantry}
cd "$PANTRY_DIR"
# shellcheck disable=SC1091
source "$PANTRY_DIR/publish-mode.sh"

compose=(docker compose --project-directory "$PANTRY_DIR" -f "$PANTRY_DIR/docker-compose.yml")
mode_rc=0
mode=$(publish_mode_from_env) || mode_rc=$?
if [[ "$mode_rc" -eq 2 ]]; then
  echo "Tunnel mode needs CLOUDFLARE_TUNNEL_TOKEN in /opt/pantry/.env. The public proxy was left unchanged. Run: sudo ./setup.sh publish --tunnel" >&2
  exit 1
elif [[ "$mode_rc" -ne 0 ]]; then
  echo "PUBLISH_MODE must be acme or tunnel. The public proxy was left unchanged." >&2
  exit 1
fi

if [[ "$mode" == tunnel ]]; then
  if [[ ! -f "$PANTRY_DIR/auth.caddy" ]]; then
    echo "CLOUDFLARE_TUNNEL_TOKEN is set but ${PANTRY_DIR}/auth.caddy is missing; leaving the tunnel stopped. Run: sudo ./setup.sh publish --tunnel" >&2
  else
    compose+=(-f "$PANTRY_DIR/docker-compose.tunnel.yml" --profile tunnel)
  fi
elif [[ "$mode" == acme ]]; then
  # -f /opt/pantry/auth.caddy is the production gate. A fixture sets
  # PANTRY_DIR somewhere else and is checked only then.
  if [[ -f /opt/pantry/auth.caddy || ( "$PANTRY_DIR" != /opt/pantry && -f "$PANTRY_DIR/auth.caddy" ) ]]; then
    compose+=(--profile public)
  else
    echo "PUBLIC_HOST is set but ${PANTRY_DIR}/auth.caddy is missing; leaving the public proxy stopped. Run: sudo ./setup.sh" >&2
  fi
fi
while IFS= read -r arg; do
  [[ -n "$arg" ]] && compose+=("$arg")
done < <(grocy_compose_suffix)

# uid 65532 is the distroless nonroot user that writes the trigger file.
mkdir -p "$PANTRY_DIR/deploy-trigger"
if ! chown 65532:65532 "$PANTRY_DIR/deploy-trigger"; then
  # The no-sudo test seam. A real timer runs this file as root and can chown.
  if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
    echo "[warn] Could not give ${PANTRY_DIR}/deploy-trigger to uid 65532" >&2
  else
    echo "Could not give ${PANTRY_DIR}/deploy-trigger to uid 65532" >&2
    exit 1
  fi
fi
chmod 755 "$PANTRY_DIR/deploy-trigger"

exec 9>"$PANTRY_DIR/deploy-trigger/.lock"
if ! flock -n 9; then
  echo "pantry update: another update is running; waiting so this trigger is not dropped"
  flock 9
fi
echo "pantry update: lock acquired"

# shellcheck disable=SC1091
source "$PANTRY_DIR/auth-migrate.sh"
if ! sync_household_credential; then
  echo "Could not copy the household password hash; leaving containers unchanged." >&2
  exit 1
fi
if [[ -f "$PANTRY_DIR/auth.caddy" ]]; then
  if ! ensure_session_secret; then
    echo "Could not save the session secret; leaving containers unchanged." >&2
    exit 1
  fi
  if [[ ! -s "$PANTRY_DIR/auth/household" ]]; then
    echo "auth.caddy is present but the login hash was not copied; leaving containers unchanged." >&2
    exit 1
  fi
fi
# shellcheck disable=SC1091
source "$PANTRY_DIR/vapid-env.sh"
if ! ensure_vapid_keys; then
  echo "Could not generate VAPID keys; scan alerts stay on an open tab until sudo ./setup.sh succeeds." >&2
fi

trigger_digest() {
  if [[ -f "$PANTRY_DIR/deploy-trigger/request" ]]; then
    sha256sum "$PANTRY_DIR/deploy-trigger/request" | awk '{print $1}'
  fi
}

# compose_service_images prints "service<TAB>image" for the compose command
# this update will actually run (profiles included).
compose_service_images() {
  local json
  if ! command -v python3 >/dev/null 2>&1; then
    echo "pantry update: python3 is required to read the compose service images" >&2
    return 1
  fi
  json=$("${compose[@]}" config --format json) || return 1
  printf '%s' "$json" | python3 -c '
import json, sys
doc = json.load(sys.stdin)
services = doc.get("services") or {}
for name, svc in services.items():
    image = ""
    if isinstance(svc, dict):
        image = svc.get("image") or ""
    sys.stdout.write("%s\t%s\n" % (name, image))
'
}

image_repo_name() {
  local ref="$1" tail
  if [[ "$ref" == *@sha256:* ]]; then
    printf '%s' "${ref%%@*}"
    return 0
  fi
  tail=${ref##*/}
  if [[ "$tail" == *:* ]]; then
    printf '%s' "${ref%:*}"
    return 0
  fi
  printf '%s' "$ref"
}

# image_repo_digest IMAGE_ID REF prints the registry digest of IMAGE_ID,
# preferring one for REF's repository. Empty when the image has none.
image_repo_digest() {
  local image_id="$1" ref="$2" repo digests digest match
  [[ -n "$image_id" ]] || return 0
  repo=$(image_repo_name "$ref")
  digests=$(docker image inspect -f '{{range .RepoDigests}}{{println .}}{{end}}' "$image_id" 2>/dev/null || true)
  match=""
  while IFS= read -r digest; do
    digest=${digest//$'\r'/}
    [[ -z "$digest" || "$digest" == "<no value>" ]] && continue
    if [[ "$digest" == "$repo@"* ]]; then
      match=$digest
      break
    fi
    if [[ -z "$match" ]]; then
      match=$digest
    fi
  done <<< "$digests"
  printf '%s' "$match"
}

one_line() {
  local value="$1"
  value=${value//$'\r'/}
  value=${value//$'\n'/}
  printf '%s' "$value"
}

# running_image_id SERVICE prints the image id of the running container,
# or of a stopped one when nothing is running, so a later failure can
# re-pin the image that was here before the pull.
running_image_id() {
  local svc="$1" cids cid line fallback
  cids=$("${compose[@]}" ps -aq "$svc" 2>/dev/null || true)
  fallback=""
  while IFS= read -r cid; do
    cid=${cid//$'\r'/}
    [[ -z "$cid" ]] && continue
    line=$(docker inspect -f '{{.State.Running}} {{.Image}}' "$cid" 2>/dev/null || true)
    line=$(one_line "$line")
    if [[ "$line" == true\ * ]]; then
      printf '%s' "${line#true }"
      return 0
    fi
    if [[ -z "$fallback" && "$line" == false\ * ]]; then
      fallback=${line#false }
    fi
  done <<< "$cids"
  printf '%s' "$fallback"
}

record_running_images() {
  local state="$1" svc image running_id digest
  compose_service_images > "$state/refs.tsv" || return 1
  if [[ ! -s "$state/refs.tsv" ]]; then
    echo "pantry update: compose returned no services" >&2
    return 1
  fi
  : > "$state/running.tsv"
  while IFS=$'\t' read -r svc image; do
    [[ -z "$svc" ]] && continue
    running_id=$(running_image_id "$svc")
    digest=""
    if [[ -n "$running_id" ]]; then
      digest=$(image_repo_digest "$running_id" "$image")
    fi
    printf '%s\t%s\t%s\n' "$svc" "$running_id" "$digest" >> "$state/running.tsv"
    if [[ -n "$digest" ]]; then
      echo "pantry update: running ${svc} image ${digest}"
    elif [[ -n "$running_id" ]]; then
      echo "pantry update: running ${svc} image ${running_id}"
    else
      echo "pantry update: ${svc} is not running"
    fi
  done < "$state/refs.tsv"
}

images_unchanged() {
  local state="$1" svc running_id digest ref pulled_id any=0
  while IFS=$'\t' read -r svc running_id digest; do
    [[ -z "$svc" ]] && continue
    any=1
    [[ -n "$running_id" ]] || return 1
    ref=$(awk -F '\t' -v s="$svc" '$1 == s { print $2; exit }' "$state/refs.tsv")
    [[ -n "$ref" ]] || return 1
    pulled_id=$(docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null || true)
    pulled_id=$(one_line "$pulled_id")
    [[ -n "$pulled_id" && "$pulled_id" == "$running_id" ]] || return 1
  done < "$state/running.tsv"
  [[ "$any" -eq 1 ]]
}

pantry_desired_digest() {
  local state="$1" ref digest
  ref=$(awk -F '\t' '$1 == "pantry" { print $2; exit }' "$state/refs.tsv")
  [[ -n "$ref" ]] || return 0
  digest=$(image_repo_digest "$ref" "$ref")
  if [[ -z "$digest" ]]; then
    digest=$(docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null || true)
    digest=$(one_line "$digest")
  fi
  printf '%s' "$digest"
}

digest_was_rejected() {
  local digest="$1" file="$PANTRY_DIR/.update-rejected-digest" line
  [[ -n "$digest" && -f "$file" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" == "$digest" ]] && return 0
  done < "$file"
  return 1
}

remember_rejected_digest() {
  local digest="$1" file="$PANTRY_DIR/.update-rejected-digest"
  [[ -n "$digest" ]] || return 0
  if digest_was_rejected "$digest"; then
    return 0
  fi
  if [[ ! -f "$file" ]]; then
    printf '%s\n' '# Image digests rolled back by pantry-update.sh. Not a setup last-known-good sha.' > "$file" || return 1
  fi
  printf '%s\n' "$digest" >> "$file"
}

log_already_rejected() {
  cat >&2 <<EOF
================================================================
PANTRY UPDATE FAILED: image ${1} was already rolled back; leaving the previous image running.
This script will not start that digest again. A newer image with a different digest will still be tried.
Delete that line from ${PANTRY_DIR}/.update-rejected-digest only when you mean to retry it.
A database migration that already ran is still applied. This script does not restore pantry-pre-*.db snapshots in /data.
================================================================
EOF
}

log_rollback_banner() {
  cat >&2 <<'EOF'
================================================================
PANTRY UPDATE FAILED: rolling back to the previous image.
This rollback only restores container images. It does not undo a database migration.
The migration runner is not transactional: a migration that already ran stays applied, and starting the previous image cannot undo it.
Automatic pre-migration snapshots, when one was taken, are files named pantry-pre-*.db in /data (the pantry-data volume).
Restoring one of those over /data/pantry.db is a manual step. This script does not restore the database.
================================================================
EOF
}

json_string_field() {
  local body="$1" field="$2" pattern
  pattern='"'"${field}"'"[[:space:]]*:[[:space:]]*"([^"]*)"'
  if [[ "$body" =~ $pattern ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
  fi
}

health_is_ok() {
  [[ "$1" =~ \"status\"[[:space:]]*:[[:space:]]*\"ok\" ]]
}

wait_for_expected_build() {
  local state="$1" ref revision host_port timeout interval deadline health build commit
  ref=$(awk -F '\t' '$1 == "pantry" { print $2; exit }' "$state/refs.tsv")
  if [[ -z "$ref" ]]; then
    echo "pantry update: compose project has no pantry service" >&2
    return 1
  fi
  revision=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$ref" 2>/dev/null || true)
  revision=$(one_line "$revision")
  if [[ -z "$revision" || "$revision" == "<no value>" ]]; then
    echo "pantry update: ${ref} has no org.opencontainers.image.revision label" >&2
    return 1
  fi
  host_port=$(publish_env_value HOST_PORT)
  host_port=${host_port:-8080}
  timeout=${PANTRY_UPDATE_HEALTH_TIMEOUT:-90}
  interval=${PANTRY_UPDATE_HEALTH_INTERVAL:-2}
  echo "pantry update: waiting for /health and /api/build commit ${revision} on 127.0.0.1:${host_port}"
  health=""
  build=""
  deadline=$((SECONDS + timeout))
  while true; do
    health=$(curl -sf --max-time 5 "http://127.0.0.1:${host_port}/health" 2>/dev/null || true)
    build=$(curl -sf --max-time 5 "http://127.0.0.1:${host_port}/api/build" 2>/dev/null || true)
    commit=$(json_string_field "$build" commit)
    if health_is_ok "$health" && [[ -n "$commit" && "$commit" == "$revision" ]]; then
      echo "pantry update: healthy, running commit ${commit}"
      return 0
    fi
    if [[ "$timeout" -le 0 || "$SECONDS" -ge "$deadline" ]]; then
      break
    fi
    sleep "$interval"
  done
  echo "pantry update: health check timed out after ${timeout}s (wanted commit ${revision})" >&2
  echo "pantry update: last /health: ${health:-<none>}" >&2
  echo "pantry update: last /api/build: ${build:-<none>}" >&2
  return 1
}

# restore_local_tags moves each compose ref whose pulled digest differs
# from the image recorded before pull back onto that previous digest.
# Pull points the local tag at the remote image. Doing this after the
# remote digest has been inspected keeps the next run honest: a new
# remote digest still looks new, and the rejected digest still does not
# look like the image that is running.
restore_local_tags() {
  local state="$1" svc running_id good pin ref remote remote_id
  while IFS=$'\t' read -r svc running_id good; do
    [[ -z "$svc" ]] && continue
    ref=$(awk -F '\t' -v s="$svc" '$1 == s { print $2; exit }' "$state/refs.tsv")
    [[ -z "$ref" || "$ref" == *@sha256:* ]] && continue
    pin=$good
    [[ -z "$pin" ]] && pin=$running_id
    [[ -z "$pin" ]] && continue
    remote=$(image_repo_digest "$ref" "$ref")
    remote_id=$(docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null || true)
    remote_id=$(one_line "$remote_id")
    if [[ -n "$good" && -n "$remote" && "$remote" == "$good" ]]; then
      continue
    fi
    if [[ -n "$running_id" && -n "$remote_id" && "$remote_id" == "$running_id" ]]; then
      continue
    fi
    if ! docker tag "$pin" "$ref"; then
      echo "pantry update: could not retag ${ref} to ${pin}" >&2
      return 1
    fi
    echo "pantry update: retagged ${ref} to ${pin}" >&2
  done < "$state/running.tsv"
}

# rollback_images re-pins every service that had an image before the pull.
# The pin is a compose override so .env and PANTRY_IMAGE_TAG stay put.
rollback_images() {
  local state="$1" override svc running_id digest pin any=0
  override=$(mktemp)
  printf 'services:\n' > "$override"
  while IFS=$'\t' read -r svc running_id digest; do
    [[ -z "$svc" ]] && continue
    pin=$digest
    [[ -z "$pin" ]] && pin=$running_id
    [[ -z "$pin" ]] && continue
    any=1
    printf '  %s:\n    image: %s\n' "$svc" "$pin" >> "$override"
    echo "pantry update: re-pinning ${svc} to ${pin}" >&2
  done < "$state/running.tsv"
  if [[ "$any" -ne 1 ]]; then
    cat >&2 <<'EOF'
================================================================
PANTRY UPDATE FAILED: no previous image digest was recorded, so the new image was left in place.
This script does not undo a database migration. The migration runner is not transactional.
Automatic pre-migration snapshots, when one was taken, are files named pantry-pre-*.db in /data (the pantry-data volume).
Restoring one of those over /data/pantry.db is a manual step. This script does not restore the database.
================================================================
EOF
    rm -f "$override"
    return 1
  fi
  log_rollback_banner
  if ! "${compose[@]}" -f "$override" up -d --pull never; then
    echo "PANTRY UPDATE FAILED: could not start the previous image." >&2
    rm -f "$override"
    return 1
  fi
  rm -f "$override"
}

apply_compose() {
  local state desired up_ok
  state=$(mktemp -d)
  if ! record_running_images "$state"; then
    rm -rf "$state"
    return 1
  fi

  "${compose[@]}" pull

  if images_unchanged "$state"; then
    echo "pantry update: images unchanged"
    rm -rf "$state"
    return 0
  fi

  desired=$(pantry_desired_digest "$state")
  if digest_was_rejected "$desired"; then
    # Pull moved the local tag onto this digest. Point it back at the
    # running image so the skip does not leave :latest on the bad image.
    if ! restore_local_tags "$state"; then
      echo "pantry update: could not point the local image tag back at the previous digest." >&2
    fi
    log_already_rejected "$desired"
    rm -rf "$state"
    exit 1
  fi

  # After a successful pull, drop a container left from the other mode so
  # the name pantry-caddy can be recreated on this path. A tunnel token with
  # no password file leaves whatever is already running alone.
  if [[ "$mode" != tunnel || -f "$PANTRY_DIR/auth.caddy" ]]; then
    drop_other_publish_containers "$mode"
  fi
  if [[ "$mode" == tunnel && -f "$PANTRY_DIR/auth.caddy" ]]; then
    drop_stale_cloudflared
  fi
  up_ok=0
  if "${compose[@]}" up -d; then
    up_ok=1
  fi
  if [[ "$up_ok" -eq 1 ]] && wait_for_expected_build "$state"; then
    rm -rf "$state"
    return 0
  fi
  if [[ "$up_ok" -ne 1 ]]; then
    echo "pantry update: docker compose up failed" >&2
  fi
  if rollback_images "$state"; then
    if ! restore_local_tags "$state"; then
      echo "pantry update: the previous container is running, but the local tag still names the rejected image. A manual docker compose up would start it." >&2
    fi
    if ! remember_rejected_digest "$desired"; then
      echo "pantry update: could not record the rejected digest; the next run may try this image again" >&2
    fi
  else
    echo "pantry update: previous image did not start; the next run will try this image again" >&2
  fi
  rm -rf "$state"
  exit 1
}

round=0
while true; do
  round=$((round + 1))
  if [[ "$round" -gt 3 ]]; then
    echo "pantry update: stopping after 3 pulls; the timer will retry"
    exit 0
  fi
  before=$(trigger_digest || true)
  if [[ -n "$before" ]]; then
    echo "pantry update: deploy trigger $(tr '\n' ' ' < "$PANTRY_DIR/deploy-trigger/request")"
  else
    echo "pantry update: scheduled pull"
  fi
  apply_compose
  after=$(trigger_digest || true)
  if [[ "$after" == "$before" ]]; then
    echo "pantry update: finished"
    break
  fi
  echo "pantry update: trigger changed during the pull; pulling again"
done
