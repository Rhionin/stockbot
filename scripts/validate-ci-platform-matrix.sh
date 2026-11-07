#!/usr/bin/env bash
set -euo pipefail
exec python3 "$(cd "$(dirname "$0")" && pwd)/validate_ci_platform_matrix.py"
