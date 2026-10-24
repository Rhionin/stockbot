#!/usr/bin/env bash
# The backup timer must fire at 03:30 America/Denver, including across DST.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
unit="$ROOT/deploy/systemd/pantry-backup.timer"
fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -q 'OnCalendar=\*-\*-\* 03:30:00 America/Denver' "$unit" || fail "OnCalendar is not 03:30 America/Denver"
grep -q '^Persistent=true$' "$unit" || fail "Persistent=true is missing"
if grep -q '^Timezone=' "$unit"; then
  fail "Timezone= is not a timer key"
fi

if ! command -v systemd-analyze >/dev/null 2>&1; then
  echo "SKIP: systemd-analyze is not available; timer calendar and verify were not run" >&2
  exit 0
fi

# 2026-11-01 is the US fall-back. 03:30 Denver is 09:30Z the day before
# and 10:30Z on the morning the clocks change. 2027-03-14 is the spring-forward.
before=$(systemd-analyze calendar --base-time=2026-10-31 '*-*-* 03:30:00 America/Denver')
after=$(systemd-analyze calendar --base-time=2026-11-01 '*-*-* 03:30:00 America/Denver')
spring_before=$(systemd-analyze calendar --base-time=2027-03-13 '*-*-* 03:30:00 America/Denver')
spring_after=$(systemd-analyze calendar --base-time=2027-03-14 '*-*-* 03:30:00 America/Denver')
printf '%s\n' "$before" | grep -q '2026-10-31 09:30:00 UTC' || fail "31 Oct did not resolve to 09:30Z: $before"
printf '%s\n' "$after" | grep -q '2026-11-01 10:30:00 UTC' || fail "1 Nov did not resolve to 10:30Z: $after"
printf '%s\n' "$spring_before" | grep -q '2027-03-13 10:30:00 UTC' || fail "13 Mar did not resolve to 10:30Z: $spring_before"
printf '%s\n' "$spring_after" | grep -q '2027-03-14 09:30:00 UTC' || fail "14 Mar did not resolve to 09:30Z: $spring_after"

# sd_booted is /run/systemd/system. Without it, verify tries to create
# /run/systemd and fails with "Permission denied" on a container. That
# text is not a unit problem, and matching verify's error text can hide
# one. Skip when systemd is not booted.
if [[ ! -d /run/systemd/system ]]; then
  echo "SKIP: systemd is not booted; systemd-analyze verify was not run" >&2
  exit 0
fi

# ExecStart and WorkingDirectory are the Pi install. They are absent on a
# build machine, so verify would stop at the missing file. Check a copy
# whose paths exist; the timer and the other service keys are unchanged.
verify_dir=$(mktemp -d)
cp "$unit" "$ROOT/deploy/systemd/pantry-backup.service" "$verify_dir/"
if [[ ! -x /opt/pantry/systemd/pantry-backup.sh || ! -d /opt/pantry ]]; then
  sed -i \
    -e 's|^ExecStart=.*|ExecStart=/bin/true|' \
    -e 's|^WorkingDirectory=.*|WorkingDirectory=/tmp|' \
    "$verify_dir/pantry-backup.service"
fi
set +e
verify=$(systemd-analyze verify "$verify_dir/pantry-backup.timer" "$verify_dir/pantry-backup.service" 2>&1)
verify_rc=$?
set -e
rm -rf "$verify_dir"
if printf '%s\n' "$verify" | grep -q 'Unknown key'; then
  printf '%s\n' "$verify" >&2
  fail "systemd-analyze verify reported an unknown key"
fi
if [[ "$verify_rc" -ne 0 ]]; then
  printf '%s\n' "$verify" >&2
  fail "systemd-analyze verify failed"
fi
