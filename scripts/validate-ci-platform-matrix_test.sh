#!/usr/bin/env bash
# Exercises ci_cross_platform_go_builds_enabled and live ci.yml contracts.

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck source=scripts/validate-ci-platform-matrix.sh
source "${ROOT}/scripts/validate-ci-platform-matrix.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_enabled() {
  local event="$1" input="$2"
  if ! ci_cross_platform_go_builds_enabled "$event" "$input"; then
    fail "expected enabled for event=$event input=$input"
  fi
}

assert_disabled() {
  local event="$1" input="$2"
  if ci_cross_platform_go_builds_enabled "$event" "$input"; then
    fail "expected disabled for event=$event input=$input"
  fi
}

# Default PR path: never cross-platform
assert_disabled pull_request false
assert_disabled pull_request true
assert_disabled pull_request ''

# workflow_dispatch / workflow_call / push (release) with input off
assert_disabled workflow_dispatch false
assert_disabled workflow_dispatch ''
assert_disabled workflow_call false
assert_disabled push false

# Explicit opt-in
assert_enabled workflow_dispatch true
assert_enabled workflow_call true
assert_enabled push true

# Unknown events stay off
assert_disabled schedule true
assert_disabled workflow_run true

# Live workflow files
bash "${ROOT}/scripts/validate-ci-platform-matrix.sh"

echo "validate-ci-platform-matrix_test ok"
