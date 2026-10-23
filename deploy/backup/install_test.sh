#!/usr/bin/env bash
# Installs the backup units with stubbed docker, systemctl, and journalctl.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
bin="$work/bin"
mkdir -p "$bin" "$work/systemd" "$work/run"
: >"$work/docker.log"
: >"$work/systemctl.log"

cat >"$bin/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${DOCKER_LOG:?}"
if [[ "$1" == inspect ]]; then
  printf '%s\n' "sha256:abc"
  exit 0
fi
if [[ "$*" == *version* ]]; then
  printf '%s\n' "stockbot-backup dev test"
  exit 0
fi
exit 0
EOF

cat >"$bin/systemctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${SYSTEMCTL_LOG:?}"
if [[ "$1" == start && -n "${PANTRY_BACKUP_REASON_FILE:-}" && -f "$PANTRY_BACKUP_REASON_FILE" ]]; then
  cp "$PANTRY_BACKUP_REASON_FILE" "${REASON_COPY:?}"
fi
exit 0
EOF

cat >"$bin/journalctl" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "products=should-not-be-printed"
printf '%s\n' "backup ok key=manual/2026-10-23T030000Z.manual.pantry.db.gz.age integrity=ok rows=4 staging=tmpfs at=2026-10-23T03:00:00-06:00"
EOF

cat >"$bin/chown" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
chmod +x "$bin/docker" "$bin/systemctl" "$bin/journalctl" "$bin/chown"

secret="test-secret-access-key-value"
answers=$(printf '%s\n' \
  "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p" \
  "abc123account" \
  "access-key-id" \
  "$secret" \
  "" \
  "")

run_install() {
  local script="$1" keep="${2:-1}" rc
  set +e
  printf '%s\n' "$answers" | \
    DOCKER_LOG="$work/docker.log" \
    SYSTEMCTL_LOG="$work/systemctl.log" \
    REASON_COPY="$work/reason.copy" \
    PATH="$bin:$PATH" \
    PANTRY_DIR="$work/pantry" \
    PANTRY_SYSTEMD_UNIT_DIR="$work/systemd" \
    PANTRY_BACKUP_UNIT_DIR="$ROOT/deploy/systemd" \
    PANTRY_BACKUP_REASON_FILE="$work/run/pantry-backup.env" \
    PANTRY_SETUP_SKIP_ROOT=1 \
    PANTRY_BACKUP_INSTALL_KEEP="$keep" \
    bash "$script" >"$work/out" 2>"$work/err"
  rc=$?
  set -e
  printf '%s' "$rc"
}

: >"$work/docker.log"
: >"$work/systemctl.log"
rc=$(run_install "$ROOT/deploy/backup/install.sh")
[[ "$rc" == 0 ]] || fail "install rc=$rc err=$(cat "$work/err") out=$(cat "$work/out")"
[[ -f "$ROOT/deploy/backup/install.sh" ]] || fail "install from the repo path shredded itself"
mode=$(stat -c '%a' "$work/pantry/secrets/backup/r2.env")
[[ "$mode" == 400 ]] || fail "settings mode is $mode"
grep -q "R2_SECRET_ACCESS_KEY=$secret" "$work/pantry/secrets/backup/r2.env" || fail "settings file missing the secret"
grep -q 'R2_BUCKET=pantry-pi-backups' "$work/pantry/secrets/backup/r2.env" || fail "default bucket was not written"
grep -q '^age1' "$work/pantry/backup/recipients" || fail "recipient was not written"
[[ -f "$work/systemd/pantry-backup.timer" ]] || fail "timer was not installed"
[[ -x "$work/pantry/systemd/pantry-backup.sh" ]] || fail "backup wrapper was not installed"
grep -q 'enable --now pantry-backup.timer' "$work/systemctl.log" || fail "timer was not enabled"
grep -q 'BACKUP_REASON=manual' "$work/reason.copy" || fail "manual reason was not set for the one-shot run"
[[ ! -f "$work/run/pantry-backup.env" ]] || fail "manual reason file was left behind"
grep -q 'key=manual/2026-10-23T030000Z.manual.pantry.db.gz.age' "$work/out" || fail "object key was not printed"
grep -q 'integrity=ok' "$work/out" || fail "integrity was not printed"
grep -q 'rows=4' "$work/out" || fail "row total was not printed"
[[ "$(cat "$work/out" "$work/err")" != *products=* ]] || fail "install printed a table name"
[[ "$(cat "$work/out" "$work/err")" != *"$secret"* ]] || fail "install printed the secret access key"
grep -q 'version' "$work/docker.log" || fail "image was not checked for stockbot-backup before prompts"
[[ ! -e "$work/pantry/backup/secrets-paths" ]] || fail "install wrote a secrets list without being asked"

: >"$work/docker.log"
: >"$work/systemctl.log"
rc=$(PANTRY_BACKUP_SECRETS_PATHS="/opt/pantry/auth.caddy,/opt/pantry/note.txt" run_install "$ROOT/deploy/backup/install.sh")
[[ "$rc" == 0 ]] || fail "explicit secrets install rc=$rc err=$(cat "$work/err")"
[[ "$(cat "$work/pantry/backup/secrets-paths")" == $'/opt/pantry/auth.caddy\n/opt/pantry/note.txt' ]] || fail "secrets list was $(cat "$work/pantry/backup/secrets-paths")"
grep -q '/opt/pantry/.env' "$work/pantry/backup/secrets-paths" && fail "live .env was added without being named"
rm -f "$work/pantry/backup/secrets-paths"

set +e
PANTRY_DIR="$work/pantry" PANTRY_SETUP_SKIP_ROOT=1 PANTRY_BACKUP_SECRETS_PATHS="note.txt" \
  bash "$ROOT/deploy/backup/install.sh" >"$work/rel.out" 2>"$work/rel.err"
rel_rc=$?
set -e
[[ "$rel_rc" != 0 ]] || fail "a relative secrets path was accepted"
grep -q 'absolute path' "$work/rel.err" || fail "relative secrets path was not explained"

# A private key is rejected before the settings file is replaced.
printf '%s\n' "AGE-SECRET-KEY-TEST" "acct" "id" "secret-value" "" "" | \
  PATH="$bin:$PATH" \
  DOCKER_LOG="$work/docker.log" \
  SYSTEMCTL_LOG="$work/systemctl.log" \
  PANTRY_DIR="$work/pantry" \
  PANTRY_SYSTEMD_UNIT_DIR="$work/systemd" \
  PANTRY_BACKUP_UNIT_DIR="$ROOT/deploy/systemd" \
  PANTRY_BACKUP_REASON_FILE="$work/run/pantry-backup.env" \
  PANTRY_SETUP_SKIP_ROOT=1 \
  PANTRY_BACKUP_INSTALL_KEEP=1 \
  bash "$ROOT/deploy/backup/install.sh" >"$work/reject.out" 2>"$work/reject.err" || true
grep -q 'private key' "$work/reject.err" || fail "private key was not rejected"
[[ "$(cat "$work/reject.out" "$work/reject.err")" != *AGE-SECRET-KEY-TEST* ]] || fail "rejected private key was echoed"

tmp_script=$(mktemp /tmp/pantry-backup-install.XXXXXX.sh)
cp "$ROOT/deploy/backup/install.sh" "$tmp_script"
chmod +x "$tmp_script"
: >"$work/docker.log"
: >"$work/systemctl.log"
rc=$(run_install "$tmp_script" 0)
[[ "$rc" == 0 ]] || fail "tmp install rc=$rc err=$(cat "$work/err")"
[[ ! -e "$tmp_script" ]] || fail "install invoked from /tmp was not shredded"
