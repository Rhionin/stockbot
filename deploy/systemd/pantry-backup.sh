#!/usr/bin/env bash
# Back up pantry.db from the running image, or check that a backup is fresh.
#
# The first argument, or BACKUP_REASON, is nightly, manual, pre-deploy,
# drill, or stale, or the subcommand record-skip (skipped receipt only).
# The timer sets nightly. A manual install writes BACKUP_REASON=manual into
# the EnvironmentFile, which overrides the unit.
#
# This waits on the same lock the updater takes:
# ${PANTRY_DIR}/deploy-trigger/.lock. The updater holds that lock for the
# whole pull. A pre-deploy backup is started from inside that script, so
# flocking again would wait until the updater exits, and the updater is
# waiting on this script. PANTRY_BACKUP_LOCK_HELD=1 means the caller
# already holds the lock. This script then does not flock it. A backup
# that is not started from the updater still takes the lock itself.
#
# The container stages the snapshot on a 64MiB tmpfs at /run/stockbot-backup
# when the database plus headroom fits. Otherwise it stages on the pantry
# data volume. The EXIT trap deletes that directory even when the container
# is killed. The plaintext file is removed once the upload is verified.
#
# Docker mounts --tmpfs as root:root. On Docker 29 that directory is mode
# 0700 even with --user 65532:65532, so the container user cannot chmod it
# and preflight fails. uid and gid on the mount make it writable by 65532.
#
# auth.caddy and .env backup copies are archived when they exist as regular
# files. The live .env is not in that list. Extra paths come only from
# ${PANTRY_DIR}/backup/secrets-paths, which the installer writes from
# PANTRY_BACKUP_SECRETS_PATHS. A symlink or any other path that is not a
# regular file fails the run, after a healthchecks failure ping, before
# the backup container starts. .env.example and the R2 settings file are
# not included.
set -euo pipefail

PANTRY_DIR="${PANTRY_DIR:-/opt/pantry}"
PANTRY_BACKUP_LOCK_WAIT="${PANTRY_BACKUP_LOCK_WAIT:-900}"
DOCKER="${DOCKER:-docker}"
PANTRY_CONTAINER="${PANTRY_CONTAINER:-pantry}"
PANTRY_RECEIPT_DIR="${PANTRY_RECEIPT_DIR:-$PANTRY_DIR/backup/receipts}"
PANTRY_RECEIPT_KEEP="${PANTRY_RECEIPT_KEEP:-30}"

receipt_stage=args
receipt_cleanup_armed=0
receipt_skip_mode=0
receipt_started_at_utc=""
receipt_image_id=""
receipt_stdout_file=""
receipt_stderr_file=""
receipt_result_source=wrapper
receipt_go_stored=""
receipt_go_object_key=""
receipt_go_manifest_key=""
receipt_go_integrity=""
receipt_go_foreign_key_check=""
receipt_go_ciphertext_size=""
receipt_go_ciphertext_sha256=""
receipt_go_last_migration=""
receipt_go_staging=""
receipt_go_tool_version=""
receipt_go_created_at=""
receipt_go_error_class=""

receipt_warn() {
  echo "[warn] backup receipt not written" >&2
}

receipt_json_quote() {
  local s=$1
  s=${s//\\/\\\\}
  s=${s//\"/\\\"}
  printf '"%s"' "$s"
}

receipt_sanitize_reason() {
  local v=$1
  case "$v" in
    nightly|manual|pre-deploy|drill|stale) printf '%s' "$v" ;;
    *) printf 'invalid' ;;
  esac
}

receipt_sanitize_object_key() {
  local v=$1
  [[ -z "$v" ]] && return 1
  if [[ "$v" =~ ^(nightly|manual)/[0-9TZ:.-]+(\.[a-z-]+)?\.(pantry\.db\.gz\.age|manifest\.json)$ ]]; then
    printf '%s' "$v"
    return 0
  fi
  return 1
}

receipt_sanitize_hash64() {
  local v=$1
  [[ "$v" =~ ^[0-9a-f]{64}$ ]] && printf '%s' "$v"
}

receipt_sanitize_image_id() {
  local v=$1
  [[ "$v" =~ ^sha256:[0-9a-f]{64}$ ]] && printf '%s' "$v"
}

receipt_sanitize_revision() {
  local v=$1
  [[ "$v" =~ ^[0-9a-f]{7,40}$ ]] && printf '%s' "$v"
}

receipt_sanitize_integrity() {
  local v=$1
  case "$v" in
    ok|foreign_key_violation|unknown) printf '%s' "$v" ;;
    *) return 1 ;;
  esac
}

receipt_sanitize_fk() {
  local v=$1
  case "$v" in
    ok|failed|skipped|unknown) printf '%s' "$v" ;;
    *) return 1 ;;
  esac
}

receipt_sanitize_staging() {
  local v=$1
  case "$v" in
    tmpfs|volume|unknown) printf '%s' "$v" ;;
    *) return 1 ;;
  esac
}

receipt_sanitize_error_class() {
  local v=$1
  [[ "$v" =~ ^[a-z0-9_-]{1,32}$ ]] && printf '%s' "$v"
}

receipt_sanitize_migration() {
  local v=$1
  [[ "$v" =~ ^[0-9A-Za-z_][0-9A-Za-z_.-]{0,63}$ ]] && printf '%s' "$v"
}

receipt_sanitize_tool_version() {
  local v=$1
  [[ "$v" =~ ^v?[0-9A-Za-z][0-9A-Za-z._-]{0,31}$ ]] && printf '%s' "$v"
}

receipt_sanitize_utc() {
  local v=$1
  [[ "$v" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] && printf '%s' "$v"
}

receipt_sanitize_deploy_mode() {
  local v=$1
  case "$v" in
    required|warn|off) printf '%s' "$v" ;;
    *) printf 'unknown' ;;
  esac
}

receipt_sanitize_failed_stage() {
  local v=$1
  case "$v" in
    args|secrets-preflight|lock|inspect|backup-run|cleanup) printf '%s' "$v" ;;
    *) return 1 ;;
  esac
}

receipt_json_field_str() {
  local json=$1 key=$2
  local pattern="\"${key}\":\"([^\"]*)\""
  if [[ "$json" =~ $pattern ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
    return 0
  fi
  pattern="\"${key}\":null"
  [[ "$json" == *"\"${key}\":null"* ]] && return 1
  return 1
}

receipt_json_field_bool() {
  local json=$1 key=$2
  if [[ "$json" == *"\"${key}\":true"* ]]; then
    printf 'true'
    return 0
  fi
  if [[ "$json" == *"\"${key}\":false"* ]]; then
    printf 'false'
    return 0
  fi
  return 1
}

receipt_json_field_num() {
  local json=$1 key=$2
  local pattern="\"${key}\":([0-9]+)"
  if [[ "$json" =~ $pattern ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

receipt_parse_container_output() {
  local out_file=$1 err_file=$2
  local line json
  receipt_result_source=wrapper
  receipt_go_stored=""
  receipt_go_object_key=""
  receipt_go_manifest_key=""
  receipt_go_integrity=""
  receipt_go_foreign_key_check=""
  receipt_go_ciphertext_size=""
  receipt_go_ciphertext_sha256=""
  receipt_go_last_migration=""
  receipt_go_staging=""
  receipt_go_tool_version=""
  receipt_go_created_at=""
  receipt_go_error_class=""
  receipt_go_exit_code=""

  if [[ -f "$out_file" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      if [[ "$line" == backup\ result\ * ]]; then
        json=${line#backup result }
        receipt_result_source=go
        receipt_go_stored=$(receipt_json_field_bool "$json" stored || true)
        receipt_go_object_key=$(receipt_json_field_str "$json" object_key 2>/dev/null || true)
        receipt_go_manifest_key=$(receipt_json_field_str "$json" manifest_key 2>/dev/null || true)
        receipt_go_integrity=$(receipt_json_field_str "$json" integrity 2>/dev/null || true)
        receipt_go_foreign_key_check=$(receipt_json_field_str "$json" foreign_key_check 2>/dev/null || true)
        receipt_go_ciphertext_size=$(receipt_json_field_num "$json" ciphertext_size 2>/dev/null || true)
        receipt_go_ciphertext_sha256=$(receipt_json_field_str "$json" ciphertext_sha256 2>/dev/null || true)
        receipt_go_last_migration=$(receipt_json_field_str "$json" last_migration 2>/dev/null || true)
        receipt_go_staging=$(receipt_json_field_str "$json" staging 2>/dev/null || true)
        receipt_go_tool_version=$(receipt_json_field_str "$json" tool_version 2>/dev/null || true)
        receipt_go_created_at=$(receipt_json_field_str "$json" created_at 2>/dev/null || true)
        receipt_go_error_class=$(receipt_json_field_str "$json" error_class 2>/dev/null || true)
        return 0
      fi
      if [[ "$line" == backup\ ok\ key=* ]]; then
        local key integrity
        key=$(sed -n 's/^backup ok key=\([^ ]*\).*/\1/p' <<<"$line")
        integrity=$(sed -n 's/.* integrity=\([^ ]*\).*/\1/p' <<<"$line")
        receipt_result_source=stdout-fallback
        receipt_go_stored=true
        receipt_go_object_key=$key
        receipt_go_integrity=$integrity
        return 0
      fi
    done <"$out_file"
  fi

  if [[ -f "$err_file" ]]; then
    local class
    class=$(sed -n 's/^backup failed class=\([a-z0-9_-]\{1,32\}\).*/\1/p' <"$err_file" | head -n1)
    if [[ -n "$class" ]]; then
      receipt_go_error_class=$class
    fi
  fi
}

receipt_denver_from_utc() {
  local utc=$1
  TZ=America/Denver date -d "$utc" +%Y-%m-%dT%H:%M:%S%:z 2>/dev/null || printf 'unknown'
}

receipt_null_or_str() {
  local sanitized=$1
  if [[ -z "$sanitized" ]]; then
    printf 'null'
  else
    receipt_json_quote "$sanitized"
  fi
}

receipt_null_or_int() {
  local v=$1
  if [[ -z "$v" || ! "$v" =~ ^[0-9]+$ ]]; then
    printf 'null'
  else
    printf '%s' "$v"
  fi
}

receipt_build_json() {
  local rc=$1 reason=$2
  local status exit_code failed_stage error_class stored
  local object_key manifest_key integrity fk size sha mig staging tool
  local created_utc created_denver finished_utc image_id result_source
  local deploy_json

  if [[ "$receipt_skip_mode" == 1 ]]; then
    status=skipped
    exit_code=null
    failed_stage=null
    error_class=null
    stored=false
    object_key=null
    manifest_key=null
    integrity=null
    fk=null
    size=null
    sha=null
    mig=null
    staging=null
    tool=null
    result_source=$(receipt_json_quote wrapper)
  else
    if [[ "$rc" -eq 0 ]]; then
      status=ok
      failed_stage=null
      error_class=null
    else
      status=failed
      if receipt_sanitize_failed_stage "$receipt_stage" >/dev/null 2>&1; then
        failed_stage=$(receipt_json_quote "$(receipt_sanitize_failed_stage "$receipt_stage")")
      else
        failed_stage=$(receipt_json_quote cleanup)
      fi
      if sanitized_class=$(receipt_sanitize_error_class "${receipt_go_error_class:-}"); then
        error_class=$(receipt_json_quote "$sanitized_class")
      else
        error_class=null
      fi
    fi
    exit_code=$rc

    if [[ "$receipt_go_stored" == true ]]; then
      stored=true
    elif [[ "$receipt_go_stored" == false ]]; then
      stored=false
    elif [[ "$rc" -eq 0 && "$receipt_result_source" == stdout-fallback ]]; then
      stored=true
    elif [[ "$rc" -eq 0 ]]; then
      stored=true
    else
      stored=false
    fi

    if [[ -n "${receipt_go_object_key:-}" ]]; then
      if sanitized=$(receipt_sanitize_object_key "${receipt_go_object_key}" 2>/dev/null); then
        object_key=$(receipt_json_quote "$sanitized")
      else
        object_key='"invalid"'
      fi
    else
      object_key=null
    fi
    if [[ -n "${receipt_go_manifest_key:-}" ]]; then
      if sanitized=$(receipt_sanitize_object_key "${receipt_go_manifest_key}" 2>/dev/null); then
        manifest_key=$(receipt_json_quote "$sanitized")
      else
        manifest_key='"invalid"'
      fi
    else
      manifest_key=null
    fi
    if [[ -n "${receipt_go_integrity:-}" ]]; then
      if sanitized=$(receipt_sanitize_integrity "${receipt_go_integrity}" 2>/dev/null); then
        integrity=$(receipt_json_quote "$sanitized")
      else
        integrity='"invalid"'
      fi
    else
      integrity=null
    fi
    if sanitized=$(receipt_sanitize_fk "${receipt_go_foreign_key_check:-}" 2>/dev/null); then
      fk=$(receipt_json_quote "$sanitized")
    else
      fk=null
    fi
    size=$(receipt_null_or_int "${receipt_go_ciphertext_size:-}")
    if sanitized=$(receipt_sanitize_hash64 "${receipt_go_ciphertext_sha256:-}" 2>/dev/null); then
      sha=$(receipt_json_quote "$sanitized")
    else
      sha=null
    fi
    if sanitized=$(receipt_sanitize_migration "${receipt_go_last_migration:-}" 2>/dev/null); then
      mig=$(receipt_json_quote "$sanitized")
    else
      mig=null
    fi
    if sanitized=$(receipt_sanitize_staging "${receipt_go_staging:-}" 2>/dev/null); then
      staging=$(receipt_json_quote "$sanitized")
    else
      staging=null
    fi
    if sanitized=$(receipt_sanitize_tool_version "${receipt_go_tool_version:-}" 2>/dev/null); then
      tool=$(receipt_json_quote "$sanitized")
    else
      tool=null
    fi
    result_source=$(receipt_json_quote "$receipt_result_source")
  fi

  if sanitized=$(receipt_sanitize_utc "${receipt_go_created_at:-}" 2>/dev/null); then
    created_utc=$sanitized
  else
    created_utc=$receipt_started_at_utc
  fi
  created_denver=$(receipt_denver_from_utc "$created_utc")
  finished_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  if sanitized=$(receipt_sanitize_image_id "${receipt_image_id:-}" 2>/dev/null); then
    image_id=$(receipt_json_quote "$sanitized")
  else
    image_id=null
  fi

  reason=$(receipt_json_quote "$(receipt_sanitize_reason "$reason")")

  if [[ "$2" == pre-deploy ]]; then
    local mode rev digest
    mode=$(receipt_sanitize_deploy_mode "${PANTRY_BACKUP_DEPLOY_MODE:-}")
    if sanitized=$(receipt_sanitize_revision "${PANTRY_BACKUP_TARGET_REVISION:-}" 2>/dev/null); then
      rev=$sanitized
    else
      rev=unknown
    fi
    if sanitized=$(receipt_sanitize_image_id "${PANTRY_BACKUP_TARGET_DIGEST:-}" 2>/dev/null); then
      digest=$sanitized
    else
      digest=unknown
    fi
    deploy_json=$(printf '{"mode":%s,"target_revision":%s,"target_image_digest":%s}' \
      "$(receipt_json_quote "$mode")" \
      "$(receipt_json_quote "$rev")" \
      "$(receipt_json_quote "$digest")")
  else
    deploy_json=null
  fi

  printf '{"schema":1,"status":"%s","reason":%s,"exit_code":%s,"failed_stage":%s,"error_class":%s,' \
    "$status" "$reason" "$exit_code" "$failed_stage" "$error_class"
  printf '"stored":%s,"object_key":%s,"manifest_key":%s,"integrity":%s,"foreign_key_check":%s,' \
    "$stored" "$object_key" "$manifest_key" "$integrity" "${fk:-null}"
  printf '"ciphertext_size":%s,"ciphertext_sha256":%s,"last_migration":%s,"staging":%s,"tool_version":%s,' \
    "$size" "$sha" "${mig:-null}" "${staging:-null}" "${tool:-null}"
  printf '"source_image_id":%s,"created_at_utc":"%s","created_at_denver":%s,"finished_at_utc":"%s",' \
    "$image_id" "$created_utc" "$(receipt_json_quote "$created_denver")" "$finished_utc"
  printf '"result_source":%s,"deploy":%s}' \
    "${result_source:-$(receipt_json_quote wrapper)}" "$deploy_json"
}

receipt_safe_path() {
  local path=$1
  [[ -L "$path" ]] && return 1
  [[ -e "$path" && ! -f "$path" && ! -d "$path" ]] && return 1
  return 0
}

receipt_write_atomic() {
  local dest=$1 body=$2
  local dir tmp
  dir=$(dirname "$dest")
  [[ -L "$dest" ]] && return 1
  [[ -e "$dest" && ! -f "$dest" ]] && return 1
  tmp="$dir/.tmp.$$.$RANDOM"
  printf '%s\n' "$body" >"$tmp" || { rm -f "$tmp"; return 1; }
  chmod 0644 "$tmp" || { rm -f "$tmp"; return 1; }
  mv -f "$tmp" "$dest"
}

receipt_append_history() {
  local dir=$1 line=$2 keep=$3
  local hist tmp old_lines n
  hist="$dir/history.jsonl"
  [[ -L "$hist" ]] && return 1
  [[ -e "$hist" && ! -f "$hist" ]] && return 1
  old_lines=""
  if [[ -f "$hist" ]]; then
    old_lines=$(cat "$hist" 2>/dev/null || true)
  fi
  if [[ -n "$old_lines" ]]; then
    n=$(printf '%s\n' "$old_lines" | wc -l | tr -d ' ')
    if (( n >= keep )); then
      old_lines=$(printf '%s\n' "$old_lines" | tail -n "$((keep - 1))")
    fi
    printf '%s\n%s\n' "$old_lines" "$line" >"$hist.tmp.$$"
  else
    printf '%s\n' "$line" >"$hist.tmp.$$"
  fi
  chmod 0644 "$hist.tmp.$$"
  mv -f "$hist.tmp.$$" "$hist"
}

receipt_persist() {
  local json=$1 reason=$2
  local dir lock_fd=0
  set +e
  dir="$PANTRY_RECEIPT_DIR"
  if ! receipt_safe_path "$dir"; then
    receipt_warn
    set -e
    return 0
  fi
  mkdir -p "$dir" 2>/dev/null || { receipt_warn; set -e; return 0; }
  chmod 0755 "$dir" 2>/dev/null || true
  if [[ -L "$dir/.lock" ]]; then
    receipt_warn
    set -e
    return 0
  fi
  exec {lock_fd}>>"$dir/.lock"
  if ! flock -w 30 "$lock_fd"; then
    receipt_warn
    exec {lock_fd}>&-
    set -e
    return 0
  fi
  receipt_append_history "$dir" "$json" "$PANTRY_RECEIPT_KEEP" || { receipt_warn; flock -u "$lock_fd"; exec {lock_fd}>&-; set -e; return 0; }
  receipt_write_atomic "$dir/latest.json" "$json" || receipt_warn
  if [[ "$reason" == pre-deploy ]]; then
    receipt_write_atomic "$dir/latest-pre-deploy.json" "$json" || receipt_warn
  fi
  flock -u "$lock_fd"
  exec {lock_fd}>&-
  set -e
  return 0
}

write_receipt() {
  local rc=$1
  local json reason="${reason:-unknown}"
  json=$(receipt_build_json "$rc" "$reason")
  receipt_persist "$json" "$reason" || true
}

on_exit() {
  local rc=$?
  if [[ "$receipt_cleanup_armed" == 1 ]]; then
    cleanup_stage || true
  fi
  if [[ -n "$receipt_stdout_file" && -f "$receipt_stdout_file" ]]; then
    receipt_parse_container_output "$receipt_stdout_file" "${receipt_stderr_file:-/dev/null}"
  fi
  write_receipt "$rc" || true
}

if [[ "${1:-}" == record-skip ]]; then
  reason="${BACKUP_REASON:-pre-deploy}"
  receipt_skip_mode=1
  receipt_started_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  trap on_exit EXIT
  exit 0
fi

reason="${1:-${BACKUP_REASON:-nightly}}"
receipt_started_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
trap on_exit EXIT
trap 'exit 1' INT TERM

case "$reason" in
  nightly|manual|pre-deploy|drill|stale) ;;
  *)
    echo "backup reason must be nightly, manual, pre-deploy, drill, or stale" >&2
    exit 2
    ;;
esac

# shellcheck disable=SC1091
source "$PANTRY_DIR/publish-mode.sh"
volume="${PANTRY_DATA_VOLUME:-$(compose_project_name)_pantry-data}"

secret_stage=""
config_secrets=()

receipt_stage=secrets-preflight

# Tell healthchecks the night was missed. The URL is not printed.
ping_failure() {
  local settings="$PANTRY_DIR/secrets/backup/r2.env" mode perm url
  [[ -f "$settings" ]] || return 0
  mode=$(stat -c '%a' "$settings" 2>/dev/null || stat -f '%OLp' "$settings" 2>/dev/null || true)
  [[ -n "$mode" ]] || return 0
  perm=$((8#$mode))
  if (( (perm & 077) != 0 )); then
    return 0
  fi
  url=$(awk '/^HC_BACKUP_RUN_URL=/ {print substr($0, length("HC_BACKUP_RUN_URL=")+1); exit}' "$settings" 2>/dev/null || true)
  [[ -n "$url" ]] || return 0
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -X POST --max-time 10 -o /dev/null "${url%/}/fail" >/dev/null 2>&1 || true
  fi
}

refuse_secret() {
  echo "backup: $1" >&2
  ping_failure
  exit 40
}

add_secret() {
  local path="$1" existing
  if [[ ${#config_secrets[@]} -gt 0 ]]; then
    for existing in "${config_secrets[@]}"; do
      [[ "$existing" == "$path" ]] && return 0
    done
  fi
  config_secrets+=("$path")
}

consider_secret() {
  local path="$1"
  if [[ -L "$path" ]]; then
    refuse_secret "a config secret is a symlink"
  fi
  if [[ -e "$path" && ! -f "$path" ]]; then
    refuse_secret "a config secret is not a regular file"
  fi
  if [[ -f "$path" ]]; then
    add_secret "$path"
  fi
}

consider_configured() {
  local path="$1"
  if [[ "$path" != /* ]]; then
    refuse_secret "a configured secret path is not absolute"
  fi
  if [[ -L "$path" || ! -f "$path" ]]; then
    refuse_secret "a configured secret path is not a regular file"
  fi
  add_secret "$path"
}

consider_secret "$PANTRY_DIR/auth.caddy"
shopt -s nullglob
for path in \
  "$PANTRY_DIR"/.env.bak \
  "$PANTRY_DIR"/.env.bak.* \
  "$PANTRY_DIR"/.env.backup \
  "$PANTRY_DIR"/.env.backup.*
do
  consider_secret "$path"
done
shopt -u nullglob
secrets_list="$PANTRY_DIR/backup/secrets-paths"
if [[ -L "$secrets_list" || -e "$secrets_list" ]]; then
  if [[ -L "$secrets_list" || ! -f "$secrets_list" ]]; then
    refuse_secret "the secrets path list is not a regular file"
  fi
  while IFS= read -r path || [[ -n "$path" ]]; do
    [[ -z "$path" || "$path" == \#* ]] && continue
    consider_configured "$path"
  done <"$secrets_list"
fi

remove_secret_stage() {
  local f
  [[ -n "$secret_stage" && -d "$secret_stage" ]] || return 0
  while IFS= read -r -d '' f; do
    if command -v shred >/dev/null 2>&1; then
      shred -u "$f" 2>/dev/null || rm -f "$f"
    else
      rm -f "$f"
    fi
  done < <(find "$secret_stage" -type f -print0 2>/dev/null || true)
  rm -rf "$secret_stage"
  secret_stage=""
}

stage_config_secrets() {
  local src base parent
  [[ ${#config_secrets[@]} -gt 0 ]] || return 0
  if [[ -d /run && -w /run ]]; then
    parent=/run
  else
    parent="${TMPDIR:-/tmp}"
  fi
  secret_stage=$(mktemp -d "$parent/stockbot-backup-secrets.XXXXXX")
  chmod 0700 "$secret_stage"
  for src in "${config_secrets[@]}"; do
    base=$(basename "$src")
    cp -- "$src" "$secret_stage/$base"
    chmod 0400 "$secret_stage/$base"
  done
  if ! chown -R 65532:65532 "$secret_stage"; then
    if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
      echo "[warn] Could not give the config-secret copy to uid 65532" >&2
    else
      echo "Could not give the config-secret copy to uid 65532" >&2
      ping_failure
      exit 40
    fi
  fi
}

mkdir -p "$PANTRY_DIR/deploy-trigger"
if ! chown 65532:65532 "$PANTRY_DIR/deploy-trigger"; then
  if [[ "${PANTRY_SETUP_SKIP_ROOT:-}" == 1 ]]; then
    echo "[warn] Could not give ${PANTRY_DIR}/deploy-trigger to uid 65532" >&2
  else
    echo "Could not give ${PANTRY_DIR}/deploy-trigger to uid 65532" >&2
    exit 1
  fi
fi
chmod 755 "$PANTRY_DIR/deploy-trigger"

receipt_stage=lock

# The updater holds this lock across the pre-deploy backup. Taking it
# again from that child blocks until the updater exits, which is waiting
# on this script. PANTRY_BACKUP_LOCK_HELD=1 means the caller already
# holds the lock; do not flock it a second time. The caller's process
# still drops the lock if it is killed. This script's EXIT trap removes
# the container on a normal failure.
if [[ "${PANTRY_BACKUP_LOCK_HELD:-}" == 1 ]]; then
  echo "backup: deploy lock already held"
else
  exec 9>"$PANTRY_DIR/deploy-trigger/.lock"
  if ! flock -w "$PANTRY_BACKUP_LOCK_WAIT" 9; then
    echo "backup: could not take the deploy lock" >&2
    ping_failure
    exit 30
  fi

  # If this process is killed, the kernel drops the lock. The waiter below
  # then stops the container, which is not in this process's cgroup.
  (
    flock -x 8 || exit 0
    "$DOCKER" rm -f pantry-backup >/dev/null 2>&1 || true
  ) 8>"$PANTRY_DIR/deploy-trigger/.lock" 9>&- &
fi

receipt_stage=inspect
image=$("$DOCKER" inspect "$PANTRY_CONTAINER" --format '{{.Image}}' 9>&-)
receipt_image_id=$image

cleanup_stage() {
  "$DOCKER" rm -f pantry-backup >/dev/null 2>&1 9>&- || true
  remove_secret_stage
  "$DOCKER" run --rm \
    --user 65532:65532 \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --pids-limit 64 \
    --memory 128m \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700,uid=65532,gid=65532 \
    --volume "${volume}:/data" \
    --entrypoint /usr/local/bin/stockbot-backup \
    "$image" cleanup-stage -db /data/pantry.db 9>&- || true
}
receipt_cleanup_armed=1

"$DOCKER" rm -f pantry-backup >/dev/null 2>&1 || true

run_locked() {
  "$DOCKER" run --name pantry-backup --rm \
    --user 65532:65532 \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --pids-limit 64 \
    --memory 128m \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700,uid=65532,gid=65532 \
    --volume "${volume}:/data" \
    --volume "${PANTRY_DIR}/secrets/backup:/run/secrets/backup:ro" \
    --volume "${PANTRY_DIR}/backup:/etc/backup:ro" \
    --entrypoint /usr/local/bin/stockbot-backup \
    "$@" 9>&-
}

run_backup_container() {
  local out_dir rc
  if [[ -d /run && -w /run ]]; then
    out_dir=$(mktemp -d /run/stockbot-backup-capture.XXXXXX)
  else
    out_dir=$(mktemp -d "${TMPDIR:-/tmp}/stockbot-backup-capture.XXXXXX")
  fi
  chmod 0700 "$out_dir"
  receipt_stdout_file="$out_dir/stdout"
  receipt_stderr_file="$out_dir/stderr"
  receipt_stage=backup-run
  set +e
  run_locked "$@" >"$receipt_stdout_file" 2>"$receipt_stderr_file"
  rc=$?
  set -e
  cat "$receipt_stdout_file"
  cat "$receipt_stderr_file" >&2
  return "$rc"
}

if [[ "$reason" == stale ]]; then
  run_backup_container "$image" stale --secrets /run/secrets/backup/r2.env
  exit $?
fi

stage_config_secrets

docker_extra=()
cmd_extra=()
if [[ -n "$secret_stage" ]]; then
  docker_extra+=(--volume "${secret_stage}:/config-secrets:ro")
  cmd_extra+=(--with-secrets)
  for src in "${config_secrets[@]}"; do
    cmd_extra+=(--secrets-path "/config-secrets/$(basename "$src")")
  done
fi

run_backup_container \
  --tmpfs /run/stockbot-backup:rw,noexec,nosuid,size=64m,mode=0700,uid=65532,gid=65532 \
  "${docker_extra[@]}" \
  "$image" run \
  --db /data/pantry.db \
  --stage /run/stockbot-backup \
  --reason "$reason" \
  --secrets /run/secrets/backup/r2.env \
  --recipients /etc/backup/recipients \
  --image-digest "$image" \
  "${cmd_extra[@]}"
