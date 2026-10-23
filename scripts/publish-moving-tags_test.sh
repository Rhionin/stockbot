#!/usr/bin/env bash
# Fake git and docker. Covers the tip match (exact retag lines), a stale
# skip, a tip that differs only in the last character, a failed ls-remote,
# empty ls-remote output, pipefail on that pipeline, and a pantry retag
# that fails after the sidecar has already moved.

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
if [ "$#" -ne 3 ] || [ "$1" != "ls-remote" ] || [ "$2" != "origin" ] || [ "$3" != "refs/heads/master" ]; then
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
set -euo pipefail
printf '%s\n' "docker $*" >> "${DOCKER_LOG:?}"
if [ -n "${FAKE_DOCKER_FAIL_ON:-}" ]; then
  n=$(wc -l < "${DOCKER_LOG}")
  if [ "$n" -eq "${FAKE_DOCKER_FAIL_ON}" ]; then
    echo "fake docker refused" >&2
    # Not 1. The publisher has to propagate this status; exit 1 in the trap would hide the mutant.
    exit 7
  fi
fi
EOF
chmod +x "$tmp/bin/git" "$tmp/bin/docker"

SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
OTHER=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
# Same through character 39. Any shorter compare would treat this tip as
# the commit being published.
PREFIX="${SHA:0:39}b"
if [ "${#SHA}" -ne 40 ] || [ "${#PREFIX}" -ne 40 ] || [ "${PREFIX:0:39}" != "${SHA:0:39}" ] || [ "${PREFIX:39}" = "${SHA:39}" ]; then
  fail "prefix fixture"
fi

SPLIT_MSG="Moved pantry-grocy-sync :latest and :master to ${SHA}, then the pantry tag move failed. pantry :latest and :master may be partially moved, so the two images may be on different versions."

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
    fail "$name moved tags: $(cat "$dir/docker")"
  fi
}

sidecar_cmd() {
  printf 'docker buildx imagetools create --tag ghcr.io/rhionin/pantry-grocy-sync:latest --tag ghcr.io/rhionin/pantry-grocy-sync:master ghcr.io/rhionin/pantry-grocy-sync:%s' "$SHA"
}

pantry_cmd() {
  printf 'docker buildx imagetools create --tag ghcr.io/rhionin/pantry:latest --tag ghcr.io/rhionin/pantry:master ghcr.io/rhionin/pantry:%s' "$SHA"
}

assert_exact_publish() {
  local dir="$1" name="$2"
  local lines
  lines=$(grep -c '' "$dir/docker" || true)
  if [ "$lines" -ne 2 ]; then
    fail "$name ran ${lines} docker commands, want 2: $(cat "$dir/docker")"
  fi
  if [ "$(sed -n '1p' "$dir/docker")" != "$(sidecar_cmd)" ]; then
    fail "$name sidecar command: $(sed -n '1p' "$dir/docker")"
  fi
  if [ "$(sed -n '2p' "$dir/docker")" != "$(pantry_cmd)" ]; then
    fail "$name pantry command: $(sed -n '2p' "$dir/docker")"
  fi
}

assert_skip() {
  local dir="$1" name="$2" tip="$3"
  grep -q '^publish=false$' "$dir/output" || fail "$name published"
  assert_no_docker "$dir" "$name"
  grep -F -q "::notice::${SHA} is not the tip of master (${tip})." "$dir/stdout" || fail "$name notice"
  grep -F -q 'leaving :latest and :master unchanged' "$dir/stdout" || fail "$name notice omitted the skip"
  grep -F -q "If ${tip}'s run did not publish, re-run it." "$dir/stdout" || fail "$name notice omitted recovery"
  grep -F -q 'leaving :latest and :master unchanged' "$dir/summary" || fail "$name summary"
  grep -F -q "If ${tip}'s run did not publish, re-run it." "$dir/summary" || fail "$name summary omitted recovery"
  if grep -F -q "$SPLIT_MSG" "$dir/summary"; then
    fail "$name wrote the partial-move summary"
  fi
}

export FAKE_LS_FAIL=0
export FAKE_LS_STDOUT=
export FAKE_TIP="$SHA"
export FAKE_DOCKER_FAIL_ON=
match=$(run_case match 0)
grep -q '^publish=true$' "$match/output" || fail "tip match did not publish"
grep -q '::notice::Moved :latest and :master to '"$SHA" "$match/stdout" || fail "tip match notice"
grep -q 'Moved :latest and :master to '"$SHA" "$match/summary" || fail "tip match summary"
assert_exact_publish "$match" match
if grep -F -q "$SPLIT_MSG" "$match/summary"; then
  fail "tip match wrote the partial-move summary"
fi

export FAKE_TIP="$PREFIX"
truncated=$(run_case truncated 0)
assert_skip "$truncated" truncated "$PREFIX"

export FAKE_TIP="$OTHER"
stale=$(run_case stale 0)
assert_skip "$stale" stale "$OTHER"

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

# The sidecar command is attempted and fails. Pantry has not moved, so the
# summary must not claim a partial move.
export FAKE_LS_FAIL=0
export FAKE_LS_STDOUT=
export FAKE_TIP="$SHA"
export FAKE_DOCKER_FAIL_ON=1
sidecar_fail=$(run_case sidecar-fail 7)
if [ "$(grep -c '' "$sidecar_fail/docker" || true)" -ne 1 ] || [ "$(sed -n '1p' "$sidecar_fail/docker")" != "$(sidecar_cmd)" ]; then
  fail "sidecar failure did not stop after the sidecar command: $(cat "$sidecar_fail/docker")"
fi
if grep -F -q "$SPLIT_MSG" "$sidecar_fail/summary" || grep -F -q "$SPLIT_MSG" "$sidecar_fail/stderr"; then
  fail "sidecar failure wrote the partial-move summary"
fi
if grep -q '^publish=' "$sidecar_fail/output"; then
  fail "sidecar failure wrote a publish output"
fi

export FAKE_DOCKER_FAIL_ON=2
pantry_fail=$(run_case pantry-fail 7)
grep -F -q "$SPLIT_MSG" "$pantry_fail/summary" || fail "pantry failure omitted the partial-move summary: $(cat "$pantry_fail/summary")"
grep -F -q "$SPLIT_MSG" "$pantry_fail/stderr" || fail "pantry failure omitted the partial-move log"
grep -F -q "Moved pantry-grocy-sync :latest and :master to ${SHA}," "$pantry_fail/summary" || fail "pantry failure omitted the sidecar commit"
grep -F -q "Moved pantry-grocy-sync :latest and :master to ${SHA}," "$pantry_fail/stderr" || fail "pantry failure log omitted the sidecar commit"
if grep -q '^publish=' "$pantry_fail/output"; then
  fail "pantry failure wrote a publish output"
fi
if grep -q 'Moved :latest and :master to '"$SHA" "$pantry_fail/summary"; then
  fail "pantry failure recorded a successful move"
fi
assert_exact_publish "$pantry_fail" pantry-fail

echo "publish-moving-tags_test ok"
