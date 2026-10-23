#!/usr/bin/env bash
# Back up pantry.db from the running image, or check that a backup is fresh.
#
# The first argument, or BACKUP_REASON, is nightly, manual, pre-deploy,
# drill, or stale. The timer sets nightly. A manual install writes
# BACKUP_REASON=manual into the EnvironmentFile, which overrides the unit.
#
# This waits on the same lock the updater takes:
# ${PANTRY_DIR}/deploy-trigger/.lock. The updater's flock is unchanged.
#
# The container stages the snapshot on a 64MiB tmpfs at /run/stockbot-backup
# when the database plus headroom fits. Otherwise it stages on the pantry
# data volume. The EXIT trap deletes that directory even when the container
# is killed. The plaintext file is removed once the upload is verified.
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
reason="${1:-${BACKUP_REASON:-nightly}}"

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

image=$("$DOCKER" inspect "$PANTRY_CONTAINER" --format '{{.Image}}' 9>&-)

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
    --tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700 \
    --volume "${volume}:/data" \
    --entrypoint /usr/local/bin/stockbot-backup \
    "$image" cleanup-stage -db /data/pantry.db 9>&- || true
}
trap cleanup_stage EXIT
trap 'exit 1' INT TERM

"$DOCKER" rm -f pantry-backup >/dev/null 2>&1 || true

run_locked() {
  "$DOCKER" run --name pantry-backup --rm \
    --user 65532:65532 \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --pids-limit 64 \
    --memory 128m \
    --tmpfs /tmp:rw,noexec,nosuid,size=16m,mode=0700 \
    --volume "${volume}:/data" \
    --volume "${PANTRY_DIR}/secrets/backup:/run/secrets/backup:ro" \
    --volume "${PANTRY_DIR}/backup:/etc/backup:ro" \
    --entrypoint /usr/local/bin/stockbot-backup \
    "$@" 9>&-
}

if [[ "$reason" == stale ]]; then
  run_locked "$image" stale --secrets /run/secrets/backup/r2.env
  exit
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

run_locked \
  --tmpfs /run/stockbot-backup:rw,noexec,nosuid,size=64m,mode=0700 \
  "${docker_extra[@]}" \
  "$image" run \
  --db /data/pantry.db \
  --stage /run/stockbot-backup \
  --reason "$reason" \
  --secrets /run/secrets/backup/r2.env \
  --recipients /etc/backup/recipients \
  --image-digest "$image" \
  "${cmd_extra[@]}"
