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
if grep -q '^Timezone=' "$unit"; then
  fail "Timezone= is not a timer key"
fi

command -v systemd-analyze >/dev/null 2>&1 || fail "systemd-analyze is required"

# 2026-11-01 is the US fall-back. 03:30 Denver is 09:30Z the day before
# and 10:30Z on the morning the clocks change.
before=$(systemd-analyze calendar --base-time=2026-10-31 '*-*-* 03:30:00 America/Denver')
after=$(systemd-analyze calendar --base-time=2026-11-01 '*-*-* 03:30:00 America/Denver')
printf '%s\n' "$before" | grep -q '2026-10-31 09:30:00 UTC' || fail "31 Oct did not resolve to 09:30Z: $before"
printf '%s\n' "$after" | grep -q '2026-11-01 10:30:00 UTC' || fail "1 Nov did not resolve to 10:30Z: $after"

verify=$(systemd-analyze verify "$unit" "$ROOT/deploy/systemd/pantry-backup.service" 2>&1 || true)
if printf '%s\n' "$verify" | grep -q 'Unknown key'; then
  printf '%s\n' "$verify" >&2
  fail "systemd-analyze verify reported an unknown key"
fi
