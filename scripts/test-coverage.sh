#!/bin/bash
set -euo pipefail

# Minimum required line coverage percentage.
# 85.1 is the clean ./... total after stage-1 sign-in. :8080 stays open
# until the household password is retired, and the added handler tests
# cover the cart, scan, and sync branches that the fresh database reaches.
# A cached go test run can report a different total. Do not ratchet from that
# result when the number looks like a cache artifact.
COVERAGE_THRESHOLD=85.1

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

cover_profile_path() {
    echo "${STOCKBOT_COVERAGE_PROFILE:-coverage.out}"
}

fail_coverage_parse() {
    local msg="$1"
    echo -e "${RED}ERROR: ${msg}${NC}" >&2
    exit 1
}

parse_coverage_from_cover_func() {
    local cover_tmp="$1"
    local cover_status="$2"

    if [ "$cover_status" -ne 0 ]; then
        if [ -s "$cover_tmp" ]; then
            cat "$cover_tmp" >&2
        fi
        fail_coverage_parse "go tool cover -func failed (exit ${cover_status})"
    fi

    local total_line
    total_line=$(grep '^total:' "$cover_tmp" | tail -1 || true)
    if [ -z "$total_line" ]; then
        if [ -s "$cover_tmp" ]; then
            cat "$cover_tmp" >&2
        fi
        fail_coverage_parse "go tool cover -func output has no total: line"
    fi

    local pct
    pct=$(echo "$total_line" | awk '{print $3}' | sed 's/%//')
    if ! [[ $pct =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
        fail_coverage_parse "could not parse total coverage from: ${total_line}"
    fi
    echo "$pct"
}

run_cover_func_to_file() {
    local profile="$1"
    local out="$2"

    if [ -n "${STOCKBOT_COVER_FUNC_OUTPUT+x}" ]; then
        printf '%s\n' "$STOCKBOT_COVER_FUNC_OUTPUT" >"$out"
        return 0
    fi

    if [ -n "${STOCKBOT_COVER_FUNC_CMD:-}" ]; then
        bash -c "$STOCKBOT_COVER_FUNC_CMD" >"$out" 2>&1
        return $?
    fi

    go tool cover -func="$profile" >"$out" 2>&1
}

echo "Running tests with coverage..."
echo "Coverage threshold: ${COVERAGE_THRESHOLD}%"
echo ""

if [ -z "${STOCKBOT_COVERAGE_SKIP_GO_TEST:-}" ]; then
    # Third-party Go under frontend/node_modules (flatted ships a Go port) is not
    # stockbot code. Leaving it in ./... drops the baseline whenever node_modules exists.
    mapfile -t PKGS < <(go list ./... | grep -v '/node_modules/')
    COVERPKG=$(IFS=,; echo "${PKGS[*]}")
    go test -cover -coverpkg="${COVERPKG}" -coverprofile=coverage.out "${PKGS[@]}"
else
    echo "(STOCKBOT_COVERAGE_SKIP_GO_TEST: skipping go test)"
fi

profile=$(cover_profile_path)
cover_tmp=$(mktemp)
trap 'rm -f "$cover_tmp"' EXIT

set +e
run_cover_func_to_file "$profile" "$cover_tmp"
cover_status=$?
set -e

COVERAGE=$(parse_coverage_from_cover_func "$cover_tmp" "$cover_status")

echo ""
echo "================================"
echo "Total coverage: ${COVERAGE}%"
echo "Required:       ${COVERAGE_THRESHOLD}%"
echo "================================"

# Compare coverage to threshold
if awk "BEGIN {exit !($COVERAGE < $COVERAGE_THRESHOLD)}"; then
    echo -e "${RED}FAIL: Coverage ${COVERAGE}% is below threshold ${COVERAGE_THRESHOLD}%${NC}"
    exit 1
else
    echo -e "${GREEN}PASS: Coverage ${COVERAGE}% meets or exceeds threshold${NC}"

    # If the codified threshold has fallen more than 0.5% behind actual
    # coverage, ratchet it up. Small fluctuations (within 0.5%) are left alone
    # so coverage can wobble during development without tripping the ratchet.
    if awk "BEGIN {exit !(($COVERAGE - $COVERAGE_THRESHOLD) > 0.5)}"; then
        echo ""
        echo -e "${CYAN}Coverage is more than 0.5% ahead of the threshold! Updating threshold from ${COVERAGE_THRESHOLD}% to ${COVERAGE}%${NC}"

        # Get the directory where this script is located
        SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
        SCRIPT_PATH="${STOCKBOT_COVERAGE_SELF:-${SCRIPT_DIR}/test-coverage.sh}"

        # Update the threshold in the script file
        sed -i.bak "s/^COVERAGE_THRESHOLD=.*/COVERAGE_THRESHOLD=${COVERAGE}/" "$SCRIPT_PATH"
        rm -f "${SCRIPT_PATH}.bak"

        echo -e "${GREEN}Threshold updated in ${SCRIPT_PATH}${NC}"
        echo -e "${YELLOW}Remember to commit this change to lock in the new coverage baseline${NC}"
    fi
fi

# Clean up
rm -f "$(cover_profile_path)"