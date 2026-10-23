#!/bin/bash
set -e

# Minimum required line coverage percentage.
# 82.6 is the clean ./... total, including the public brand mark and legal pages.
# A cached go test run, or several packages writing one profile at once, can
# report a different total. Do not ratchet from that result. -p 1 keeps the
# merged profile to a single writer.
COVERAGE_THRESHOLD=82.6

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

echo "Running tests with coverage..."
echo "Coverage threshold: ${COVERAGE_THRESHOLD}%"
echo ""

# Third-party Go under frontend/node_modules (flatted ships a Go port) is not
# stockbot code. Leaving it in ./... drops the baseline whenever node_modules exists.
mapfile -t PKGS < <(go list ./... | grep -v '/node_modules/')
COVERPKG=$(IFS=,; echo "${PKGS[*]}")
go test -p 1 -cover -coverpkg="${COVERPKG}" -coverprofile=coverage.out "${PKGS[@]}"

if [ $? -ne 0 ]; then
    echo -e "${RED}Tests failed${NC}"
    exit 1
fi

# Calculate total coverage
COVERAGE=$(go tool cover -func=coverage.out | tail -1 | awk '{print $3}' | sed 's/%//')

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
        SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
        SCRIPT_PATH="${SCRIPT_DIR}/test-coverage.sh"
        
        # Update the threshold in the script file
        sed -i.bak "s/^COVERAGE_THRESHOLD=.*/COVERAGE_THRESHOLD=${COVERAGE}/" "$SCRIPT_PATH"
        rm -f "${SCRIPT_PATH}.bak"
        
        echo -e "${GREEN}Threshold updated in ${SCRIPT_PATH}${NC}"
        echo -e "${YELLOW}Remember to commit this change to lock in the new coverage baseline${NC}"
    fi
fi

# Clean up
rm -f coverage.out
