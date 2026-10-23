#!/usr/bin/env bash
# Fake git and docker. Covers the tip match, a stale skip, a failed
# ls-remote, empty ls-remote output, and pipefail on that pipeline.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT="${ROOT}/scripts/publish-moving-tags.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -q -F 'set -euo pipefail' "$SCRIPT" || fail "publisher must keep pipefail"
if grep -q -F 'git fetch' "$SCRIPT"; then
  fail "publisher must read the tip with ls-remote"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/bin"

cat > "$tmp/bin/git" << 'EOF'
#!/bin/bash
set -euo pipefail
if [ "${1:-}" != "ls-remote" ]; then
  echo "unexpected git: $*" >&2
  exit 1
fi
if [ "${FAKE_LS_FAIL:-}" = 1 ]; then
  if [ -n "${FAKE_LS_STDOUT:-}" ]; then
    printf '%s\n' "${FAKE_LS_STDOUT}"
  fi
  exit 1
fi
if [ -n "${FAKE_TIP:-}" ]; then
  printf '%s\trefs/heads/master\n' "${FAKE_TIP}"
fi
exit 0
EOF

cat > "$tmp/bin/docker" << 'EOF'
#!/bin/bash
printf '%s\n' "$*" >> "${DOCKER_LOG:?}"
EOF
chmod +x "$tmp/bin/git" "$tmp/bin/docker"

SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
OTHER=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

run_case() {
  local name="$1" expect="$2"
  local dir="$tmp/cases/$name"
  mkdir -p "$dir"
  export GITHUB_SHA="$SHA"
  export GITHUB_OUTPUT="$dir/output"
  export GITHUB_STEP_SUMMARY="$dir/summary"
  export DOCKER_LOG="$dir/docker"
  : > "$GITHUB_OUTPUT"
  : > "$GITHUB_STEP_SUMMARY"
  : > "$DOCKER_LOG"
  set +e
  PATH="$tmp/bin:${PATH}" bash "$SCRIPT" > "$dir/stdout" 2> "$dir/stderr"
  local code=$?
  set -e
  if [ "$code" -ne "$expect" ]; then
    echo "stdout: $(cat "$dir/stdout")" >&2
    echo "stderr: $(cat "$dir/stderr")" >&2
    fail "$name exited $code, want $expect"
  fi
  printf '%s' "$dir"
}

assert_no_docker() {
  local dir="$1" name="$2"
  if [ -s "$dir/docker" ]; then
    fail "$name moved tags"
  fi
}

export FAKE_LS_FAIL=0
export FAKE_LS_STDOUT=
export FAKE_TIP="$SHA"
match=$(run_case match 0)
grep -q '^publish=true$' "$match/output" || fail "tip match did not publish"
grep -q '::notice::Moved :latest and :master to '"$SHA" "$match/stdout" || fail "tip match notice"
grep -q 'Moved :latest and :master to '"$SHA" "$match/summary" || fail "tip match summary"
first=$(sed -n '1p' "$match/docker")
second=$(sed -n '2p' "$match/docker")
case "$first" in
  *pantry-grocy-sync:latest*) ;;
  *) fail "grocy-sync tag was not first: $first" ;;
esac
case "$second" in
  *ghcr.io/rhionin/pantry:latest*) ;;
  *) fail "pantry tag was not second: $second" ;;
esac

export FAKE_TIP="$OTHER"
stale=$(run_case stale 0)
grep -q '^publish=false$' "$stale/output" || fail "stale commit published"
assert_no_docker "$stale" stale
grep -q '::notice::'"$SHA"' is not the tip of master ('"$OTHER"')' "$stale/stdout" || fail "stale notice"
grep -q 'leaving :latest and :master unchanged' "$stale/stdout" || fail "stale notice omitted the skip"
grep -q 'Re-run the master workflow for '"$OTHER" "$stale/stdout" || fail "stale notice omitted recovery"
grep -q 'leaving :latest and :master unchanged' "$stale/summary" || fail "stale summary"
grep -q 'Re-run the master workflow for '"$OTHER" "$stale/summary" || fail "stale summary omitted recovery"

export FAKE_TIP=
export FAKE_LS_FAIL=1
export FAKE_LS_STDOUT=
lsfail=$(run_case lsfail 1)
assert_no_docker "$lsfail" lsfail
if grep -q '^publish=' "$lsfail/output"; then
  fail "failed ls-remote wrote a publish output"
fi

export FAKE_LS_FAIL=0
empty=$(run_case empty 1)
assert_no_docker "$empty" empty
grep -q 'Could not read refs/heads/master' "$empty/stderr" || fail "empty ls-remote was silent"
if grep -q '^publish=' "$empty/output"; then
  fail "empty ls-remote wrote a publish output"
fi

# ls-remote prints the matching tip and then fails. Without pipefail the
# pipeline would succeed and this commit would move :latest.
export FAKE_LS_FAIL=1
export FAKE_LS_STDOUT="${SHA}"$'\t'"refs/heads/master"
pipe=$(run_case pipefail 1)
assert_no_docker "$pipe" pipefail

echo "publish-moving-tags_test ok"
