#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

OUT="${TMPDIR:-/tmp}/stockbot_firmware_host_test"
CXX="${CXX:-g++}"

SRC=(
  src/core/text_util.cpp
  src/core/barcode_filter.cpp
  src/core/scanner_state.cpp
  src/core/ring_buffer.cpp
  src/core/display_model.cpp
  test/test_scanner_core/test_main.cpp
)

"$CXX" -std=c++17 -Wall -Wextra -Werror \
  -Isrc/core \
  "${SRC[@]}" \
  -o "$OUT"

"$OUT"
echo "All firmware host tests passed."
