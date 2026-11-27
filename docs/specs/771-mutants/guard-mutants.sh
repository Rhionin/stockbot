#!/usr/bin/env bash
# Mutant check for the #771 in_group guard. Run from the repo root:
#   bash docs/specs/771-mutants/guard-mutants.sh
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
exec python3 docs/specs/771-mutants/guard-mutants.py