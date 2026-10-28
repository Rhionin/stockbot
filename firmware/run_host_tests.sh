#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

OUT="${TMPDIR:-/tmp}/stockbot_firmware_host_test"
CXX="${CXX:-g++}"

mapfile -t CORE_SRC < <(find src/core -name '*.cpp' | sort)

"$CXX" -std=c++17 -Wall -Wextra -Werror \
  -Isrc/core -Isrc/net -Isrc/hal \
  "${CORE_SRC[@]}" \
  test/test_scanner_core/test_main.cpp \
  -o "$OUT"

"$OUT"
echo "All firmware host tests passed."
