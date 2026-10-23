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
# The container stages the snapshot on a 64MiB tmpfs when the database plus
# headroom fits. Otherwise it stages on the pantry data volume and the EXIT
# trap deletes that directory even when the container is killed.
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
  exit 1
fi

image=$("$DOCKER" inspect "$PANTRY_CONTAINER" --format '{{.Image}}')

cleanup_stage() {
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
    "$image" cleanup-stage -db /data/pantry.db || true
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
    "$@"
}

if [[ "$reason" == stale ]]; then
  run_locked "$image" stale --secrets /run/secrets/backup/r2.env
  exit
fi

run_locked \
  --tmpfs /stage:rw,noexec,nosuid,size=64m,mode=0700 \
  "$image" run \
  --db /data/pantry.db \
  --stage /stage \
  --reason "$reason" \
  --secrets /run/secrets/backup/r2.env \
  --recipients /etc/backup/recipients \
  --image-digest "$image"
