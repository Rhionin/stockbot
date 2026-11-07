#!/usr/bin/env bash
# Static contract for Linux-default Go platform builds in ci.yml:
# ubuntu always; macOS/Windows only when go_cross_platform is set on
# workflow_dispatch or workflow_call (e.g. release on push).

set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

CI=.github/workflows/ci.yml
RELEASE=.github/workflows/release.yml

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

have() {
  local file="$1" needle="$2" label="$3"
  if ! grep -q -F -- "$needle" "$file"; then
    fail "$label (missing $needle in $file)"
  fi
}

# --- Event/input semantics mirrored for tests (validate-ci-platform-matrix_test.sh) ---
# Returns 0 when macOS/Windows Go build jobs should run.
ci_cross_platform_go_builds_enabled() {
  local event="${1:-}"
  local input="${2:-false}"
  if [ "$event" = "pull_request" ]; then
    return 1
  fi
  case "$input" in
    true | 1 | yes) ;;
    *) return 1 ;;
  esac
  case "$event" in
    workflow_dispatch | workflow_call | push) return 0 ;;
    *) return 1 ;;
  esac
}

run_validate_ci_platform_matrix() {
if [ ! -f "$CI" ]; then
  fail "missing $CI"
fi

have "$CI" 'go-build-linux:' "dedicated Linux Go build job"
have "$CI" 'go-build-macos:' "optional macOS Go build job"
have "$CI" 'go-build-windows:' "optional Windows Go build job"

if grep -q 'go-build-all-platforms:' "$CI"; then
  fail "go-build-all-platforms matrix must be split (Linux default + opt-in jobs)"
fi

if awk '/^  go-build-linux:/{f=1} f&&/^    if:/{found=1; exit} f&&/^  [a-zA-Z0-9_-]+:/&&!/^  go-build-linux:/{exit} END{exit !found}' "$CI"; then
  fail "go-build-linux must always run (no job-level if:)"
fi

opt_in_if='github.event_name != '\''pull_request'\'' &&'
have "$CI" "$opt_in_if" "macOS/Windows gated off pull_request before reading inputs"

have "$CI" 'inputs.go_cross_platform == true' "opt-in uses go_cross_platform input"
have "$CI" 'go_cross_platform:' "workflow defines go_cross_platform input"

if grep -q 'os: \[ubuntu-latest, macos-latest, windows-latest\]' "$CI"; then
  fail "three-OS matrix must not run by default on every trigger"
fi

have "$RELEASE" 'uses: ./.github/workflows/ci.yml' "release still gates on ci.yml"

docker_needs=$(
  awk '
    /^  docker-publish:/ { in_job = 1; next }
    in_job && /^    needs:/ { sub(/^    needs: /, ""); print; exit }
  ' "$CI"
)
for token in go-build-linux go-build-macos go-build-windows go-build-all-platforms; do
  if printf '%s' "$docker_needs" | grep -q "$token"; then
    fail "docker-publish must not wait on optional Go platform builds (found $token)"
  fi
done

if ! printf '%s' "$docker_needs" | grep -q 'go'; then
  fail "docker-publish must still need go tests job"
fi

echo "validate-ci-platform-matrix ok"
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  run_validate_ci_platform_matrix
fi
