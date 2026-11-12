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

GEN_OUT="${TMPDIR:-/tmp}/gen_device_protocol_vectors"
COMMITTED_VECTORS="$(cd "$ROOT/.." && pwd)/testdata/device-protocol"
GEN_TMP="${TMPDIR:-/tmp}/device_protocol_vectors_gen"
rm -rf "$GEN_TMP"
mkdir -p "$GEN_TMP"
"$CXX" -std=c++17 -Wall -Wextra -Werror \
  -Isrc/core -Isrc/net -Isrc/hal \
  "${CORE_SRC[@]}" \
  test/gen_device_protocol_vectors.cpp \
  -o "$GEN_OUT"
"$GEN_OUT" "$GEN_TMP"
for name in requests.json responses.json setup_qr.json api_error.json http_retry.json; do
  if ! diff -q "$COMMITTED_VECTORS/$name" "$GEN_TMP/$name" >/dev/null; then
    echo "device-protocol vector drift: $name does not match C++ generator output" >&2
    echo "Regenerate with: $GEN_OUT $COMMITTED_VECTORS" >&2
    diff -u "$COMMITTED_VECTORS/$name" "$GEN_TMP/$name" >&2 || true
    exit 1
  fi
done

echo "All firmware host tests passed."
