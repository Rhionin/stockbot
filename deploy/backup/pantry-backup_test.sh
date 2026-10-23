#!/usr/bin/env bash
# Behavior checks for the Pi backup wrapper. Docker is a stub.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin="$work/bin"
mkdir -p "$bin" "$work/pantry/secrets/backup" "$work/pantry/backup" "$work/pantry/deploy-trigger"
cp "$ROOT/deploy/publish-mode.sh" "$work/pantry/publish-mode.sh"
: >"$work/docker.log"

cat >"$bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${DOCKER_LOG:?}"
if [[ "$1" == inspect ]]; then
  printf '%s\n' "sha256:abc"
  exit 0
fi
if [[ "$1" == rm ]]; then
  exit 0
fi
if [[ "$*" == *cleanup-stage* ]]; then
  exit 0
fi
if [[ "$*" == *" run "* ]]; then
  if [[ "${FAIL_RUN:-}" == 1 ]]; then
    exit 1
  fi
  printf '%s\n' "backup ok key=nightly/x.pantry.db.gz.age integrity=ok rows=1 staging=tmpfs at=now"
  exit 0
fi
exit 0
EOF
chmod +x "$bin/docker"

run_backup() {
  local rc
  set +e
  DOCKER_LOG="$work/docker.log" \
    DOCKER="$bin/docker" \
    PATH="$bin:$PATH" \
    PANTRY_DIR="$work/pantry" \
    PANTRY_DATA_VOLUME="pantry_pantry-data" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_CONTAINER=pantry \
    FAIL_RUN="${FAIL_RUN:-}" \
    PANTRY_BACKUP_LOCK_WAIT="${PANTRY_BACKUP_LOCK_WAIT:-2}" \
    bash "$ROOT/deploy/systemd/pantry-backup.sh" "$@" >"$work/out" 2>"$work/err" 8>&- 9>&-
  rc=$?
  set -e
  printf '%s' "$rc"
}

: >"$work/docker.log"
rc=$(run_backup nightly)
[[ "$rc" == 0 ]] || fail "nightly backup wrapper rc=$rc err=$(cat "$work/err")"
log=$(cat "$work/docker.log")
for needle in \
  "--user 65532:65532" \
  "--read-only" \
  "--cap-drop ALL" \
  "--security-opt no-new-privileges" \
  "--pids-limit 64" \
  "--memory 128m" \
  "--tmpfs /stage:rw,noexec,nosuid,size=64m,mode=0700" \
  "--volume pantry_pantry-data:/data" \
  "--entrypoint /usr/local/bin/stockbot-backup" \
  "cleanup-stage -db /data/pantry.db" \
  "--image-digest sha256:abc"
do
  [[ "$log" == *"$needle"* ]] || fail "docker args missing $needle"
done
[[ "$log" == *--env-file* ]] && fail "docker was passed --env-file"
[[ "$log" == *R2_SECRET* ]] && fail "docker args named an R2 secret"
grep -q 'backup ok key=' "$work/out" || fail "success line was not printed"

: >"$work/docker.log"
FAIL_RUN=1
rc=$(FAIL_RUN=1 run_backup manual)
unset FAIL_RUN
[[ "$rc" != 0 ]] || fail "a failed docker run should fail the wrapper"
grep -q 'cleanup-stage' "$work/docker.log" || fail "failed run did not trap-clean the stage"

: >"$work/docker.log"
exec 8>"$work/pantry/deploy-trigger/.lock"
flock -n 8 || fail "could not hold the deploy lock in the test"
rc=$(PANTRY_BACKUP_LOCK_WAIT=1 run_backup nightly)
[[ "$rc" != 0 ]] || fail "backup should wait on the updater lock and time out"
[[ ! -s "$work/docker.log" ]] || fail "backup talked to docker without the lock"
flock -u 8
