#!/usr/bin/env bash
# Pull and recreate the Stockbot stack.
#
# When PUBLIC_HOST is set, the public HTTPS proxy (compose profile "public")
# or tunnel profile is part of the same update. A plain `docker compose up`
# omits profiled services, which would leave certificate renewal and proxy
# config out of automatic updates. A LAN-only install leaves PUBLIC_HOST empty,
# so Caddy is never started.
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
# would drop the scanner session for nothing. A minute that does not change
# the pantry image does not take a backup.
#
# When the pantry image digest is about to change, pantry-backup.sh
# pre-deploy runs before anything is recreated. BACKUP_BEFORE_DEPLOY is
# required (the default: a failed backup leaves containers unchanged and
# this script exits non-zero), warn or optional (a failed backup is a journal
# warning and the deploy continues), or off. The value may be set in the
# environment or in .env. A blocked backup is also recorded in
# last-deploy-status under PANTRY_DIR.
# This script already holds the deploy lock, so the backup is started with
# PANTRY_BACKUP_LOCK_HELD=1 and does not flock that file again.
#
# When an image does change, the pantry revision label is read before
# anything is recreated. An image without org.opencontainers.image.revision
# is not started. The new containers are kept only if GET /health is ok and
# GET /api/build's commit equals that label. While /health reports
# "migrating", the wait continues up to PANTRY_UPDATE_MIGRATION_TIMEOUT
# (default 10 minutes). The short timeout (PANTRY_UPDATE_HEALTH_TIMEOUT,
# default 90 seconds) uses /proc/uptime, because the Pi has no RTC. Both
# URLs are read on 127.0.0.1:$HOST_PORT. /health on the public hostname asks
# for the household password, so that URL is not the LAN check. When the
# public proxy is part of this update, /api/build is also requested through
# Caddy.
#
# .update-pending is written before containers are recreated. If this process
# stops after that and before a verdict, the next run checks health again
# against the revision label of the digest named in that marker, then checks
# every container. It rolls back if either check fails. A newer image pulled
# in the meantime is not the commit that check expects. If the pending digest
# is no longer on this machine, the resume check is skipped with a warning.
#
# Before containers are recreated, each service's status, RestartCount,
# container id, and StartedAt are recorded. Afterwards only a service
# whose image changed can fail the deploy. `docker compose up` failing is
# not itself a rollback: if every changed service is healthy, the new
# images stay. On that path a changed container still running the previous
# image is not healthy. An unchanged service that is down, including the
# proxy, logs a journal warning and does not roll back. The service is
# listed in .update-sidecar-warn. The next run checks that list even when
# the images did not change. A down sidecar warns again until it is
# running. A proxy stays on the list until a later probe passes, even
# while its container is running: "running" is not the same as answering.
# That later probe is the same check a deploy makes, against the pantry
# image already recorded for this run, so a healthy tunnel can match its
# commit on a minute when no image changed. A deploy removes a non-proxy
# line once that container is running, and removes a proxy line only
# after the probe passes. A line whose service
# is not in this compose config is dropped, so turning a profile off does
# not warn forever. Status and RestartCount are read again after
# PANTRY_UPDATE_SETTLE_SECONDS (default 12, never more than 15). A restart
# in that window fails the deploy only for a changed image. An unchanged
# cloudflared that restarts on its own does not.
#
# On timeout, a compose failure, or a commit mismatch, each service that
# was running is re-pinned to its previous digest and started with --pull
# never. That rollback is health-checked. If the previous image starts but
# does not become healthy, the log says BOTH IMAGES DOWN. If no previous
# digest was recorded, the new image is left in place and that banner is
# not printed. The local tag compose uses
# (ghcr.io/rhionin/pantry:$PANTRY_IMAGE_TAG, and the same for any other
# service whose digest changed) is moved back to the previous digest. A
# later manual compose up or setup.sh would otherwise start the
# rejected image with no health check. .env is not rewritten.
# PANTRY_IMAGE_TAG and the setup last-known-good sha (#152) stay whatever
# they already were. A failure pinned on one changed service rejects that
# digest. A pantry digest that already passed /health and /api/build is
# not rejected when a sidecar is the one that failed. Every changed digest
# is rejected when pantry itself is unverified. An unchanged service that
# is merely down is not that named failure.
# Lines are "service<TAB>digest" in .update-rejected-digest. The next run
# retags each rejected service back to its previous digest. The first skip
# of a digest is a journal warning; later minutes log one skip line and do
# not warn again. A warning mark is dropped when that digest is no longer
# rejected. Other services are still deployed. Comparison happens
# after pull, so the restored local tag is not treated as the remote image.
#
# Tunnel mode checks caddy-tunnel before recreate, then again after.
# Both requests are `curl --resolve $PUBLIC_HOST:80:10.77.77.3
# http://$PUBLIC_HOST/api/build`. PUBLIC_HOST is the hostname compose
# passes in, and the only site Caddyfile.tunnel serves. The address
# 10.77.77.3 is caddy-tunnel on the cf-tunnel network. The second check
# must return the new pantry commit. The old stack counts as passed only
# when that response is 2xx and its commit is the running pantry revision.
# A 502, an empty 200, or the wrong commit does not. The probe is skipped
# only when the pre-deploy request was also unreachable (curl 6, 7, 28, or
# 52). A refusal or a hang after a reachable old stack fails the deploy.
# Curl 56 on the old stack is not "unreachable": a healthy new listener
# still passes. A failed probe is tried once more after
# PANTRY_UPDATE_PROBE_RETRY_SECONDS (default 3, never more than 15).
# Certificate mode makes the same before-and-after request over HTTPS. An
# unchanged proxy that passed before and fails after is a pantry failure
# only when that same proxy container is still running, with the same id,
# restart count, and StartedAt. Compose before 2.35 can restart Caddy
# without changing the id or the restart count, so a new StartedAt is the
# proxy's failure. If the proxy container is down, exited, restarting, or
# replaced, the script warns and does not reject pantry. An unchanged
# proxy that was already failing only warns, and stays in
# .update-sidecar-warn until a later probe passes. A changed proxy that
# fails rejects that proxy, not a pantry image that already passed
# /health. The service name is caddy-tunnel in tunnel mode and caddy
# otherwise. Cloudflare still uses http://caddy:80 inside that network.
#
# Re-pinning the image does not undo a migration. The migration runner is
# not transactional (#249). This script does not copy pantry-pre-*.db back
# over the database. Logs from the rejected pantry container are saved in
# the pantry directory before the rollback removes that container. The
# banner names a pantry-pre-*.db path only when those logs say this deploy
# wrote the snapshot. An "already exists" line is not a new snapshot.
#
# After a successful deploy, host scripts under PANTRY_DIR (and installed
# systemd units when they are already present) are refreshed from a copy
# shipped inside the pantry image at /usr/share/stockbot/host-bundle. The
# running pantry-update.sh is not overwritten in place; the new script
# takes effect on the next timer or path run. Rollback does not install
# those files. setup.sh, .env, secrets, and data are never touched.
#
# Service images come from `docker compose config`. python3 is not required.

set -euo pipefail

UPDATE_STATE=""
# PANTRY_VERIFIED is 1 after the new pantry image has passed /health and
# /api/build. FAILED_SERVICES lists services that failed after that, one
# name per line. A verified pantry digest is rejected only if it is in
# that list.
PANTRY_VERIFIED=0
FAILED_SERVICES=""
# TUNNEL_REACHABLE is 1 when the pre-deploy probe was not a connect failure.
# PROXY_REACHABLE is 1 when that probe actually succeeded (curl 0). An
# unchanged proxy that passed and then fails is charged to pantry.
TUNNEL_REACHABLE=0
PROXY_REACHABLE=0
# PROXY_PROBE_PASSED is 1 only when a proxy probe got a successful answer,
# not when the probe was skipped. A marker line stays until that happens.
PROXY_PROBE_PASSED=0
TUNNEL_PROBE_RC=0
cleanup_update_state() {
  if [[ -n "${UPDATE_STATE:-}" ]]; then
    rm -rf "$UPDATE_STATE"
  fi
}
trap cleanup_update_state EXIT

PANTRY_DIR=${PANTRY_DIR:-/opt/pantry}
mode=""
compose=()
HOST_STATE_INITIALIZED=0
HOST_STATE_CORRUPT=0
HOST_STATE_BLOCKED=0
HOST_FLAG_HOST_REFRESH=allow
HOST_FLAG_AUTO_SETUP=off
HOST_FLAG_ANY_INVALID=0
HOST_RECOVERY_EXIT=0
HOST_TXN_FATAL_RC=3
HOST_TXN_BUDGET_CMD=5
HOST_TXN_BUDGET_VERIFY=15
HOST_TXN_BUDGET_CURL=3
HOST_TXN_BUDGET_HEALTH=15
HOST_TXN_BUDGET_TXN=120
HOST_TXN_BUDGET_RESTORE=60
HOST_TXN_KILL_GRACE=2

# prepare_setup_dir in pantry_update_main always runs before apply_compose recreates containers.

# public_proxy_in_update is true when this run will manage the HTTPS proxy
# (certificate or tunnel profile). LAN-only installs skip the session secret.
public_proxy_in_update() {
  if [[ "$mode" == tunnel ]]; then
    [[ -f "$PANTRY_DIR/docker-compose.tunnel.yml" && -f "$PANTRY_DIR/Caddyfile.tunnel" ]] \
      && [[ -n "$(publish_env_value PUBLIC_HOST)" ]]
    return
  fi
  if [[ "$mode" == acme ]]; then
    [[ -f "$PANTRY_DIR/Caddyfile" ]] && [[ -n "$(publish_env_value PUBLIC_HOST)" ]]
    return
  fi
  return 1
}

host_entry_trace() {
  if [[ -n "${PANTRY_TEST_ENTRY_TRACE:-}" ]]; then
    printf '%s\n' "$1" >> "$PANTRY_TEST_ENTRY_TRACE"
  fi
}

host_setup_state_dir() {
  printf '%s/host-setup-state' "$PANTRY_DIR"
}

host_txn_target_rel() {
  case "$1" in
    1) printf '%s' "publish-mode.sh" ;;
    2) printf '%s' "runtime-setup.sh" ;;
    3) printf '%s' "vapid-env.sh" ;;
    4) printf '%s' "systemd/pantry-backup.sh" ;;
    *) return 1 ;;
  esac
}

host_txn_target_dest() {
  local id="$1" rel
  rel=$(host_txn_target_rel "$id") || return 1
  printf '%s/%s' "$PANTRY_DIR" "$rel"
}

host_txn_guard_rels() {
  printf '%s\n' \
    systemd/pantry-update.sh \
    systemd/pantry-update.service \
    systemd/pantry-update.timer \
    systemd/pantry-update.path \
    systemd/pantry-backup.service \
    systemd/pantry-backup.timer
}

host_txn_test_hook() {
  local point="$1"
  if [[ -n "${PANTRY_TEST_TRACE:-}" ]]; then
    printf '%s\n' "$point" >>"$PANTRY_TEST_TRACE"
  fi
  if [[ -n "${PANTRY_TEST_INTERRUPT_AT:-}" && "$PANTRY_TEST_INTERRUPT_AT" == "$point" ]]; then
    exit 42
  fi
  if [[ -n "${PANTRY_TEST_FAIL_AT:-}" && "$PANTRY_TEST_FAIL_AT" == "$point" ]]; then
    return 1
  fi
  if [[ -n "${PANTRY_TEST_HOLD_AT:-}" && "$PANTRY_TEST_HOLD_AT" == "$point" ]]; then
    if [[ -n "${PANTRY_TEST_LIVE_SIGNAL:-}" ]]; then
      while kill -0 "$$" 2>/dev/null; do
        sleep 0.05
      done
    fi
  fi
  return 0
}

host_descendant_pids() {
  local root_pid="$1" pid child depth=0
  local -a queue=()
  [[ -n "$root_pid" && "$root_pid" =~ ^[0-9]+$ ]] || return 0
  queue=("$root_pid")
  while ((${#queue[@]})) && [[ "$depth" -lt 32 ]]; do
    pid=${queue[0]}
    queue=("${queue[@]:1}")
    printf '%s\n' "$pid"
    while read -r child; do
      [[ -z "$child" ]] && continue
      queue+=("$child")
    done < <(ps -o pid= --ppid "$pid" 2>/dev/null || true)
    depth=$((depth + 1))
  done
}

host_reap_process_group() {
  local pgid="$1" root_pid="$2" pid
  while read -r pid; do
    [[ -z "$pid" || "$pid" == "$$" ]] && continue
    if kill -0 "$pid" 2>/dev/null; then
      wait "$pid" 2>/dev/null || true
    fi
  done < <( {
    if [[ -n "$pgid" && "$pgid" =~ ^[0-9]+$ ]]; then
      ps -o pid= --pgid "$pgid" 2>/dev/null || true
    fi
    if [[ -n "$root_pid" && "$root_pid" =~ ^[0-9]+$ ]]; then
      host_descendant_pids "$root_pid"
    fi
  } | sort -u)
}

host_run_bounded() {
  local budget="$1"; shift
  if [[ -n "${PANTRY_TEST_RUN_BOUNDED_RC+x}" ]]; then
    return "${PANTRY_TEST_RUN_BOUNDED_RC:-0}"
  fi
  if [[ -n "${PANTRY_TEST_SKIP_HEALTH:-}" ]]; then
    return 0
  fi
  local supervisor_pid child_pg start rc=0 budget_kill_done=0 caller_pgid
  start=$SECONDS
  caller_pgid=$(ps -o pgid= -p $$ 2>/dev/null | tr -d ' ')
  if command -v setsid >/dev/null 2>&1; then
    setsid bash -c 'exec "$@"' _ "$@" &
  else
    bash -c 'set -m; exec "$@"' _ "$@" &
  fi
  supervisor_pid=$!
  sleep 0.02
  child_pg=$(ps -o pgid= -p "$supervisor_pid" 2>/dev/null | tr -d ' ')
  bounded_kill_group() {
    [[ "$budget_kill_done" -eq 1 ]] && return
    budget_kill_done=1
    if [[ -n "$child_pg" && "$child_pg" != "$caller_pgid" ]]; then
      kill -TERM -- "-$child_pg" 2>/dev/null || kill -TERM "$supervisor_pid" 2>/dev/null || true
    else
      kill -TERM "$supervisor_pid" 2>/dev/null || true
    fi
    local grace=0
    while [[ "$grace" -lt "${HOST_TXN_KILL_GRACE}" ]]; do
      if ! kill -0 "$supervisor_pid" 2>/dev/null; then
        break
      fi
      sleep 1
      grace=$((grace + 1))
    done
    if kill -0 "$supervisor_pid" 2>/dev/null; then
      if [[ -n "$child_pg" && "$child_pg" != "$caller_pgid" ]]; then
        kill -KILL -- "-$child_pg" 2>/dev/null || true
      fi
      while read -r pid; do
        [[ -z "$pid" || "$pid" == "$$" ]] && continue
        kill -KILL "$pid" 2>/dev/null || true
      done < <(host_descendant_pids "$supervisor_pid")
      kill -KILL "$supervisor_pid" 2>/dev/null || true
    fi
  }
  while kill -0 "$supervisor_pid" 2>/dev/null; do
    if [[ $((SECONDS - start)) -ge "$budget" ]]; then
      bounded_kill_group
      break
    fi
    sleep 0.05
  done
  wait "$supervisor_pid" 2>/dev/null || rc=$?
  host_reap_process_group "$child_pg" "$supervisor_pid"
  if [[ "$budget_kill_done" -eq 1 ]]; then
    return 1
  fi
  if [[ "$rc" -eq 124 || "$rc" -eq 137 || "$rc" -eq 143 ]]; then
    return 1
  fi
  return "$rc"
}

host_sha256_file() {
  sha256sum "$1" | awk '{print $1}'
}

host_manifest_hash() {
  host_sha256_file "$1"
}

host_manifest_mode_and_size() {
  local path="$1" _mode _size
  _mode=$(stat -c '%a' "$path" 2>/dev/null || stat -f '%OLp' "$path")
  _size=$(stat -c '%s' "$path" 2>/dev/null || stat -f '%z' "$path")
  printf '%s\t%s' "$_mode" "$_size"
}

host_manifest_row_guard() {
  local rel="$1" path="$2" ms mode size sha
  if [[ -f "$path" ]]; then
    ms=$(host_manifest_mode_and_size "$path")
    mode=${ms%%$'\t'*}
    size=${ms##*$'\t'}
    sha=$(host_sha256_file "$path")
    printf 'guard,%s,1,%s,0,0,%s,%s\n' "$rel" "$mode" "$size" "$sha"
  else
    printf 'guard,%s,0,0755,0,0,0,absent\n' "$rel"
  fi
}

host_manifest_row_target() {
  local id="$1" path="$2" ms mode size sha
  if [[ -f "$path" ]]; then
    ms=$(host_manifest_mode_and_size "$path")
    mode=${ms%%$'\t'*}
    size=${ms##*$'\t'}
    sha=$(host_sha256_file "$path")
    printf '%s,1,%s,0,0,%s,%s\n' "$id" "$mode" "$size" "$sha"
  else
    printf '%s,0,0755,0,0,0,absent\n' "$id"
  fi
}

host_manifest_build_body() {
  local revision="$1" staged="$2" installed_mode="$3"
  local rel path id
  revision=${revision,,}
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || return 1
  printf 'version=1\nrevision=%s\n' "$revision"
  while IFS= read -r rel || [[ -n "$rel" ]]; do
    [[ -z "$rel" ]] && continue
    if [[ "$installed_mode" == 1 ]]; then
      path="$PANTRY_DIR/$rel"
    else
      path="$staged/$rel"
    fi
    host_manifest_row_guard "$rel" "$path" || return 1
  done < <(host_txn_guard_rels)
  for id in 1 2 3 4; do
    rel=$(host_txn_target_rel "$id") || return 1
    if [[ "$installed_mode" == 1 ]]; then
      path=$(host_txn_target_dest "$id")
    else
      path="$staged/$rel"
    fi
    host_manifest_row_target "$id" "$path" || return 1
  done
}

host_manifest_build_staged() {
  local staged="$1" revision="$2" out="$3"
  host_manifest_build_body "$revision" "$staged" 0 >"$out"
}

host_manifest_build_installed() {
  local revision="$1" out="$2"
  host_manifest_build_body "$revision" "" 1 >"$out"
}

host_manifest_parse_validate() {
  local man="$1" line rev seen_g seen_t
  [[ -f "$man" ]] || return 1
  rev=""
  seen_g=0
  seen_t=0
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue
    if [[ "$line" == version=1 ]]; then
      continue
    fi
    if [[ "$line" == revision=* ]]; then
      rev=${line#revision=}
      [[ "$rev" =~ ^[0-9a-f]{40}$ ]] || return 1
      continue
    fi
    if [[ "$line" == guard,* ]]; then
      seen_g=$((seen_g + 1))
      continue
    fi
    if [[ "$line" =~ ^[1-4], ]]; then
      seen_t=$((seen_t + 1))
      continue
    fi
    return 1
  done <"$man"
  [[ -n "$rev" && "$seen_g" -eq 6 && "$seen_t" -eq 4 ]]
}

host_snapshot_verify() {
  local snap="$1" man line id present path rowpath sha expect
  man="$snap/manifest"
  host_manifest_parse_validate "$man" || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[1-4], ]] || continue
    IFS=, read -r id present _mode _uid _gid _size sha <<<"$line"
    rowpath="$snap/files/$id"
    if [[ "$present" == 1 ]]; then
      [[ -f "$rowpath" ]] || return 1
      expect=$(host_sha256_file "$rowpath")
      [[ "$sha" == "$expect" ]] || return 1
    else
      [[ ! -e "$rowpath" ]] || return 1
    fi
  done <"$man"
}

host_snapshot_build_from_staged() {
  local snap="$1" staged="$2" revision="$3" id rel
  rm -rf "$snap"
  mkdir -p "$snap/files"
  for id in 1 2 3 4; do
    rel=$(host_txn_target_rel "$id") || return 1
    cp "$staged/$rel" "$snap/files/$id"
    chmod 755 "$snap/files/$id"
  done
  host_manifest_build_staged "$staged" "$revision" "$snap/manifest"
  host_snapshot_verify "$snap"
}

host_snapshot_select_by_hash() {
  local want="$1" root name man h
  root=$(host_setup_state_dir)
  for name in lkg prior candidate; do
    man="$root/$name/manifest"
    [[ -f "$man" ]] || continue
    h=$(host_manifest_hash "$man")
    if [[ "$h" == "$want" ]]; then
      printf '%s/%s' "$root" "$name"
      return 0
    fi
  done
  return 1
}

host_control_path() {
  printf '%s/control' "$(host_setup_state_dir)"
}

host_control_field() {
  local key="$1" ctrl val
  ctrl=$(host_control_path)
  [[ -f "$ctrl" ]] || return 1
  val=$(awk -F= -v k="$key" '$1 == k { print $2; exit }' "$ctrl" 2>/dev/null || true)
  [[ -n "$val" ]] || return 1
  printf '%s' "$val"
}

host_control_publish() {
  local root tmp ctrl
  root=$(host_setup_state_dir)
  ctrl="$root/control"
  tmp=$(mktemp "${root}/control.host-txn-v1.XXXXXX") || return 1
  {
    printf 'version=1\n'
    printf 'phase=%s\n' "${HOST_CTRL_PHASE:-clean}"
    printf 'committed_revision=%s\n' "${HOST_CTRL_COMMITTED:-none}"
    printf 'target_revision=%s\n' "${HOST_CTRL_TARGET:-none}"
    printf 'previous_manifest_sha256=%s\n' "${HOST_CTRL_PREV_MANIFEST:-none}"
    printf 'candidate_manifest_sha256=%s\n' "${HOST_CTRL_CAND_MANIFEST:-none}"
    printf 'original_reason=%s\n' "${HOST_CTRL_REASON:-none}"
    printf 'failed_target_sha=%s\n' "${HOST_CTRL_FAILED_TARGET:-none}"
  } >"$tmp"
  chmod 600 "$tmp" 2>/dev/null || true
  mv "$tmp" "$ctrl"
}

host_control_load_into_publish_vars() {
  HOST_CTRL_PHASE=$(host_control_field phase || printf '%s' clean)
  HOST_CTRL_COMMITTED=$(host_control_field committed_revision || printf '%s' none)
  HOST_CTRL_TARGET=$(host_control_field target_revision || printf '%s' none)
  HOST_CTRL_PREV_MANIFEST=$(host_control_field previous_manifest_sha256 || printf '%s' none)
  HOST_CTRL_CAND_MANIFEST=$(host_control_field candidate_manifest_sha256 || printf '%s' none)
  HOST_CTRL_REASON=$(host_control_field original_reason || printf '%s' none)
  HOST_CTRL_FAILED_TARGET=$(host_control_field failed_target_sha || printf '%s' none)
}

host_embedded_env_value() {
  local key="$1" line k v count=0 val=""
  [[ -f "$PANTRY_DIR/.env" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=${line%%#*}
    [[ "$line" == *"="* ]] || continue
    k=${line%%=*}
    k=${k//$' \t'/}
    [[ "$k" == "$key" ]] || continue
    count=$((count + 1))
    v=${line#*=}
    v=${v#"${v%%[![:space:]]*}"}
    v=${v%"${v##*[![:space:]]}"}
    if [[ "$v" == \"*\" && "$v" == *\" ]]; then
      return 2
    fi
    if [[ "$v" == \'*\' && "$v" == *\' ]]; then
      return 2
    fi
    val=$v
  done < "$PANTRY_DIR/.env"
  if [[ "$count" -eq 0 ]]; then
    return 1
  fi
  if [[ "$count" -gt 1 ]]; then
    return 2
  fi
  printf '%s' "$val"
  return 0
}

host_flags_read() {
  local hr as env_as
  hr=${PANTRY_HOST_REFRESH:-}
  hr=${hr:-}
  case "$hr" in
    ''|on) HOST_FLAG_HOST_REFRESH=allow ;;
    off) HOST_FLAG_HOST_REFRESH=deny ;;
    *) HOST_FLAG_HOST_REFRESH=invalid; HOST_FLAG_ANY_INVALID=1 ;;
  esac
  if [[ -n "${PANTRY_AUTO_SETUP+x}" && -n "${PANTRY_AUTO_SETUP}" ]]; then
    as=$PANTRY_AUTO_SETUP
  else
    local _env_rc=0
    as=
    env_as=$(host_embedded_env_value PANTRY_AUTO_SETUP) || _env_rc=$?
    if [[ "$_env_rc" -eq 0 ]]; then
      as=$env_as
    elif [[ "$_env_rc" -eq 2 ]]; then
      HOST_FLAG_AUTO_SETUP=invalid
      HOST_FLAG_ANY_INVALID=1
    fi
  fi
  if [[ "${HOST_FLAG_ANY_INVALID:-0}" -eq 0 ]]; then
    as=${as:-}
    case "$as" in
      ''|off) HOST_FLAG_AUTO_SETUP=off ;;
      on) HOST_FLAG_AUTO_SETUP=on ;;
      *) HOST_FLAG_AUTO_SETUP=invalid; HOST_FLAG_ANY_INVALID=1 ;;
    esac
  fi
  host_entry_trace "host_flags_read"
}

host_control_phase() {
  local ctrl phase
  ctrl="$(host_setup_state_dir)/control"
  [[ -f "$ctrl" ]] || return 1
  phase=$(awk -F= '$1 == "phase" { print $2; exit }' "$ctrl" 2>/dev/null || true)
  [[ -n "$phase" ]] && printf '%s' "$phase"
}

host_state_validate() {
  local root ctrl phase child base allowed lkg_man
  HOST_STATE_INITIALIZED=0
  HOST_STATE_CORRUPT=0
  HOST_STATE_BLOCKED=0
  root=$(host_setup_state_dir)
  if [[ ! -e "$root" ]]; then
    host_entry_trace "host_state_validate:uninitialized"
    return 0
  fi
  if [[ ! -d "$root" ]]; then
    HOST_STATE_CORRUPT=1
    HOST_STATE_BLOCKED=1
    echo "pantry update: host setup state is not a directory" >&2
    return 1
  fi
  HOST_STATE_INITIALIZED=1
  if [[ -L "$root" ]]; then
    HOST_STATE_CORRUPT=1
    HOST_STATE_BLOCKED=1
    echo "pantry update: host setup state path is a symlink" >&2
    return 1
  fi
  for child in "$root"/*; do
    [[ -e "$child" ]] || continue
    base=$(basename "$child")
    allowed=0
    case "$base" in
      control|control.tmp|seed.tmp|lkg|candidate|prior) allowed=1 ;;
    esac
    if [[ "$allowed" -eq 0 ]]; then
      HOST_STATE_CORRUPT=1
      HOST_STATE_BLOCKED=1
      echo "pantry update: host setup state has unexpected child ${base}" >&2
      return 1
    fi
  done
  ctrl="$root/control"
  if [[ -e "$root/control.tmp" && ! -f "$ctrl" ]]; then
    HOST_STATE_CORRUPT=1
    HOST_STATE_BLOCKED=1
    echo "pantry update: host setup state has only control.tmp" >&2
    return 1
  fi
  if [[ -f "$ctrl" ]]; then
    if [[ -L "$ctrl" ]]; then
      HOST_STATE_CORRUPT=1
      HOST_STATE_BLOCKED=1
      echo "pantry update: host setup control is a symlink" >&2
      return 1
    fi
    phase=$(host_control_phase)
    case "$phase" in
      clean)
        lkg_man="$root/lkg/manifest"
        if [[ -f "$lkg_man" ]] && ! host_manifest_parse_validate "$lkg_man"; then
          HOST_STATE_CORRUPT=1
          HOST_STATE_BLOCKED=1
          echo "pantry update: host setup lkg manifest is invalid" >&2
          return 1
        fi
        ;;
      recovered)
        host_entry_trace "host_state_validate:recovered"
        lkg_man="$root/lkg/manifest"
        if [[ -f "$lkg_man" ]] && ! host_manifest_parse_validate "$lkg_man"; then
          HOST_STATE_CORRUPT=1
          HOST_STATE_BLOCKED=1
          echo "pantry update: host setup lkg manifest is invalid" >&2
          return 1
        fi
        ;;
      prepared|applying|restoring|committing|blocked)
        host_entry_trace "host_state_validate:pending"
        return 0
        ;;
      '')
        HOST_STATE_CORRUPT=1
        HOST_STATE_BLOCKED=1
        echo "pantry update: host setup control has no phase" >&2
        return 1
        ;;
      *)
        HOST_STATE_CORRUPT=1
        HOST_STATE_BLOCKED=1
        echo "pantry update: host setup control has unknown phase ${phase}" >&2
        return 1
        ;;
    esac
  elif [[ -d "$root/lkg" || -d "$root/seed.tmp" ]]; then
    HOST_STATE_CORRUPT=1
    HOST_STATE_BLOCKED=1
    echo "pantry update: host setup state is missing control" >&2
    return 1
  fi
  host_entry_trace "host_state_validate:initialized"
  return 0
}

host_restore_from_snapshot() {
  local snap="$1" man line id present dest src tmp rel
  man="$snap/manifest"
  host_snapshot_verify "$snap" || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[1-4], ]] || continue
    IFS=, read -r id present _mode _uid _gid _size _sha <<<"$line"
    dest=$(host_txn_target_dest "$id") || return 1
    src="$snap/files/$id"
    if [[ "$present" == 1 ]]; then
      if [[ -f "$dest" ]] && cmp -s "$src" "$dest"; then
        continue
      fi
      tmp=$(mktemp "${dest}.host-txn-v1.XXXXXX") || return 1
      cp "$src" "$tmp" || { rm -f "$tmp"; return 1; }
      chmod 755 "$tmp" || { rm -f "$tmp"; return 1; }
      mv "$tmp" "$dest"
    else
      rm -f "$dest"
    fi
  done <"$man"
  host_txn_test_hook restore-done || return 1
  return 0
}

host_restore_transaction() {
  local snap="${1:-}" start=$SECONDS
  if [[ -z "$snap" ]]; then
    snap="$(host_setup_state_dir)/lkg"
  fi
  host_restore_from_snapshot "$snap" || return 1
  if [[ $((SECONDS - start)) -gt "$HOST_TXN_BUDGET_RESTORE" ]]; then
    return 1
  fi
  return 0
}

host_normalize_recovered_state() {
  local phase
  [[ "${HOST_STATE_INITIALIZED:-0}" -eq 1 ]] || return 0
  phase=$(host_control_phase || true)
  [[ "$phase" == recovered ]] || return 0
  host_control_load_into_publish_vars
  HOST_CTRL_PHASE=clean
  host_control_publish || return 1
  host_entry_trace "host_normalize_recovered"
  return 0
}

host_recover_pending() {
  local phase snap prev root reason failed
  if [[ "${HOST_STATE_INITIALIZED:-0}" -ne 1 ]]; then
    return 0
  fi
  phase=$(host_control_phase || true)
  case "$phase" in
    prepared|applying|restoring|committing|blocked)
      host_entry_trace "host_recover_pending"
      host_txn_test_hook pending-recover
      echo "pantry update: recovering pending host setup transaction (phase=${phase})"
      snap=""
      if [[ "$phase" == committing ]]; then
        prev=$(host_control_field previous_manifest_sha256 || true)
        if [[ -n "$prev" && "$prev" != none ]]; then
          snap=$(host_snapshot_select_by_hash "$prev" || true)
        fi
      fi
      if [[ -z "$snap" ]]; then
        snap="$(host_setup_state_dir)/lkg"
      fi
      if ! host_restore_transaction "$snap"; then
        echo "pantry update: host setup recovery restore failed" >&2
        HOST_RECOVERY_EXIT=1
        return 1
      fi
      root=$(host_setup_state_dir)
      host_control_load_into_publish_vars
      HOST_CTRL_PHASE=recovered
      HOST_CTRL_REASON=$(host_control_field original_reason || printf '%s' interrupted)
      HOST_CTRL_FAILED_TARGET=$(host_control_field failed_target_sha || printf '%s' none)
      host_control_publish || {
        HOST_RECOVERY_EXIT=1
        return 1
      }
      HOST_RECOVERY_EXIT=1
      echo "pantry update: host setup recovery finished; original transaction failed"
      return 2
      ;;
    *)
      return 0
      ;;
  esac
}

host_seed_from_manifest() {
  local _approved_manifest="$1"
  [[ "${PANTRY_TEST_ALLOW_MANUAL_SEED:-}" == 1 ]] || return 1
  [[ -n "$_approved_manifest" ]]
  host_txn_test_hook seed
  return 1
}

host_guard_validate() {
  local staged="$1" rel src dest
  [[ -n "$staged" && -d "$staged" ]] || return 1
  if [[ -f "$staged/systemd/pantry-update.sh" ]]; then
    if [[ -f "$PANTRY_DIR/systemd/pantry-update.sh" ]] \
      && ! cmp -s "$staged/systemd/pantry-update.sh" "$PANTRY_DIR/systemd/pantry-update.sh"; then
      echo "pantry update: host refresh refused: candidate changes frozen pantry-update.sh coordinator" >&2
      return 1
    fi
  fi
  for rel in systemd/pantry-update.service systemd/pantry-update.timer systemd/pantry-update.path \
    systemd/pantry-backup.service systemd/pantry-backup.timer; do
    src="$staged/$rel"
    dest="$PANTRY_DIR/$rel"
    [[ -f "$src" && -f "$dest" ]] || continue
    if ! cmp -s "$src" "$dest"; then
      echo "pantry update: host refresh refused: candidate changes frozen unit ${rel}" >&2
      return 1
    fi
  done
  return 0
}

host_subset_plan() {
  local staged="$1" id rel src dest
  for id in 1 2 3 4; do
    rel=$(host_txn_target_rel "$id") || return 1
    src="$staged/$rel"
    dest=$(host_txn_target_dest "$id")
    [[ -f "$src" ]] && printf '%s\t%s\n' "$src" "$dest"
  done
}

host_subset_validate() {
  local staged="$1" id rel src unit
  for id in 1 2 3 4; do
    rel=$(host_txn_target_rel "$id") || return 1
    src="$staged/$rel"
    if [[ ! -f "$src" ]]; then
      echo "pantry update: host subset bundle is missing ${rel}" >&2
      return 1
    fi
    if ! host_run_bounded "$HOST_TXN_BUDGET_CMD" bash -n "$src"; then
      echo "pantry update: host subset bundle failed bash -n for ${rel}" >&2
      return 1
    fi
  done
  if ! command -v systemd-analyze >/dev/null 2>&1; then
    echo "pantry update: host subset bundle validation requires systemd-analyze" >&2
    return 1
  fi
  for unit in \
    systemd/pantry-update.service \
    systemd/pantry-update.timer \
    systemd/pantry-update.path \
    systemd/pantry-backup.service \
    systemd/pantry-backup.timer
  do
    src="$staged/$unit"
    [[ -f "$src" ]] || continue
    if ! host_run_bounded "$HOST_TXN_BUDGET_VERIFY" systemd-analyze verify "$src"; then
      echo "pantry update: host subset bundle failed systemd-analyze verify for ${unit}" >&2
      return 1
    fi
  done
  return 0
}

host_revision_allowed() {
  local state="$1" revision prov_rc
  prov_rc=0
  host_bundle_provenance_ok "$state" || prov_rc=$?
  [[ "$prov_rc" -eq 0 ]]
}

host_target_retry_allowed() {
  local root ctrl failed revision
  [[ "${HOST_STATE_INITIALIZED:-0}" -eq 1 ]] || return 0
  root=$(host_setup_state_dir)
  ctrl="$root/control"
  [[ -f "$ctrl" ]] || return 0
  failed=$(host_control_field failed_target_sha || true)
  if [[ -n "$failed" && "$failed" != none ]]; then
    revision=$(host_control_field target_revision || true)
    if [[ -n "$revision" && "$revision" == "$failed" ]]; then
      echo "pantry update: host setup blocks retry for failed target ${failed}" >&2
      return 1
    fi
  fi
  return 0
}

host_installed_matches_lkg() {
  local root="$1" revision="${2:-}" tmp lkg
  root=${root:-$(host_setup_state_dir)}
  lkg="$root/lkg/manifest"
  [[ -f "$lkg" ]] || return 1
  [[ -n "$revision" && "$revision" != none ]] || return 1
  tmp=$(mktemp)
  host_manifest_build_installed "$revision" "$tmp" || { rm -f "$tmp"; return 1; }
  cmp -s "$lkg" "$tmp" || { rm -f "$tmp"; return 1; }
  rm -f "$tmp"
  return 0
}

host_txn_post_apply_validate() {
  local root revision tmp started
  started=$SECONDS
  root=$(host_setup_state_dir)
  revision=$(host_control_field target_revision || printf '%s' none)
  [[ "$revision" != none ]] || return 1
  local id rel dest
  for id in 1 2 3 4; do
    rel=$(host_txn_target_rel "$id") || return 1
    dest=$(host_txn_target_dest "$id")
    host_run_bounded "$HOST_TXN_BUDGET_CMD" bash -n "$dest" || return 1
  done
  tmp=$(mktemp)
  host_manifest_build_installed "$revision" "$tmp" || { rm -f "$tmp"; return 1; }
  cmp -s "$root/candidate/manifest" "$tmp" || { rm -f "$tmp"; return 1; }
  rm -f "$tmp"
  [[ $((SECONDS - started)) -le "$HOST_TXN_BUDGET_VERIFY" ]] || return 1
  return 0
}

host_txn_health_validate() {
  local state="$1" port rev body commit started
  [[ -n "${PANTRY_TEST_SKIP_HEALTH:-}" ]] && return 0
  started=$SECONDS
  port=$(host_embedded_env_value HOST_PORT || true)
  port=${port:-8080}
  rev=$(pantry_deploy_revision "$state")
  [[ -n "$rev" ]] || return 1
  host_run_bounded "$HOST_TXN_BUDGET_CURL" curl -fsS --max-time "$HOST_TXN_BUDGET_CURL" \
    "http://127.0.0.1:${port}/health" >/dev/null || return 1
  if [[ $((SECONDS - started)) -gt "$HOST_TXN_BUDGET_HEALTH" ]]; then
    return 1
  fi
  body=$(mktemp)
  host_run_bounded "$HOST_TXN_BUDGET_CURL" curl -fsS --max-time "$HOST_TXN_BUDGET_CURL" \
    "http://127.0.0.1:${port}/api/build" >"$body" || { rm -f "$body"; return 1; }
  commit=$(json_string_field "$(<"$body")" commit)
  rm -f "$body"
  [[ "$commit" == "$rev" ]] || return 1
  [[ $((SECONDS - started)) -le "$HOST_TXN_BUDGET_HEALTH" ]] || return 1
  return 0
}

host_txn_abort_to_recovered() {
  local reason="$1" failed_sha="$2" snap
  snap="$(host_setup_state_dir)/lkg"
  HOST_CTRL_PHASE=restoring
  host_control_load_into_publish_vars
  host_control_publish || true
  host_restore_transaction "$snap" || return 1
  HOST_CTRL_PHASE=recovered
  HOST_CTRL_REASON=$reason
  HOST_CTRL_FAILED_TARGET=${failed_sha:-none}
  host_control_publish || return 1
  return 0
}

host_retained_prior_present() {
  local root
  root=$(host_setup_state_dir)
  [[ -d "$root/prior" ]]
}

host_apply_transaction() {
  local snap="$1" id dest src tmp
  host_txn_test_hook replace-start || return 1
  for id in 1 2 3 4; do
    host_txn_test_hook "replace-${id}" || return 1
    if [[ -n "${PANTRY_TEST_APPLY_FAIL_ID:-}" && "$PANTRY_TEST_APPLY_FAIL_ID" == "$id" ]]; then
      return 1
    fi
    dest=$(host_txn_target_dest "$id") || return 1
    src="$snap/files/$id"
    if [[ -f "$dest" ]] && [[ -f "$src" ]] && cmp -s "$src" "$dest"; then
      continue
    fi
    tmp=$(mktemp "${dest}.host-txn-v1.XXXXXX") || return 1
    cp "$src" "$tmp" || { rm -f "$tmp"; return 1; }
    chmod 755 "$tmp" || { rm -f "$tmp"; return 1; }
    mv "$tmp" "$dest"
  done
  host_txn_test_hook replace-done || return 1
  return 0
}

host_commit_transaction() {
  local root prev_hash cand_hash revision
  root=$(host_setup_state_dir)
  host_txn_test_hook commit-start || return 1
  prev_hash=$(host_manifest_hash "$root/lkg/manifest")
  cand_hash=$(host_manifest_hash "$root/candidate/manifest")
  revision=$(host_control_field target_revision || printf '%s' none)
  HOST_CTRL_PHASE=committing
  host_control_load_into_publish_vars
  HOST_CTRL_PREV_MANIFEST=$prev_hash
  HOST_CTRL_CAND_MANIFEST=$cand_hash
  host_control_publish || return 1
  host_txn_test_hook committing || return 1
  if [[ -d "$root/prior" ]]; then
    echo "pantry update: host setup prior snapshot already present" >&2
    return 1
  fi
  mv "$root/lkg" "$root/prior"
  mv "$root/candidate" "$root/lkg"
  host_snapshot_verify "$root/lkg" || return 1
  HOST_CTRL_PHASE=clean
  HOST_CTRL_COMMITTED=$revision
  HOST_CTRL_TARGET=none
  HOST_CTRL_PREV_MANIFEST=$prev_hash
  HOST_CTRL_CAND_MANIFEST=none
  HOST_CTRL_REASON=none
  HOST_CTRL_FAILED_TARGET=none
  host_control_publish || return 1
  host_txn_test_hook commit-done || return 1
  return 0
}

host_subset_refresh_from_image() {
  local state="$1" cid work staged root revision cand_man lkg_hash man_tmp committed txn_start
  txn_start=$SECONDS
  if [[ -n "${PANTRY_TEST_TXN_START_ADJUST:-}" ]]; then
    txn_start=$((SECONDS - PANTRY_TEST_TXN_START_ADJUST))
  fi
  root=$(host_setup_state_dir)
  if host_retained_prior_present; then
    echo "pantry update: host setup refuses another promotion while prior snapshot is retained" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  if ! host_target_retry_allowed; then
    return "$HOST_TXN_FATAL_RC"
  fi
  cid=$(container_id pantry)
  if [[ -z "$cid" ]]; then
    echo "pantry update: host script refresh failed: pantry container id is missing" >&2
    return 1
  fi
  revision=$(pantry_deploy_revision "$state")
  revision=${revision,,}
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || {
    echo "pantry update: host subset transaction needs a 40-character revision" >&2
    return "$HOST_TXN_FATAL_RC"
  }
  if [[ "$(host_control_phase || true)" == clean ]] && [[ -f "$root/lkg/manifest" ]]; then
    committed=$(host_control_field committed_revision || true)
    committed=${committed:-$(awk -F= '$1=="revision"{print $2;exit}' "$root/lkg/manifest" 2>/dev/null || true)}
    if ! host_installed_matches_lkg "$root" "${committed:-$revision}"; then
      echo "pantry update: host setup installed scripts drift from lkg manifest" >&2
      return "$HOST_TXN_FATAL_RC"
    fi
  fi
  work=$(mktemp -d) || return 1
  staged="$work/bundle"
  mkdir -p "$staged"
  if ! docker cp "${cid}:${HOST_BUNDLE_CONTAINER_PATH}/." "$staged"; then
    rm -rf "$work"
    echo "pantry update: host script refresh failed: could not read host scripts from the pantry container" >&2
    return 1
  fi
  if ! host_guard_validate "$staged"; then
    rm -rf "$work"
    return "$HOST_TXN_FATAL_RC"
  fi
  if ! host_subset_validate "$staged"; then
    rm -rf "$work"
    return "$HOST_TXN_FATAL_RC"
  fi
  if [[ -n "${PANTRY_TEST_VALIDATION_FAIL:-}" ]]; then
    rm -rf "$work"
    return "$HOST_TXN_FATAL_RC"
  fi
  if ! host_revision_allowed "$state"; then
    rm -rf "$work"
    return 1
  fi
  if [[ -f "$root/lkg/manifest" ]]; then
    lkg_hash=$(host_manifest_hash "$root/lkg/manifest")
    man_tmp=$(mktemp)
    host_manifest_build_staged "$staged" "$revision" "$man_tmp" || { rm -f "$man_tmp"; rm -rf "$work"; return "$HOST_TXN_FATAL_RC"; }
    cand_man=$(host_manifest_hash "$man_tmp")
    rm -f "$man_tmp"
    if [[ "$cand_man" == "$lkg_hash" ]]; then
      host_snapshot_verify "$root/lkg" || {
        rm -rf "$work"
        return "$HOST_TXN_FATAL_RC"
      }
      rm -rf "$work"
      echo "pantry update: host subset unchanged at revision ${revision}"
      return 0
    fi
  fi
  if ! host_snapshot_build_from_staged "$root/candidate" "$staged" "$revision"; then
    rm -rf "$work"
    echo "pantry update: host subset candidate snapshot failed" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  cand_man=$(host_manifest_hash "$root/candidate/manifest")
  lkg_hash=none
  if [[ -f "$root/lkg/manifest" ]]; then
    lkg_hash=$(host_manifest_hash "$root/lkg/manifest")
  fi
  HOST_CTRL_PHASE=prepared
  HOST_CTRL_COMMITTED=$(host_control_field committed_revision || printf '%s' none)
  if [[ "$HOST_CTRL_COMMITTED" == none && -f "$root/lkg/manifest" ]]; then
    HOST_CTRL_COMMITTED=$(awk -F= '$1=="revision"{print $2;exit}' "$root/lkg/manifest" || printf '%s' none)
  fi
  HOST_CTRL_TARGET=$revision
  HOST_CTRL_PREV_MANIFEST=$lkg_hash
  HOST_CTRL_CAND_MANIFEST=$cand_man
  HOST_CTRL_REASON=none
  HOST_CTRL_FAILED_TARGET=none
  host_control_publish || { rm -rf "$work"; return "$HOST_TXN_FATAL_RC"; }
  host_txn_test_hook prepared || { rm -rf "$work"; return "$HOST_TXN_FATAL_RC"; }
  HOST_CTRL_PHASE=applying
  host_control_publish || { rm -rf "$work"; return "$HOST_TXN_FATAL_RC"; }
  host_txn_test_hook applying || { rm -rf "$work"; return "$HOST_TXN_FATAL_RC"; }
  if ! host_apply_transaction "$root/candidate"; then
    host_txn_abort_to_recovered apply_failed "$revision" || true
    rm -rf "$work"
    echo "pantry update: host subset transaction apply failed" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  if ! host_txn_post_apply_validate; then
    host_txn_abort_to_recovered validation_failed "$revision" || true
    rm -rf "$work"
    echo "pantry update: host subset post-apply validation failed" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  if ! host_txn_health_validate "$state"; then
    host_txn_abort_to_recovered health_failed "$revision" || true
    rm -rf "$work"
    echo "pantry update: host subset health validation failed" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  if [[ $((SECONDS - txn_start)) -gt "$HOST_TXN_BUDGET_TXN" ]]; then
    host_txn_abort_to_recovered time_budget_exceeded "$revision" || true
    rm -rf "$work"
    echo "pantry update: host subset transaction exceeded time budget before commit" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  if ! host_commit_transaction; then
    host_txn_abort_to_recovered recovery_failed "$revision" || true
    rm -rf "$work"
    echo "pantry update: host subset commit failed" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  rm -rf "$work"
  echo "pantry update: refreshed host deploy scripts from the running image (subset transaction)"
  return 0
}

host_acquire_deploy_lock() {
  exec 9>>"$PANTRY_DIR/deploy-trigger/.lock"
  if ! flock -n 9; then
    echo "pantry update: another update is running; waiting so this trigger is not dropped"
    flock 9
  fi
  echo "pantry update: lock acquired"
  host_entry_trace "deploy_lock"
}

host_prepare_deploy_trigger_dir() {
  mkdir -p "$PANTRY_DIR/deploy-trigger"
  host_entry_trace "deploy_trigger_dir_ready"
  if ! chown 65532:65532 "$PANTRY_DIR/deploy-trigger"; then
    if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
      echo "[warn] Could not give ${PANTRY_DIR}/deploy-trigger to uid 65532" >&2
    else
      echo "Could not give ${PANTRY_DIR}/deploy-trigger to uid 65532" >&2
      return 1
    fi
  fi
  chmod 755 "$PANTRY_DIR/deploy-trigger"
}

host_gate_before_operational() {
  local gate_rc=0
  host_state_validate || gate_rc=$?
  if [[ "${HOST_STATE_BLOCKED:-0}" -eq 1 || "${HOST_STATE_CORRUPT:-0}" -eq 1 ]]; then
    echo "pantry update: blocked by host setup state" >&2
    exit 1
  fi
  host_normalize_recovered_state || {
    echo "pantry update: host setup could not normalize recovered state" >&2
    exit 1
  }
  host_recover_pending || gate_rc=$?
  if [[ "$gate_rc" -eq 2 ]]; then
    exit "${HOST_RECOVERY_EXIT:-1}"
  fi
  if [[ "$gate_rc" -ne 0 ]]; then
    echo "pantry update: host setup recovery failed" >&2
    exit 1
  fi
  host_flags_read
  if [[ "${HOST_FLAG_ANY_INVALID:-0}" -eq 1 ]]; then
    if [[ "${HOST_STATE_INITIALIZED:-0}" -eq 1 ]]; then
      echo "pantry update: invalid host setup flags; refusing host writes" >&2
    fi
  fi
  host_prepare_deploy_trigger_dir || exit 1
}

host_should_freeze_writes() {
  if [[ "${HOST_FLAG_ANY_INVALID:-0}" -eq 1 ]]; then
    return 0
  fi
  if [[ "${HOST_STATE_INITIALIZED:-0}" -eq 1 ]]; then
    if [[ "${HOST_FLAG_AUTO_SETUP:-off}" != on ]]; then
      return 0
    fi
    if [[ "${HOST_FLAG_HOST_REFRESH:-allow}" == deny ]]; then
      return 0
    fi
  fi
  return 1
}

trigger_digest() {
  if [[ -f "$PANTRY_DIR/deploy-trigger/request" ]]; then
    sha256sum "$PANTRY_DIR/deploy-trigger/request" | awk '{print $1}'
  fi
}

# compose_service_images prints "service<TAB>image" for the compose command
# this update will actually run (profiles included). `docker compose config`
# is YAML; python3 is not required.
compose_service_images() {
  local config svc image
  config=$("${compose[@]}" config) || return 1
  while IFS=$'\t' read -r svc image; do
    [[ -z "$svc" ]] && continue
    image=${image#\"}
    image=${image%\"}
    image=${image#\'}
    image=${image%\'}
    printf '%s\t%s\n' "$svc" "$image"
  done < <(printf '%s\n' "$config" | awk '
    /^services:/ { in_s = 1; next }
    in_s && /^[^ #]/ { in_s = 0 }
    in_s && /^  [^ ].*:[[:space:]]*$/ {
      svc = $1
      sub(/:$/, "", svc)
      next
    }
    in_s && /^    image:[[:space:]]/ {
      img = $0
      sub(/^    image:[[:space:]]+/, "", img)
      if (svc != "") print svc "\t" img
    }
  ')
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

service_ref() {
  awk -F '\t' -v s="$1" '$1 == s { print $2; exit }' "$2/refs.tsv"
}

running_field() {
  awk -F '\t' -v s="$1" -v col="$2" '$1 == s { print $col; exit }' "$3/running.tsv"
}

service_pulled_id() {
  local ref id
  ref=$(service_ref "$1" "$2")
  [[ -n "$ref" ]] || return 1
  id=$(docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null || true)
  id=$(one_line "$id")
  [[ -n "$id" ]] || return 1
  printf '%s' "$id"
}

service_changed() {
  local running_id pulled
  running_id=$(running_field "$2" 2 "$1")
  pulled=$(service_pulled_id "$2" "$1" || true)
  [[ -n "$pulled" && "$pulled" != "$running_id" ]]
}

service_desired_digest() {
  local ref digest id
  ref=$(service_ref "$2" "$1")
  [[ -n "$ref" ]] || return 0
  id=$(service_pulled_id "$2" "$1" || true)
  if [[ -n "$id" ]]; then
    digest=$(image_repo_digest "$id" "$ref")
  fi
  if [[ -z "${digest:-}" ]]; then
    digest=$id
  fi
  printf '%s' "$digest"
}

image_revision() {
  local revision
  revision=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$1" 2>/dev/null || true)
  revision=$(one_line "$revision")
  if [[ -z "$revision" || "$revision" == "<no value>" ]]; then
    return 1
  fi
  printf '%s' "$revision"
}

# digest_was_rejected SERVICE DIGEST is true when that pair was recorded.
# A legacy line with only the digest still matches.
digest_was_rejected() {
  local svc="$1" digest="$2" file="$PANTRY_DIR/.update-rejected-digest" line got_svc got_digest
  [[ -n "$digest" && -f "$file" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ "$line" != *$'\t'* ]]; then
      [[ "$line" == "$digest" ]] && return 0
      continue
    fi
    got_svc=${line%%$'\t'*}
    got_digest=${line#*$'\t'}
    [[ "$got_svc" == "$svc" && "$got_digest" == "$digest" ]] && return 0
  done < "$file"
  return 1
}

remember_rejected_digest() {
  local svc="$1" digest="$2" file="$PANTRY_DIR/.update-rejected-digest"
  if [[ -z "$svc" || -z "$digest" ]]; then
    return 0
  fi
  if digest_was_rejected "$svc" "$digest"; then
    return 0
  fi
  if [[ ! -f "$file" ]]; then
    if ! printf '%s\n' '# Image digests rolled back by pantry-update.sh, one service per line. Not a setup last-known-good sha.' > "$file"; then
      echo "PANTRY UPDATE FAILED: could not write ${file}; rejected ${svc} digest ${digest} was not recorded" >&2
      return 1
    fi
  fi
  if ! printf '%s\t%s\n' "$svc" "$digest" >> "$file"; then
    echo "PANTRY UPDATE FAILED: could not record rejected ${svc} digest ${digest} in ${file}" >&2
    return 1
  fi
  echo "pantry update: recorded rejected ${svc} digest ${digest}" >&2
}

note_failed() {
  local svc="$1" line
  [[ -n "$svc" ]] || return 0
  while IFS= read -r line; do
    [[ "$line" == "$svc" ]] && return 0
  done <<< "$FAILED_SERVICES"
  FAILED_SERVICES+="${svc}"$'\n'
}

# changed_failure_named is true when a changed service was recorded as down
# or failing. An unchanged service is not recorded there.
changed_failure_named() {
  local line
  while IFS= read -r line; do
    [[ -n "$line" ]] && return 0
  done <<< "$FAILED_SERVICES"
  return 1
}

# should_reject_service is true for every changed service when pantry did
# not pass health and build. After it has, only services that actually
# failed are rejected.
should_reject_service() {
  local svc="$1" line
  if [[ "$PANTRY_VERIFIED" -eq 0 ]]; then
    return 0
  fi
  while IFS= read -r line; do
    [[ "$line" == "$svc" ]] && return 0
  done <<< "$FAILED_SERVICES"
  return 1
}

# remember_changed_digest records one changed service. Returns 0 when the
# write worked, and 2 when this service was not selected.
remember_changed_digest() {
  local state="$1" svc="$2" desired
  service_changed "$state" "$svc" || return 2
  if ! should_reject_service "$svc"; then
    return 2
  fi
  desired=$(service_desired_digest "$state" "$svc")
  remember_rejected_digest "$svc" "$desired"
}

record_changed_rejections() {
  local state="$1" svc running_id previous rc recorded=0 failed=0
  while IFS=$'\t' read -r svc running_id previous; do
    [[ -z "$svc" ]] && continue
    rc=0
    remember_changed_digest "$state" "$svc" || rc=$?
    if [[ "$rc" -eq 0 ]]; then
      recorded=1
    elif [[ "$rc" -ne 2 ]]; then
      failed=1
    fi
  done < "$state/running.tsv"
  if [[ "$recorded" -eq 0 ]]; then
    echo "PANTRY UPDATE FAILED: no changed digest was recorded, so the next run may try this release again" >&2
    return 1
  fi
  [[ "$failed" -eq 0 ]]
}

# prune_skip_warnings drops warning marks whose digest is no longer rejected,
# so deleting a line in .update-rejected-digest warns again the next time
# that digest is skipped.
prune_skip_warnings() {
  local mark="$PANTRY_DIR/.update-skip-warned" tmp svc digest
  [[ -f "$mark" ]] || return 0
  tmp=$(mktemp) || return 0
  while IFS=$'\t' read -r svc digest || [[ -n "${svc:-}" ]]; do
    [[ -z "${svc:-}" || -z "${digest:-}" ]] && continue
    if digest_was_rejected "$svc" "$digest"; then
      printf '%s\t%s\n' "$svc" "$digest"
    fi
  done < "$mark" > "$tmp"
  mv "$tmp" "$mark"
}

# note_skipped_digest warns the first time a digest is skipped. Later
# minutes keep the one-line skip. <4> is a syslog warning for journald.
note_skipped_digest() {
  local svc="$1" digest="$2" mark="$PANTRY_DIR/.update-skip-warned" line
  if [[ -f "$mark" ]]; then
    while IFS= read -r line; do
      if [[ "$line" == "${svc}"$'\t'"${digest}" ]]; then
        echo "pantry update: skipping rejected ${digest}" >&2
        return 0
      fi
    done < "$mark"
  fi
  echo "<4>pantry update: warning: skipping rejected ${digest}; delete that line in .update-rejected-digest to retry it" >&2
  if ! printf '%s\t%s\n' "$svc" "$digest" >> "$mark"; then
    echo "pantry update: could not record that ${digest} was already warned" >&2
  fi
}

# park_rejected_services points each rejected digest's local tag back at
# the previous image. Services that are not rejected stay on the pulled
# digest so a healthy pantry image can still be deployed.
park_rejected_services() {
  local state="$1" svc running_id previous desired ref pin
  prune_skip_warnings
  while IFS=$'\t' read -r svc running_id previous; do
    [[ -z "$svc" ]] && continue
    service_changed "$state" "$svc" || continue
    desired=$(service_desired_digest "$state" "$svc")
    if ! digest_was_rejected "$svc" "$desired"; then
      continue
    fi
    note_skipped_digest "$svc" "$desired"
    ref=$(service_ref "$svc" "$state")
    [[ -z "$ref" || "$ref" == *@sha256:* ]] && continue
    pin=$previous
    [[ -z "$pin" ]] && pin=$running_id
    [[ -z "$pin" ]] && continue
    if ! docker tag "$pin" "$ref"; then
      echo "pantry update: could not retag ${ref} to ${pin}" >&2
      return 1
    fi
    echo "pantry update: retagged ${ref} to ${pin}" >&2
  done < "$state/running.tsv"
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

health_is_migrating() {
  [[ "$1" =~ \"status\"[[:space:]]*:[[:space:]]*\"migrating\" ]]
}

# mono_seconds is seconds since boot. SECONDS follows the wall clock, and a
# Pi without an RTC can step that clock when NTP starts.
mono_seconds() {
  if [[ -r /proc/uptime ]]; then
    awk '{printf "%d\n", $1}' /proc/uptime
    return 0
  fi
  printf '%s\n' "${SECONDS:-0}"
}

container_id() {
  local cid
  cid=$("${compose[@]}" ps -aq "$1" 2>/dev/null | head -n 1 || true)
  cid=$(one_line "$cid")
  printf '%s' "$cid"
}

container_state() {
  local cid line
  cid=$(container_id "$1")
  [[ -n "$cid" ]] || return 1
  line=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' "$cid" 2>/dev/null || true)
  line=$(one_line "$line")
  [[ -n "$line" ]] || return 1
  printf '%s' "$line"
}

# container_started_at is State.StartedAt. Compose before 2.35 can replace
# a container's process without changing its id or RestartCount.
container_started_at() {
  local cid when
  cid=$(container_id "$1")
  [[ -n "$cid" ]] || return 1
  when=$(docker inspect -f '{{.State.StartedAt}}' "$cid" 2>/dev/null || true)
  when=$(one_line "$when")
  [[ -n "$when" ]] || return 1
  printf '%s' "$when"
}

# proxy_container_steady is true when the proxy container is the same one
# that was running before recreate: still running, same id, same restart
# count, same StartedAt. A crash, recreate, or restart is the proxy's
# failure, not pantry's.
proxy_container_steady() {
  local state="$1" svc="$2" line status restarts cid started before_status before_restarts before_cid before_started
  line=$(container_state "$svc" || true)
  status=${line%% *}
  restarts=${line##* }
  [[ "$status" == running ]] || return 1
  cid=$(container_id "$svc")
  started=$(container_started_at "$svc" || true)
  before_status=$(awk -F '\t' -v s="$svc" '$1 == s { print $2; exit }' "$state/pre-deploy.tsv")
  before_restarts=$(awk -F '\t' -v s="$svc" '$1 == s { print $3; exit }' "$state/pre-deploy.tsv")
  before_cid=$(awk -F '\t' -v s="$svc" '$1 == s { print $4; exit }' "$state/pre-deploy.tsv")
  before_started=$(awk -F '\t' -v s="$svc" '$1 == s { print $5; exit }' "$state/pre-deploy.tsv")
  [[ "$before_status" == running ]] || return 1
  [[ "$restarts" =~ ^[0-9]+$ && "$before_restarts" =~ ^[0-9]+$ && "$restarts" -eq "$before_restarts" ]] || return 1
  [[ -n "$cid" && "$cid" == "$before_cid" ]] || return 1
  [[ -n "$started" && "$started" == "$before_started" ]]
}

# pantry_container_failed BASELINE is true when pantry has exited, is
# restarting, or its RestartCount has risen.
pantry_container_failed() {
  local baseline="$1" line status restarts
  line=$(container_state pantry || true)
  if [[ -z "$line" ]]; then
    echo "pantry update: pantry container is missing" >&2
    return 0
  fi
  status=${line%% *}
  restarts=${line##* }
  if [[ "$status" == exited || "$status" == restarting || "$status" == dead ]]; then
    echo "pantry update: pantry container is ${status}" >&2
    return 0
  fi
  if [[ "$restarts" =~ ^[0-9]+$ && "$baseline" =~ ^[0-9]+$ && "$restarts" -gt "$baseline" ]]; then
    echo "pantry update: pantry container restarted (${baseline} -> ${restarts})" >&2
    return 0
  fi
  return 1
}

# wait_for_expected_build STATE [REVISION]
# A second argument, even an empty one, is the commit to expect. Empty means
# /health ok is enough (the previous image had no revision label). With one
# argument the commit is the pantry image's current label.
wait_for_expected_build() {
  local state="$1" supplied=0 revision="" ref host_port timeout interval migr_cap
  local start now short_deadline hard_deadline health build commit baseline line
  if [[ "$#" -ge 2 ]]; then
    supplied=1
    revision=$2
  fi
  ref=$(service_ref pantry "$state")
  if [[ -z "$ref" ]]; then
    echo "pantry update: compose project has no pantry service" >&2
    return 1
  fi
  if [[ "$supplied" -eq 0 ]]; then
    revision=$(image_revision "$ref" || true)
    if [[ -z "$revision" ]]; then
      echo "pantry update: ${ref} has no org.opencontainers.image.revision label" >&2
      return 1
    fi
  fi
  host_port=$(publish_env_value HOST_PORT)
  host_port=${host_port:-8080}
  timeout=${PANTRY_UPDATE_HEALTH_TIMEOUT:-90}
  interval=${PANTRY_UPDATE_HEALTH_INTERVAL:-2}
  migr_cap=${PANTRY_UPDATE_MIGRATION_TIMEOUT:-600}
  if [[ -n "$revision" ]]; then
    echo "pantry update: waiting for /health and /api/build commit ${revision} on 127.0.0.1:${host_port}"
  else
    echo "pantry update: waiting for /health on 127.0.0.1:${host_port}"
  fi
  start=$(mono_seconds)
  short_deadline=$((start + timeout))
  hard_deadline=$((start + migr_cap))
  line=$(container_state pantry || true)
  baseline=${line##* }
  [[ "$baseline" =~ ^[0-9]+$ ]] || baseline=0
  health=""
  build=""
  while true; do
    if pantry_container_failed "$baseline"; then
      return 1
    fi
    # curl -f hides the body of a 503, which is how /health reports migrating.
    health=$(curl -sS --max-time 5 "http://127.0.0.1:${host_port}/health" 2>/dev/null || true)
    if health_is_migrating "$health"; then
      now=$(mono_seconds)
      if [[ "$now" -ge "$hard_deadline" ]]; then
        echo "pantry update: still migrating after ${migr_cap}s" >&2
        return 1
      fi
      echo "pantry update: migrations still running"
      sleep "$interval"
      continue
    fi
    build=$(curl -sS --max-time 5 "http://127.0.0.1:${host_port}/api/build" 2>/dev/null || true)
    commit=$(json_string_field "$build" commit)
    if health_is_ok "$health"; then
      if [[ -z "$revision" || ( -n "$commit" && "$commit" == "$revision" ) ]]; then
        if [[ "${ROLLBACK_HEALTH:-}" == 1 ]]; then
          if [[ -n "$commit" ]]; then
            echo "pantry update: previous image healthy, commit ${commit}"
          else
            echo "pantry update: previous image healthy"
          fi
        elif [[ -n "$commit" ]]; then
          echo "pantry update: healthy, running commit ${commit}"
        else
          echo "pantry update: healthy"
        fi
        return 0
      fi
    fi
    now=$(mono_seconds)
    if [[ "$timeout" -le 0 || "$now" -ge "$short_deadline" ]]; then
      break
    fi
    sleep "$interval"
  done
  echo "pantry update: health check timed out after ${timeout}s (wanted commit ${revision:-<any>})" >&2
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

free_kib() {
  df -Pk "$1" 2>/dev/null | awk 'NR==2 { print $4 }'
}

ensure_free_space() {
  local min path free root
  min=${PANTRY_UPDATE_MIN_FREE_KIB:-65536}
  for path in "$PANTRY_DIR" /tmp; do
    free=$(free_kib "$path" || true)
    if [[ -z "$free" || ! "$free" =~ ^[0-9]+$ ]]; then
      echo "PANTRY UPDATE FAILED: could not read free space on ${path}" >&2
      return 1
    fi
    if [[ "$free" -lt "$min" ]]; then
      echo "PANTRY UPDATE FAILED: ${path} has ${free} KiB free; need at least ${min} before deploying" >&2
      return 1
    fi
  done
  root=$(docker info -f '{{.DockerRootDir}}' 2>/dev/null || true)
  root=$(one_line "$root")
  if [[ -n "$root" && -d "$root" ]]; then
    free=$(free_kib "$root" || true)
    if [[ -n "$free" && "$free" =~ ^[0-9]+$ && "$free" -lt "$min" ]]; then
      echo "PANTRY UPDATE FAILED: docker root ${root} has ${free} KiB free; need at least ${min} before deploying" >&2
      return 1
    fi
  fi
}

save_rejected_logs() {
  local cid dest
  cid=$("${compose[@]}" ps -aq pantry 2>/dev/null | head -n 1 || true)
  cid=$(one_line "$cid")
  dest="$PANTRY_DIR/update-rejected-pantry.log"
  if [[ -z "$cid" ]]; then
    echo "pantry update: no pantry container to capture logs from" >&2
    return 0
  fi
  if docker logs --tail 300 "$cid" >"$dest" 2>&1; then
    echo "pantry update: saved rejected container logs to ${dest}" >&2
  else
    echo "PANTRY UPDATE FAILED: could not save container logs to ${dest}" >&2
  fi
}

# newest_snapshot_path prints a pantry-pre-*.db path only when the rejected
# container log says this deploy wrote it. "already exists" names an older
# file and is ignored. The last write in the log wins.
newest_snapshot_path() {
  local log="$PANTRY_DIR/update-rejected-pantry.log" line path newest=""
  [[ -f "$log" ]] || return 0
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" == *"already exists"* ]] && continue
    [[ "$line" == *"wrote"* && "$line" == *"backup"* ]] || continue
    path=$(printf '%s\n' "$line" | grep -oE '/data/pantry-pre-[^[:space:]]+\.db' | tail -n 1 || true)
    [[ -n "$path" ]] && newest=$path
  done < "$log"
  printf '%s' "$newest"
}

log_both_down() {
  cat >&2 <<'EOF'
================================================================
BOTH IMAGES DOWN
The new image failed and the previous image did not become healthy.
The site may be unreachable. This script does not restore the database.
================================================================
EOF
}

write_pin_override() {
  local pins="$1" override="$2" svc pin rest any=0
  if ! printf 'services:\n' > "$override"; then
    return 1
  fi
  while IFS=$'\t' read -r svc pin rest; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    [[ -z "$pin" ]] && continue
    any=1
    printf '  %s:\n    image: %s\n' "$svc" "$pin" >> "$override"
    echo "pantry update: re-pinning ${svc} to ${pin}" >&2
  done < "$pins"
  [[ "$any" -eq 1 ]]
}

pins_from_running() {
  local state="$1" dest="$2" svc running_id digest pin
  : > "$dest"
  while IFS=$'\t' read -r svc running_id digest; do
    [[ -z "$svc" ]] && continue
    pin=$digest
    [[ -z "$pin" ]] && pin=$running_id
    [[ -z "$pin" ]] && continue
    printf '%s\t%s\n' "$svc" "$pin" >> "$dest"
  done < "$state/running.tsv"
}

# rollback_to_pins STATE PINS
# Returns 0 when the previous image is healthy, 2 when it started but is not
# healthy, and 1 when it did not start. Callers record a rejection only for
# 0 and 2.
rollback_to_pins() {
  local state="$1" pins="$2" override snap old_pin old_rev
  override=$(mktemp) || {
    echo "PANTRY UPDATE FAILED: could not create a rollback override file" >&2
    log_both_down
    return 1
  }
  if ! write_pin_override "$pins" "$override"; then
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
  save_rejected_logs
  log_rollback_banner
  snap=$(newest_snapshot_path || true)
  if [[ -n "$snap" ]]; then
    echo "Pre-migration snapshot written by this deploy: ${snap}" >&2
  else
    echo "pantry update: no new snapshot found" >&2
  fi
  if ! "${compose[@]}" -f "$override" up -d --pull never; then
    echo "PANTRY UPDATE FAILED: could not start the previous image." >&2
    log_both_down
    rm -f "$override"
    return 1
  fi
  rm -f "$override"
  old_pin=$(awk -F '\t' '$1 == "pantry" { print $2; exit }' "$pins")
  old_rev=""
  if [[ -n "$old_pin" ]]; then
    old_rev=$(image_revision "$old_pin" || true)
  fi
  ROLLBACK_HEALTH=1
  if ! wait_for_expected_build "$state" "$old_rev"; then
    ROLLBACK_HEALTH=""
    log_both_down
    return 2
  fi
  ROLLBACK_HEALTH=""
  return 0
}

write_pending() {
  local state="$1" tmp svc running_id previous desired pin
  tmp=$(mktemp "${PANTRY_DIR}/.update-pending.XXXXXX") || {
    echo "PANTRY UPDATE FAILED: could not create the pending-update marker" >&2
    return 1
  }
  while IFS=$'\t' read -r svc running_id previous; do
    [[ -z "$svc" ]] && continue
    desired=$(service_desired_digest "$state" "$svc")
    pin=$previous
    [[ -z "$pin" ]] && pin=$running_id
    printf '%s\t%s\t%s\n' "$svc" "$pin" "$desired"
  done < "$state/running.tsv" > "$tmp"
  if ! mv "$tmp" "$PANTRY_DIR/.update-pending"; then
    echo "PANTRY UPDATE FAILED: could not write ${PANTRY_DIR}/.update-pending" >&2
    rm -f "$tmp"
    return 1
  fi
}

# deploy_was_started is true when a service's running digest is the digest
# .update-pending said we were deploying, and that digest is not the previous one.
deploy_was_started() {
  local state="$1" svc pin desired current
  [[ -f "$PANTRY_DIR/.update-pending" ]] || return 1
  while IFS=$'\t' read -r svc pin desired; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    [[ -n "$desired" && "$desired" != "$pin" ]] || continue
    current=$(running_field "$svc" 3 "$state")
    if [[ "$current" == "$desired" ]]; then
      return 0
    fi
  done < "$PANTRY_DIR/.update-pending"
  return 1
}

record_pending_rejections() {
  local svc pin desired failed=0 recorded=0 spill=0
  while IFS=$'\t' read -r svc pin desired; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    [[ -n "$desired" && "$desired" != "$pin" ]] || continue
    if [[ "$spill" -eq 0 ]] && ! should_reject_service "$svc"; then
      continue
    fi
    if remember_rejected_digest "$svc" "$desired"; then
      recorded=1
    else
      failed=1
    fi
  done < "$PANTRY_DIR/.update-pending"
  if [[ "$recorded" -eq 0 && "$spill" -eq 0 ]]; then
    echo "<4>pantry update: warning: could not pin the failure on one service; rejecting every changed digest" >&2
    PANTRY_VERIFIED=0
    FAILED_SERVICES=""
    spill=1
    while IFS=$'\t' read -r svc pin desired; do
      [[ -z "$svc" || "$svc" == \#* ]] && continue
      [[ -n "$desired" && "$desired" != "$pin" ]] || continue
      if remember_rejected_digest "$svc" "$desired"; then
        recorded=1
      else
        failed=1
      fi
    done < "$PANTRY_DIR/.update-pending"
  fi
  if [[ "$recorded" -eq 0 ]]; then
    echo "PANTRY UPDATE FAILED: no changed digest was recorded, so the next run may try this release again" >&2
    return 1
  fi
  [[ "$failed" -eq 0 ]]
}

retag_pins() {
  local file="$1" state="$2" svc pin desired ref
  while IFS=$'\t' read -r svc pin desired; do
    [[ -z "$svc" || "$svc" == \#* ]] && continue
    ref=$(service_ref "$svc" "$state")
    [[ -z "$ref" || "$ref" == *@sha256:* || -z "$pin" ]] && continue
    if ! docker tag "$pin" "$ref"; then
      echo "pantry update: could not retag ${ref} to ${pin}" >&2
      return 1
    fi
    echo "pantry update: retagged ${ref} to ${pin}" >&2
  done < "$file"
}

# record_container_baseline saves status, RestartCount, container id, and
# StartedAt before recreate. A later proxy failure is pantry's only when
# all four still match.
record_container_baseline() {
  local state="$1" svc image line status restarts cid started
  : > "$state/pre-deploy.tsv"
  while IFS=$'\t' read -r svc image; do
    [[ -z "$svc" ]] && continue
    line=$(container_state "$svc" || true)
    status=${line%% *}
    restarts=${line##* }
    if [[ ! "$restarts" =~ ^[0-9]+$ ]]; then
      restarts=0
    fi
    [[ -n "$status" ]] || status=missing
    cid=$(container_id "$svc")
    started=$(container_started_at "$svc" || true)
    printf '%s\t%s\t%s\t%s\t%s\n' "$svc" "$status" "$restarts" "$cid" "$started" >> "$state/pre-deploy.tsv"
  done < "$state/refs.tsv"
}

pre_status() {
  awk -F '\t' -v s="$1" '$1 == s { print $2; exit }' "$2/pre-deploy.tsv"
}

# warn_unchanged logs a journal warning for a service whose image did not
# change. A remembered service is checked again next run. A sidecar leaves
# the list once it is running. A proxy leaves only after its probe passes.
warn_unchanged() {
  local svc="$1" detail="$2" mark="$PANTRY_DIR/.update-sidecar-warn" line remember="${3:-}"
  echo "<4>pantry update: warning: ${svc} ${detail}; not rolling back" >&2
  [[ "$remember" == 1 ]] || return 0
  if [[ -f "$mark" ]]; then
    while IFS= read -r line; do
      line=${line//$'\r'/}
      [[ "$line" == "$svc" ]] && return 0
    done < "$mark"
  fi
  printf '%s\n' "$svc" >> "$mark"
}

# proxy_marker_service is a compose service whose outage is a failed probe,
# not merely a stopped container.
proxy_marker_service() {
  [[ "$1" == caddy || "$1" == caddy-tunnel ]]
}

# proxy_probe_recovered is true when the marked proxy answers again.
# STATE is this run's directory, which already lists the pantry image.
# The tunnel probe reads that ref for the expected commit. A fresh empty
# directory would fail a healthy listener with "wanted commit <missing>".
# A skipped probe is not a pass, so the marker stays.
proxy_probe_recovered() {
  local state="$1" host
  host=$(publish_env_value PUBLIC_HOST)
  [[ -n "$host" ]] || return 1
  public_host_set || return 1
  [[ -n "$state" ]] || return 1
  probe_public_proxy "$state" || true
  [[ "$PROXY_PROBE_PASSED" -eq 1 ]]
}

# clear_proxy_marker drops one proxy line after its probe has passed.
clear_proxy_marker() {
  local svc="$1" mark="$PANTRY_DIR/.update-sidecar-warn" tmp line
  [[ -f "$mark" ]] || return 0
  tmp=$(mktemp) || return 0
  while IFS= read -r line || [[ -n "${line:-}" ]]; do
    line=${line//$'\r'/}
    [[ -z "$line" || "$line" == "$svc" ]] && continue
    printf '%s\n' "$line" >> "$tmp"
  done < "$mark"
  mv "$tmp" "$mark"
  [[ -s "$mark" ]] || rm -f "$mark"
}

# rewrite_sidecar_marker drops services that are not in this compose config.
# A non-proxy container that is running again is dropped. A proxy stays
# until its probe passes: a running Caddy that fails the probe is still
# the outage this file exists to keep visible. mode "warn" repeats the
# journal warning for whatever is still listed. mode "clear" only removes
# running non-proxy lines, which a deploy does before it checks containers.
rewrite_sidecar_marker() {
  local marker_mode="$1" state="$2" mark="$PANTRY_DIR/.update-sidecar-warn" tmp raw svc line status known config_ok=0
  [[ -f "$mark" ]] || return 0
  tmp=$(mktemp) || return 0
  if known=$(compose_service_images); then
    config_ok=1
    known=$(printf '%s\n' "$known" | awk -F '\t' '{ print $1 }')
  else
    known=""
  fi
  while IFS= read -r raw || [[ -n "${raw:-}" ]]; do
    svc=${raw//$'\r'/}
    [[ -z "${svc:-}" || "$svc" == \#* ]] && continue
    if [[ "$config_ok" -eq 1 ]] && ! grep -qx -F -- "$svc" <<<"$known"; then
      continue
    fi
    line=$(container_state "$svc" || true)
    status=${line%% *}
    if [[ "$status" == running ]]; then
      if proxy_marker_service "$svc"; then
        if [[ "$marker_mode" == warn ]] && proxy_probe_recovered "$state"; then
          echo "pantry update: ${svc} probe passed"
          continue
        fi
        if [[ "$marker_mode" == warn ]]; then
          echo "<4>pantry update: warning: ${svc} probe failed and its image did not change; not rolling back" >&2
        fi
        printf '%s\n' "$svc" >> "$tmp"
        continue
      fi
      echo "pantry update: ${svc} is running again"
      continue
    fi
    if [[ "$marker_mode" == warn ]]; then
      echo "<4>pantry update: warning: ${svc} is ${status:-unhealthy} and its image did not change; not rolling back" >&2
    fi
    printf '%s\n' "$svc" >> "$tmp"
  done < "$mark"
  mv "$tmp" "$mark"
  [[ -s "$mark" ]] || rm -f "$mark"
}

# recheck_sidecar_warnings repeats warnings for services still on the list.
# A running sidecar is dropped. A proxy is dropped only when its probe
# passes. It runs even when no image changed.
recheck_sidecar_warnings() {
  rewrite_sidecar_marker warn "$1"
}

# confirm_recreated checks changed services, then checks them again after a
# short settle. An unchanged service that is down or restarting is a warning.
# One sleep, capped at 15s.
confirm_recreated() {
  local state="$1" svc image line status restarts base failed=0 settle before
  : > "$state/restart-base.tsv"
  while IFS=$'\t' read -r svc image; do
    [[ -z "$svc" ]] && continue
    line=$(container_state "$svc" || true)
    status=${line%% *}
    restarts=${line##* }
    if [[ ! "$restarts" =~ ^[0-9]+$ ]]; then
      restarts=0
    fi
    if ! service_changed "$state" "$svc"; then
      if [[ "$status" != running ]]; then
        before=$(pre_status "$svc" "$state")
        if [[ "$before" == running ]]; then
          warn_unchanged "$svc" "is ${status:-unhealthy} and its image did not change" 1
        else
          warn_unchanged "$svc" "was already ${status:-unhealthy} and its image did not change" 1
        fi
      elif [[ -n "$status" ]]; then
        printf '%s\t%s\n' "$svc" "$restarts" >> "$state/restart-base.tsv"
      fi
      continue
    fi
    if [[ "$status" != running ]]; then
      echo "pantry update: ${svc} is not running (${status:-missing})" >&2
      note_failed "$svc"
      failed=1
      continue
    fi
    printf '%s\t%s\n' "$svc" "$restarts" >> "$state/restart-base.tsv"
  done < "$state/refs.tsv"
  [[ "$failed" -eq 0 ]] || return 1

  settle=${PANTRY_UPDATE_SETTLE_SECONDS:-12}
  if [[ ! "$settle" =~ ^[0-9]+$ ]]; then
    settle=12
  fi
  if [[ "$settle" -gt 15 ]]; then
    settle=15
  fi
  if [[ "$settle" -gt 0 ]]; then
    echo "pantry update: watching containers for ${settle}s" >&2
    sleep "$settle"
  fi

  while IFS=$'\t' read -r svc image; do
    [[ -z "$svc" ]] && continue
    line=$(container_state "$svc" || true)
    status=${line%% *}
    restarts=${line##* }
    if [[ ! "$restarts" =~ ^[0-9]+$ ]]; then
      restarts=0
    fi
    if ! service_changed "$state" "$svc"; then
      if [[ "$status" != running ]]; then
        continue
      fi
      base=$(awk -F '\t' -v s="$svc" '$1 == s { print $2; exit }' "$state/restart-base.tsv")
      if [[ "$base" =~ ^[0-9]+$ && "$restarts" -gt "$base" ]]; then
        warn_unchanged "$svc" "restarted during settle (${base} -> ${restarts}) but its image did not change"
      fi
      continue
    fi
    if [[ "$status" != running ]]; then
      echo "pantry update: ${svc} is not running (${status:-missing})" >&2
      note_failed "$svc"
      failed=1
      continue
    fi
    base=$(awk -F '\t' -v s="$svc" '$1 == s { print $2; exit }' "$state/restart-base.tsv")
    if [[ "$base" =~ ^[0-9]+$ && "$restarts" -gt "$base" ]]; then
      echo "pantry update: ${svc} restarted (${base} -> ${restarts})" >&2
      note_failed "$svc"
      failed=1
    fi
  done < "$state/refs.tsv"
  [[ "$failed" -eq 0 ]]
}

note_stopped_services() {
  local state="$1" svc image line status now pulled
  while IFS=$'\t' read -r svc image; do
    [[ -z "$svc" ]] && continue
    line=$(container_state "$svc" || true)
    status=${line%% *}
    if [[ "$status" == running ]]; then
      if service_changed "$state" "$svc"; then
        now=$(running_image_id "$svc")
        pulled=$(service_pulled_id "$svc" "$state" || true)
        if [[ -z "$pulled" || "$now" != "$pulled" ]]; then
          echo "pantry update: ${svc} is still running the previous image" >&2
          note_failed "$svc"
        fi
      fi
      continue
    fi
    if ! service_changed "$state" "$svc"; then
      warn_unchanged "$svc" "is ${status:-unhealthy} and its image did not change" 1
      continue
    fi
    note_failed "$svc"
  done < "$state/refs.tsv"
}

public_host_set() {
  [[ -n "$(publish_env_value PUBLIC_HOST)" ]]
}

# proxy_compose_service is the compose service that publishes the hostname.
# Tunnel mode's service is caddy-tunnel. The certificate path's is caddy.
proxy_compose_service() {
  if [[ "$mode" == tunnel ]]; then
    printf 'caddy-tunnel'
    return 0
  fi
  printf 'caddy'
}

# tunnel_connect_failed is true for curl exits that never reached Caddy:
# resolve failure, connection refused, timeout, and empty reply.
tunnel_connect_failed() {
  case "$1" in
    6|7|28|52) return 0 ;;
    *) return 1 ;;
  esac
}

# tunnel_probe_curl requests http://$PUBLIC_HOST/api/build with that name
# resolved to caddy-tunnel's pinned address. The body is written to DEST
# in this shell, and TUNNEL_PROBE_RC is curl's exit code. A command
# substitution would drop that exit code. PUBLIC_HOST is the value compose
# interpolates into Caddyfile.tunnel.
tunnel_probe_curl() {
  local dest="$1" host ip="10.77.77.3" code
  TUNNEL_PROBE_RC=0
  TUNNEL_HTTP_CODE=""
  : > "$dest"
  host=$(publish_env_value PUBLIC_HOST)
  if [[ -z "$host" ]]; then
    TUNNEL_PROBE_RC=6
    return 0
  fi
  code=$(curl -sS -o "$dest" -w '%{http_code}' --max-time 5 --resolve "${host}:80:${ip}" "http://${host}/api/build" 2>"${dest}.err") || TUNNEL_PROBE_RC=$?
  TUNNEL_HTTP_CODE=$(one_line "$code")
  if [[ -s "${dest}.err" ]]; then
    cat "${dest}.err" >&2
  fi
}

# tunnel_http_ok is true when curl returned a 2xx response.
tunnel_http_ok() {
  [[ "$TUNNEL_PROBE_RC" -eq 0 ]] || return 1
  [[ "$TUNNEL_HTTP_CODE" =~ ^2[0-9][0-9]$ ]]
}

# remember_tunnel_reachability probes the stack that is still running,
# before recreate. A later refusal or hang is ignored only when this
# probe could not connect either. The proxy counts as passed only when
# the reply is 2xx and its commit is the running pantry revision.
remember_tunnel_reachability() {
  local state="$1" body commit rev id
  TUNNEL_REACHABLE=0
  [[ "$mode" == tunnel ]] || return 0
  public_host_set || return 0
  [[ -n "$(publish_env_value PUBLIC_HOST)" ]] || return 0
  echo "pantry update: checking whether caddy-tunnel was reachable before recreate"
  tunnel_probe_curl "$state/tunnel-pre.body"
  if ! tunnel_connect_failed "$TUNNEL_PROBE_RC"; then
    TUNNEL_REACHABLE=1
  fi
  body=$(<"$state/tunnel-pre.body")
  id=$(running_field pantry 2 "$state")
  rev=""
  if [[ -n "$id" ]]; then
    rev=$(image_revision "$id" || true)
  fi
  commit=$(json_string_field "$body" commit)
  if tunnel_http_ok && [[ -n "$rev" && "$commit" == "$rev" ]]; then
    PROXY_REACHABLE=1
  fi
}

# remember_acme_reachability is the certificate-mode form of the same
# pre-deploy probe. HTTPS to PUBLIC_HOST, resolved to this host.
remember_acme_reachability() {
  local host rc=0
  [[ "$mode" == acme ]] || return 0
  public_host_set || return 0
  host=$(publish_env_value PUBLIC_HOST)
  [[ -n "$host" ]] || return 0
  echo "pantry update: checking whether Caddy was reachable before recreate"
  curl -sf --max-time 10 --resolve "${host}:443:127.0.0.1" "https://${host}/api/build" >/dev/null 2>/dev/null || rc=$?
  if [[ "$rc" -eq 0 ]]; then
    PROXY_REACHABLE=1
  fi
}

# probe_tunnel_listener checks the new stack through caddy-tunnel.
# The Host header is PUBLIC_HOST, because that is the only site in
# Caddyfile.tunnel. The commit has to match the pantry image label.
# Connect failures are skipped only when the pre-deploy probe failed too.
probe_tunnel_listener() {
  local state="$1" host ip="10.77.77.3" body commit revision ref
  host=$(publish_env_value PUBLIC_HOST)
  [[ -n "$host" ]] || return 0
  echo "pantry update: checking http://${host}/api/build through caddy-tunnel at ${ip}"
  tunnel_probe_curl "$state/tunnel-post.body"
  body=$(<"$state/tunnel-post.body")
  if tunnel_connect_failed "$TUNNEL_PROBE_RC"; then
    if [[ "$TUNNEL_REACHABLE" -ne 1 ]]; then
      echo "pantry update: caddy-tunnel internal listener at ${ip}:80 is not reachable from this host; skipped. The pre-deploy probe could not reach it either. Cloudflare uses http://caddy:80 on the cf-tunnel network, which is not published on the Pi." >&2
      return 0
    fi
    echo "pantry update: caddy-tunnel probe of http://${host}/api/build failed (curl ${TUNNEL_PROBE_RC})" >&2
    return 1
  fi
  if [[ "$TUNNEL_PROBE_RC" -ne 0 ]]; then
    echo "pantry update: caddy-tunnel probe of http://${host}/api/build failed (curl ${TUNNEL_PROBE_RC})" >&2
    return 1
  fi
  if ! tunnel_http_ok; then
    echo "pantry update: caddy-tunnel probe of http://${host}/api/build failed (HTTP ${TUNNEL_HTTP_CODE:-<none>})" >&2
    return 1
  fi
  commit=$(json_string_field "$body" commit)
  ref=$(service_ref pantry "$state")
  revision=""
  if [[ -n "$ref" ]]; then
    revision=$(image_revision "$ref" || true)
  fi
  if [[ -z "$revision" || "$commit" != "$revision" ]]; then
    echo "pantry update: caddy-tunnel probe of http://${host}/api/build failed (wanted commit ${revision:-<missing>}, got ${commit:-<none>})" >&2
    return 1
  fi
  PROXY_PROBE_PASSED=1
}

probe_caddy() {
  local state="$1" host
  host=$(publish_env_value PUBLIC_HOST)
  [[ -n "$host" ]] || return 0
  public_host_set || return 0
  if [[ "$mode" == tunnel ]]; then
    probe_tunnel_listener "$state"
    return
  fi
  [[ "$mode" == acme ]] || return 0
  echo "pantry update: checking https://${host}/api/build through Caddy"
  if curl -sf --max-time 10 --resolve "${host}:443:127.0.0.1" "https://${host}/api/build" >/dev/null; then
    PROXY_PROBE_PASSED=1
    return 0
  fi
  return 1
}

# probe_public_proxy checks the proxy, then once more after a short delay.
# One transient failure must not reject a pantry image that just passed.
# The delay is capped like the settle window, so a bad unit file cannot
# stall the timer for the rest of the minute.
probe_public_proxy() {
  local state="$1" delay
  PROXY_PROBE_PASSED=0
  if probe_caddy "$state"; then
    return 0
  fi
  delay=${PANTRY_UPDATE_PROBE_RETRY_SECONDS:-3}
  if [[ ! "$delay" =~ ^[0-9]+$ ]]; then
    delay=3
  fi
  if [[ "$delay" -gt 15 ]]; then
    delay=15
  fi
  echo "pantry update: proxy probe failed; retrying in ${delay}s" >&2
  if [[ "$delay" -gt 0 ]]; then
    sleep "$delay"
  fi
  probe_caddy "$state"
}

clear_state() {
  if [[ -n "${UPDATE_STATE:-}" ]]; then
    rm -rf "$UPDATE_STATE"
    UPDATE_STATE=""
  fi
}

finish_rollback() {
  local state="$1" pins="$2" rc=0
  rollback_to_pins "$state" "$pins" || rc=$?
  if [[ "$rc" -eq 0 || "$rc" -eq 2 ]]; then
    # Record while the local tag still names the image that failed. Retagging
    # first would make that digest look like the one already running.
    if [[ "$pins" == "$PANTRY_DIR/.update-pending" ]]; then
      if ! record_pending_rejections; then
        echo "PANTRY UPDATE FAILED: could not record a rejected digest; the next run may try that image again" >&2
      fi
      if ! retag_pins "$pins" "$state"; then
        echo "pantry update: the previous container is running, but the local tag still names the rejected image. A manual docker compose up would start it." >&2
      fi
    else
      if ! record_changed_rejections "$state"; then
        echo "PANTRY UPDATE FAILED: could not record a rejected digest; the next run may try that image again" >&2
      fi
      if ! restore_local_tags "$state"; then
        echo "pantry update: the previous container is running, but the local tag still names the rejected image. A manual docker compose up would start it." >&2
      fi
    fi
    rm -f "$PANTRY_DIR/.update-pending"
  else
    echo "pantry update: previous image did not start; the next run will try this image again" >&2
  fi
}

# resume_pending_deploy checks the digest named in .update-pending.
# Returns 0 when the script should continue, and exits on a failed resume.
resume_pending_deploy() {
  local state="$1" desired rev
  if ! deploy_was_started "$state"; then
    echo "pantry update: pending marker was left before the new image started"
    rm -f "$PANTRY_DIR/.update-pending"
    return 0
  fi
  desired=$(awk -F '\t' '$1 == "pantry" { print $3; exit }' "$PANTRY_DIR/.update-pending")
  if [[ -z "$desired" ]]; then
    echo "pantry update: pending marker has no pantry digest; skipping the resume check" >&2
    rm -f "$PANTRY_DIR/.update-pending"
    return 0
  fi
  if ! docker image inspect "$desired" >/dev/null 2>&1; then
    echo "pantry update: pending digest ${desired} is not on this machine; skipping the resume check" >&2
    rm -f "$PANTRY_DIR/.update-pending"
    return 0
  fi
  rev=$(image_revision "$desired" || true)
  record_container_baseline "$state"
  echo "pantry update: previous deploy was not confirmed; checking health"
  PANTRY_VERIFIED=0
  FAILED_SERVICES=""
  if [[ -z "$rev" ]]; then
    echo "pantry update: pending digest ${desired} has no revision label" >&2
    echo "pantry update: pending deploy failed the health check" >&2
    finish_rollback "$state" "$PANTRY_DIR/.update-pending"
    clear_state
    exit 1
  fi
  if wait_for_expected_build "$state" "$rev"; then
    PANTRY_VERIFIED=1
    if confirm_recreated "$state"; then
      rm -f "$PANTRY_DIR/.update-pending"
      echo "pantry update: pending deploy is healthy"
      PANTRY_VERIFIED=0
      FAILED_SERVICES=""
      return 0
    fi
  fi
  echo "pantry update: pending deploy failed the health check" >&2
  finish_rollback "$state" "$PANTRY_DIR/.update-pending"
  clear_state
  exit 1
}

HOST_BUNDLE_CONTAINER_PATH=/usr/share/stockbot/host-bundle

deploy_status_path() {
  printf '%s\n' "$PANTRY_DIR/last-deploy-status"
}

# write_deploy_status RESULT REASON [IMAGE_REVISION]
write_deploy_status() {
  local result="$1" reason="$2" revision="${3:-}" dest tmp ts
  dest=$(deploy_status_path)
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || true)
  [[ -n "$ts" ]] || ts=unknown
  tmp=$(mktemp "${dest}.XXXXXX") || {
    echo "pantry update: could not write ${dest}" >&2
    return 1
  }
  {
    printf 'result=%s\n' "$result"
    printf 'reason=%s\n' "$reason"
    printf 'timestamp=%s\n' "$ts"
    printf 'image_revision=%s\n' "$revision"
  } > "$tmp"
  mv "$tmp" "$dest"
}

host_bundle_rel_paths() {
  cat <<'EOF'
publish-mode.sh
runtime-setup.sh
vapid-env.sh
systemd/pantry-update.sh
systemd/pantry-backup.sh
systemd/pantry-update.service
systemd/pantry-update.timer
systemd/pantry-update.path
systemd/pantry-backup.service
systemd/pantry-backup.timer
EOF
}

# atomic_install_file SRC DEST sets execute bit when DEST ends in .sh.
atomic_install_file() {
  local src="$1" dest="$2" tmp
  if [[ -f "$dest" ]] && cmp -s "$src" "$dest"; then
    return 0
  fi
  tmp=$(mktemp "${dest}.refresh.XXXXXX") || return 1
  cp "$src" "$tmp" || {
    rm -f "$tmp"
    return 1
  }
  if [[ "$dest" == *.sh ]]; then
    chmod +x "$tmp" || {
      rm -f "$tmp"
      return 1
    }
  fi
  mv "$tmp" "$dest"
}

systemd_unit_dir() {
  printf '%s\n' "${PANTRY_SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
}

host_scripts_last_known_good_dir() {
  printf '%s\n' "$PANTRY_DIR/host-scripts-last-known-good"
}

# host_bundle_install_plan lists SRC and DEST pairs to install from STAGED.
# Systemd unit paths are included when those units are already installed.
host_bundle_install_plan() {
  local staged="$1" rel src dest unit_dir
  while IFS= read -r rel || [[ -n "${rel:-}" ]]; do
    [[ -z "${rel:-}" ]] && continue
    src="$staged/$rel"
    [[ -f "$src" ]] || continue
    dest="$PANTRY_DIR/$rel"
    printf '%s\t%s\n' "$src" "$dest"
  done < <(host_bundle_rel_paths)
  unit_dir=$(systemd_unit_dir)
  if [[ -f "$unit_dir/pantry-update.service" || -f "$unit_dir/pantry-update.timer" ]]; then
    for rel in \
      systemd/pantry-update.service \
      systemd/pantry-update.timer \
      systemd/pantry-update.path
    do
      src="$staged/$rel"
      [[ -f "$src" ]] || continue
      printf '%s\t%s\n' "$src" "$unit_dir/$(basename "$rel")"
    done
  fi
  if [[ -f "$unit_dir/pantry-backup.timer" ]]; then
    for rel in systemd/pantry-backup.service systemd/pantry-backup.timer; do
      src="$staged/$rel"
      [[ -f "$src" ]] || continue
      printf '%s\t%s\n' "$src" "$unit_dir/$(basename "$rel")"
    done
  fi
}

verify_host_bundle_staged() {
  local staged="$1" rel src missing=0
  while IFS= read -r rel || [[ -n "${rel:-}" ]]; do
    [[ -z "${rel:-}" ]] && continue
    src="$staged/$rel"
    if [[ ! -f "$src" ]]; then
      echo "pantry update: host bundle from the image is missing ${rel}" >&2
      missing=1
    fi
  done < <(host_bundle_rel_paths)
  [[ "$missing" -eq 0 ]]
}

snapshot_host_scripts_last_known_good() {
  local lkg rel src dest unit_dir
  lkg=$(host_scripts_last_known_good_dir)
  rm -rf "$lkg"
  mkdir -p "$lkg/systemd" "$lkg/systemd-units"
  while IFS= read -r rel || [[ -n "${rel:-}" ]]; do
    [[ -z "${rel:-}" ]] && continue
    dest="$PANTRY_DIR/$rel"
    [[ -f "$dest" ]] || continue
    src="$lkg/$rel"
    mkdir -p "$(dirname "$src")"
    if ! cp -a "$dest" "$src"; then
      echo "pantry update: could not snapshot ${rel} before host script refresh" >&2
      return 1
    fi
  done < <(host_bundle_rel_paths)
  unit_dir=$(systemd_unit_dir)
  for rel in \
    pantry-update.service pantry-update.timer pantry-update.path \
    pantry-backup.service pantry-backup.timer
  do
    [[ -f "$unit_dir/$rel" ]] || continue
    if ! cp -a "$unit_dir/$rel" "$lkg/systemd-units/$rel"; then
      echo "pantry update: could not snapshot ${rel} under ${unit_dir} before host script refresh" >&2
      return 1
    fi
  done
}

host_bundle_cleanup_refresh_temps() {
  local entry tmp dest unit_dir
  for entry in "${HOST_BUNDLE_INSTALL_PENDING[@]:-}"; do
    [[ -z "${entry:-}" ]] && continue
    tmp=${entry%%|*}
    rm -f "$tmp"
  done
  find "$PANTRY_DIR" -name '*.refresh.*' 2>/dev/null | while IFS= read -r f; do
    [[ -n "$f" ]] && rm -f "$f"
  done
  unit_dir=$(systemd_unit_dir)
  find "$unit_dir" -maxdepth 1 -name '*.refresh.*' 2>/dev/null | while IFS= read -r f; do
    [[ -n "$f" ]] && rm -f "$f"
  done
}

restore_host_scripts_from_last_known_good() {
  local lkg rel src dest unit_dir
  lkg=$(host_scripts_last_known_good_dir)
  [[ -d "$lkg" ]] || return 1
  while IFS= read -r rel || [[ -n "${rel:-}" ]]; do
    [[ -z "${rel:-}" ]] && continue
    dest="$PANTRY_DIR/$rel"
    src="$lkg/$rel"
    if [[ -f "$src" ]]; then
      mkdir -p "$(dirname "$dest")"
      cp -a "$src" "$dest" || return 1
    elif [[ -f "$dest" && -z "${HOST_BUNDLE_HAD_DEST[$dest]:-}" ]]; then
      rm -f "$dest"
    fi
  done < <(host_bundle_rel_paths)
  unit_dir=$(systemd_unit_dir)
  for rel in \
    pantry-update.service pantry-update.timer pantry-update.path \
    pantry-backup.service pantry-backup.timer
  do
    dest="$unit_dir/$rel"
    src="$lkg/systemd-units/$rel"
    if [[ -f "$src" ]]; then
      cp -a "$src" "$dest" || return 1
    elif [[ -f "$dest" && -z "${HOST_BUNDLE_HAD_DEST[$dest]:-}" ]]; then
      rm -f "$dest"
    fi
  done
  if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
  fi
}

# install_host_bundle_tree_all_or_nothing stages every file, verifies the set,
# snapshots the previous host scripts, then swaps atomically per file.
install_host_bundle_tree_all_or_nothing() {
  local staged="$1" tmp src dest copied_units=0 unit_dir entry
  HOST_BUNDLE_INSTALL_PENDING=()
  declare -gA HOST_BUNDLE_HAD_DEST=()
  if ! verify_host_bundle_staged "$staged"; then
    return 1
  fi
  while IFS=$'\t' read -r src dest || [[ -n "${src:-}" ]]; do
    [[ -z "${src:-}" ]] && continue
    if [[ -f "$dest" ]]; then
      HOST_BUNDLE_HAD_DEST[$dest]=1
    fi
    if [[ -f "$dest" ]] && cmp -s "$src" "$dest"; then
      continue
    fi
    tmp=$(mktemp "${dest}.refresh.XXXXXX") || return 1
    if ! cp "$src" "$tmp"; then
      rm -f "$tmp"
      host_bundle_cleanup_refresh_temps
      return 1
    fi
    if [[ "$dest" == *.sh ]]; then
      if ! chmod +x "$tmp"; then
        rm -f "$tmp"
        host_bundle_cleanup_refresh_temps
        return 1
      fi
    fi
    HOST_BUNDLE_INSTALL_PENDING+=("$tmp|$dest")
  done < <(host_bundle_install_plan "$staged")
  if ! snapshot_host_scripts_last_known_good; then
    host_bundle_cleanup_refresh_temps
    return 1
  fi
  for entry in "${HOST_BUNDLE_INSTALL_PENDING[@]}"; do
    tmp=${entry%%|*}
    dest=${entry#*|}
    if ! command mv "$tmp" "$dest"; then
      echo "pantry update: could not install $(basename "$dest") from the image host bundle" >&2
      restore_host_scripts_from_last_known_good || true
      host_bundle_cleanup_refresh_temps
      return 1
    fi
    case "$dest" in
      *pantry-update.service|*pantry-update.timer|*pantry-update.path|*pantry-backup.service|*pantry-backup.timer)
        copied_units=1 ;;
    esac
  done
  if [[ "$copied_units" -eq 1 ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
  fi
  return 0
}

HOST_BUNDLE_GITHUB_REPO=Rhionin/stockbot
HOST_BUNDLE_GITHUB_BRANCH=master

host_bundle_github_compare_url() {
  local revision="$1"
  printf 'https://api.github.com/repos/%s/compare/%s...%s' \
    "$HOST_BUNDLE_GITHUB_REPO" "$revision" "$HOST_BUNDLE_GITHUB_BRANCH"
}

# github_compare_top_status prints the top-level status field from a GitHub
# compare API body. Without jq, flatten newlines and take the first "status"
# key (top-level precedes files[] in GitHub's JSON).
github_compare_top_status() {
  local body="$1" status flattened match
  if command -v jq >/dev/null 2>&1; then
    status=$(printf '%s' "$body" | jq -r '.status // empty' 2>/dev/null) || status=
    printf '%s' "$status"
    return 0
  fi
  flattened=$(printf '%s' "$body" | tr -d '\n\r')
  match=$(printf '%s' "$flattened" | grep -o '"status"[[:space:]]*:[[:space:]]*"[a-z_]*"' | head -n1)
  if [[ -n "$match" ]]; then
    status=$(printf '%s' "$match" | sed 's/"status"[[:space:]]*:[[:space:]]*"\([a-z_]*\)"/\1/')
  else
    status=
  fi
  printf '%s' "$status"
}

# host_bundle_revision_on_master uses the public GitHub compare API. Returns 0
# when REVISION is an ancestor of master (status ahead or identical), 1 when
# it is not, and 2 when GitHub could not be reached.
host_bundle_revision_on_master() {
  local revision="$1" url body status
  [[ -n "$revision" ]] || return 1
  if [[ -n "${PANTRY_TEST_GITHUB_COMPARE_STATUS:-}" ]]; then
    case "$PANTRY_TEST_GITHUB_COMPARE_STATUS" in
      ahead|identical) return 0 ;;
      unreachable) return 2 ;;
      *) return 1 ;;
    esac
  fi
  url=$(host_bundle_github_compare_url "$revision")
  body=$(curl -fsS --max-time 15 "$url" 2>/dev/null) || return 2
  status=$(github_compare_top_status "$body")
  case "$status" in
    ahead|identical) return 0 ;;
    *) return 1 ;;
  esac
}

host_bundle_running_digest_matches() {
  local state="$1" expected running_id ref running_dig
  expected=$(service_desired_digest "$state" pantry)
  running_id=$(running_image_id pantry)
  ref=$(service_ref pantry "$state")
  running_dig=$(image_repo_digest "$running_id" "$ref")
  [[ -n "$expected" && -n "$running_dig" && "$expected" == "$running_dig" ]]
}

# host_bundle_provenance_ok gates host refresh. Returns 0 when the running
# pantry image matches the deployed digest and its revision is on master.
host_bundle_provenance_ok() {
  local state="$1" revision rc
  if [[ -n "${PANTRY_TEST_SKIP_BUNDLE_PROVENANCE:-}" ]]; then
    return 0
  fi
  if ! host_bundle_running_digest_matches "$state"; then
    echo "pantry update: host script refresh skipped: running pantry digest does not match the deployed image" >&2
    return 1
  fi
  revision=$(pantry_deploy_revision "$state")
  host_bundle_revision_on_master "$revision"
  rc=$?
  if [[ "$rc" -eq 2 ]]; then
    echo "pantry update: host script refresh skipped: could not reach GitHub to verify image revision" >&2
    return 2
  fi
  if [[ "$rc" -ne 0 ]]; then
    echo "pantry update: host script refresh skipped: image revision is not on origin/master" >&2
    return 1
  fi
  return 0
}

# refresh_host_scripts_from_image copies the bundle out of the running pantry
# container after a successful deploy. Returns non-zero when refresh fails.
refresh_host_scripts_from_image() {
  local state="$1" cid tmp staged prov_rc
  if [[ "${HOST_STATE_BLOCKED:-0}" -eq 1 ]]; then
    return "$HOST_TXN_FATAL_RC"
  fi
  if host_should_freeze_writes; then
    if [[ "${HOST_FLAG_ANY_INVALID:-0}" -eq 1 ]]; then
      return "$HOST_TXN_FATAL_RC"
    fi
    return 0
  fi
  if [[ "${HOST_FLAG_HOST_REFRESH:-allow}" == deny ]]; then
    return 0
  fi
  if [[ "${HOST_STATE_INITIALIZED:-0}" -eq 0 && "${HOST_FLAG_AUTO_SETUP:-off}" == on ]]; then
    echo "pantry update: PANTRY_AUTO_SETUP requires approved initialization before host transactions" >&2
    return "$HOST_TXN_FATAL_RC"
  fi
  prov_rc=0
  host_bundle_provenance_ok "$state" || prov_rc=$?
  if [[ "$prov_rc" -ne 0 ]]; then
    return "$prov_rc"
  fi
  if [[ "${HOST_STATE_INITIALIZED:-0}" -eq 1 ]]; then
    if [[ "${HOST_FLAG_AUTO_SETUP:-off}" != on ]]; then
      return 0
    fi
    host_subset_refresh_from_image "$state"
    return $?
  fi
  cid=$(container_id pantry)
  if [[ -z "$cid" ]]; then
    echo "pantry update: host script refresh failed: pantry container id is missing" >&2
    return 1
  fi
  tmp=$(mktemp -d) || {
    echo "pantry update: host script refresh failed: temp directory failed" >&2
    return 1
  }
  staged="$tmp/bundle"
  mkdir -p "$staged"
  if ! docker cp "${cid}:${HOST_BUNDLE_CONTAINER_PATH}/." "$staged"; then
    rm -rf "$tmp"
    echo "pantry update: host script refresh failed: could not read host scripts from the pantry container" >&2
    return 1
  fi
  if ! install_host_bundle_tree_all_or_nothing "$staged"; then
    rm -rf "$tmp"
    echo "pantry update: host script refresh failed" >&2
    return 1
  fi
  rm -rf "$tmp"
  echo "pantry update: refreshed host deploy scripts from the running image"
  return 0
}

pantry_deploy_revision() {
  local state="$1" ref rev=""
  ref=$(service_ref pantry "$state")
  if [[ -n "$ref" ]]; then
    rev=$(image_revision "$ref" || true)
  fi
  printf '%s' "$rev"
}

# backup_before_deploy_mode is required, warn, or off. An empty setting
# is required. optional is the same as warn. The environment wins over .env.
backup_before_deploy_mode() {
  local mode="${BACKUP_BEFORE_DEPLOY:-}"
  if [[ -z "$mode" ]]; then
    mode=$(publish_env_value BACKUP_BEFORE_DEPLOY)
  fi
  mode=$(printf '%s' "$mode" | tr '[:upper:]' '[:lower:]')
  if [[ -z "$mode" ]]; then
    mode=required
  fi
  case "$mode" in
    optional) mode=warn ;;
  esac
  case "$mode" in
    warn|required|off)
      printf '%s' "$mode"
      ;;
    *)
      echo "pantry update: BACKUP_BEFORE_DEPLOY must be required, warn, optional, or off" >&2
      return 1
      ;;
  esac
}

# pre_deploy_backup runs pantry-backup.sh pre-deploy when the pantry image
# is about to change. The deploy lock is already held on fd 9. The child
# is started with that fd closed and PANTRY_BACKUP_LOCK_HELD=1 so it does
# not flock the same file. Returns 0 when the deploy may continue.
pre_deploy_backup() {
  local state="$1" mode script rc revision
  mode=$(backup_before_deploy_mode) || return 1
  if [[ "$mode" == off ]]; then
    return 0
  fi
  service_changed "$state" pantry || return 0
  script="$(cd "$(dirname "$0")" && pwd)/pantry-backup.sh"
  revision=$(pantry_deploy_revision "$state")
  echo "pantry update: pre-deploy backup (${mode})"
  if [[ ! -f "$script" || ! -x "$script" ]]; then
    echo "pantry update: pre-deploy backup script is missing or not executable" >&2
    if [[ "$mode" == required ]]; then
      write_deploy_status blocked pre_deploy_backup_failed "$revision" || true
      echo "PANTRY UPDATE BLOCKED: pre-deploy backup failed; leaving the running pantry container unchanged" >&2
      return 1
    fi
    echo "<4>pantry update: warning: pre-deploy backup failed; continuing because BACKUP_BEFORE_DEPLOY=${mode}" >&2
    return 0
  fi
  rc=0
  PANTRY_BACKUP_LOCK_HELD=1 "$script" pre-deploy 9>&- || rc=$?
  if [[ "$rc" -eq 0 ]]; then
    echo "pantry update: pre-deploy backup finished"
    return 0
  fi
  if [[ "$mode" == required ]]; then
    write_deploy_status blocked pre_deploy_backup_failed "$revision" || true
    echo "PANTRY UPDATE BLOCKED: pre-deploy backup failed; leaving the running pantry container unchanged" >&2
    return 1
  fi
  echo "<4>pantry update: warning: pre-deploy backup failed; continuing because BACKUP_BEFORE_DEPLOY=${mode}" >&2
  return 0
}

apply_compose() {
  local state up_ok ref pins desired containers_ok caddy_ok proxy revision
  PANTRY_VERIFIED=0
  FAILED_SERVICES=""
  TUNNEL_REACHABLE=0
  PROXY_REACHABLE=0
  PROXY_PROBE_PASSED=0
  TUNNEL_PROBE_RC=0
  UPDATE_STATE=$(mktemp -d) || {
    echo "PANTRY UPDATE FAILED: could not create a temp directory" >&2
    return 1
  }
  state=$UPDATE_STATE
  if ! record_running_images "$state"; then
    clear_state
    return 1
  fi

  "${compose[@]}" pull

  if [[ -f "$PANTRY_DIR/.update-pending" ]]; then
    resume_pending_deploy "$state"
  fi

  if images_unchanged "$state"; then
    recheck_sidecar_warnings "$state"
    echo "pantry update: images unchanged"
    clear_state
    return 0
  fi

  if ! park_rejected_services "$state"; then
    echo "pantry update: could not point a rejected image tag back at the previous digest." >&2
  fi
  if images_unchanged "$state"; then
    recheck_sidecar_warnings "$state"
    clear_state
    return 0
  fi

  ref=$(service_ref pantry "$state")
  if ! image_revision "$ref" >/dev/null; then
    echo "pantry update: ${ref} has no org.opencontainers.image.revision label; not starting it" >&2
    if service_changed "$state" pantry; then
      desired=$(service_desired_digest "$state" pantry)
      if ! remember_rejected_digest pantry "$desired"; then
        echo "PANTRY UPDATE FAILED: could not record the rejected digest ${desired}" >&2
      fi
    fi
    if ! restore_local_tags "$state"; then
      echo "pantry update: could not point the local image tag back at the previous digest." >&2
    fi
    clear_state
    exit 1
  fi

  if ! ensure_free_space; then
    clear_state
    exit 1
  fi
  if ! pre_deploy_backup "$state"; then
    if ! restore_local_tags "$state"; then
      echo "pantry update: could not point the local image tag back at the previous digest." >&2
    fi
    clear_state
    exit 1
  fi
  if ! write_pending "$state"; then
    clear_state
    exit 1
  fi

  # After a successful pull, drop a container left from the other mode so
  # the proxy name can be recreated on this path.
  if [[ "$mode" == tunnel ]]; then
    if [[ -n "$(publish_env_value PUBLIC_HOST)" ]]; then
      drop_other_publish_containers "$mode"
      drop_stale_cloudflared
    fi
  else
    drop_other_publish_containers "$mode"
  fi
  record_container_baseline "$state"
  remember_tunnel_reachability "$state"
  remember_acme_reachability
  up_ok=0
  if "${compose[@]}" up -d; then
    up_ok=1
  fi
  rewrite_sidecar_marker clear "$state"
  PANTRY_VERIFIED=0
  FAILED_SERVICES=""
  containers_ok=0
  caddy_ok=0
  if [[ "$up_ok" -eq 1 ]]; then
    if wait_for_expected_build "$state"; then
      PANTRY_VERIFIED=1
      if confirm_recreated "$state"; then
        containers_ok=1
      fi
    fi
  else
    echo "pantry update: docker compose up failed" >&2
    # One poll. The health timeout must not add another 90s after compose
    # has already said the recreate failed. The exit code is not the
    # verdict: changed services that are healthy stay up.
    if PANTRY_UPDATE_HEALTH_TIMEOUT=0 wait_for_expected_build "$state"; then
      PANTRY_VERIFIED=1
    fi
    note_stopped_services "$state"
    if [[ "$PANTRY_VERIFIED" -eq 1 ]] && ! changed_failure_named; then
      containers_ok=1
    fi
  fi
  if [[ "$PANTRY_VERIFIED" -eq 1 && "$containers_ok" -eq 1 ]]; then
    if probe_public_proxy "$state"; then
      caddy_ok=1
      if [[ "$PROXY_PROBE_PASSED" -eq 1 ]]; then
        clear_proxy_marker "$(proxy_compose_service)"
      fi
    else
      proxy=$(proxy_compose_service)
      if service_changed "$state" "$proxy"; then
        note_failed "$proxy"
      elif [[ "$PROXY_REACHABLE" -eq 1 ]] && service_changed "$state" pantry && proxy_container_steady "$state" "$proxy"; then
        echo "<4>pantry update: warning: ${proxy} was reachable before recreate and failed after; rejecting pantry" >&2
        note_failed pantry
      else
        warn_unchanged "$proxy" "probe failed and its image did not change" 1
        caddy_ok=1
      fi
    fi
  fi
  if [[ "$PANTRY_VERIFIED" -eq 1 && "$containers_ok" -eq 1 && "$caddy_ok" -eq 1 ]]; then
    if [[ "$up_ok" -eq 0 ]]; then
      echo "<4>pantry update: warning: docker compose up failed, but every changed service is healthy; not rolling back" >&2
    fi
    rm -f "$PANTRY_DIR/.update-pending"
    revision=$(pantry_deploy_revision "$state")
    local refresh_rc=0
    refresh_host_scripts_from_image "$state" || refresh_rc=$?
    if [[ "$refresh_rc" -eq 0 ]]; then
      write_deploy_status success deploy_ok "$revision" || true
      clear_state
      return 0
    fi
    if [[ "$refresh_rc" -eq "$HOST_TXN_FATAL_RC" ]]; then
      write_deploy_status blocked host_setup_failed "$revision" || true
      clear_state
      return "$refresh_rc"
    fi
    write_deploy_status success host_refresh_failed "$revision" || true
    clear_state
    return 0
  fi
  pins=$(mktemp) || {
    echo "PANTRY UPDATE FAILED: could not create a rollback pin file" >&2
    log_both_down
    clear_state
    exit 1
  }
  pins_from_running "$state" "$pins"
  finish_rollback "$state" "$pins"
  rm -f "$pins"
  clear_state
  exit 1
}

pantry_update_main() {
  local mode_rc round=0 before after compose_rc=0
  cd "$PANTRY_DIR"
  host_acquire_deploy_lock
  host_gate_before_operational
  host_entry_trace "publish-mode-source"
  # shellcheck disable=SC1091
  source "$PANTRY_DIR/publish-mode.sh"

  compose=(docker compose --project-directory "$PANTRY_DIR" -f "$PANTRY_DIR/docker-compose.yml")
  mode_rc=0
  mode=$(publish_mode_from_env) || mode_rc=$?
  if [[ "$mode_rc" -eq 2 ]]; then
    echo "Tunnel mode needs CLOUDFLARE_TUNNEL_TOKEN in ${PANTRY_DIR}/.env. The public proxy was left unchanged. Run: sudo ./setup.sh publish --tunnel" >&2
    exit 1
  elif [[ "$mode_rc" -ne 0 ]]; then
    echo "PUBLISH_MODE must be acme or tunnel. The public proxy was left unchanged." >&2
    exit 1
  fi

  if [[ "$mode" == tunnel ]]; then
    if [[ ! -f "$PANTRY_DIR/docker-compose.tunnel.yml" || ! -f "$PANTRY_DIR/Caddyfile.tunnel" ]]; then
      echo "CLOUDFLARE_TUNNEL_TOKEN is set but tunnel compose files are missing from ${PANTRY_DIR}; leaving the tunnel stopped. Run: sudo ./setup.sh publish --tunnel" >&2
    elif [[ -z "$(publish_env_value PUBLIC_HOST)" ]]; then
      echo "CLOUDFLARE_TUNNEL_TOKEN is set but PUBLIC_HOST is empty; leaving the tunnel stopped. Set PUBLIC_HOST in ${PANTRY_DIR}/.env and run: sudo ./setup.sh publish --tunnel" >&2
    else
      compose+=(-f "$PANTRY_DIR/docker-compose.tunnel.yml" --profile tunnel)
    fi
  elif [[ "$mode" == acme ]]; then
    if [[ -n "$(publish_env_value PUBLIC_HOST)" && -f "$PANTRY_DIR/Caddyfile" ]]; then
      compose+=(--profile public)
    elif [[ -n "$(publish_env_value PUBLIC_HOST)" ]]; then
      echo "PUBLIC_HOST is set but ${PANTRY_DIR}/Caddyfile is missing; leaving the public proxy stopped. Run: sudo ./setup.sh" >&2
    fi
  fi
  while IFS= read -r arg; do
    [[ -n "$arg" ]] && compose+=("$arg")
  done < <(grocy_compose_suffix)

  # shellcheck disable=SC1091
  source "$PANTRY_DIR/runtime-setup.sh"
  if ! prepare_setup_dir; then
    echo "Could not prepare ${PANTRY_DIR}/setup for the setup code" >&2
    exit 1
  fi
  if ! ensure_app_base_url; then
    echo "Could not set APP_BASE_URL from PUBLIC_HOST; leaving containers unchanged." >&2
    exit 1
  fi
  if public_proxy_in_update; then
    if ! ensure_session_secret; then
      echo "Could not save the session secret; leaving containers unchanged." >&2
      exit 1
    fi
  fi
  # shellcheck disable=SC1091
  source "$PANTRY_DIR/vapid-env.sh"
  if ! ensure_vapid_keys; then
    echo "Could not generate VAPID keys; scan alerts stay on an open tab until sudo ./setup.sh succeeds." >&2
  fi

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
    compose_rc=0
    apply_compose || compose_rc=$?
    if [[ "$compose_rc" -eq "$HOST_TXN_FATAL_RC" ]]; then
      exit "$compose_rc"
    fi
    if [[ "$compose_rc" -ne 0 ]]; then
      exit "$compose_rc"
    fi
    after=$(trigger_digest || true)
    if [[ "$after" == "$before" ]]; then
      echo "pantry update: finished"
      break
    fi
    echo "pantry update: trigger changed during the pull; pulling again"
  done
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  pantry_update_main
fi
