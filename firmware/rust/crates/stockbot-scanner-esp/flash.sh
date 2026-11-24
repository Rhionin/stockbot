#!/usr/bin/env bash
# Flash a release build to a connected board and open the serial monitor.
#
# Usage: ./flash.sh [target]     (default: sticks3_u173)
#
# Build first: cargo build --release --features <target>
# The bootloader is the one esp-idf-sys built from sdkconfig.defaults (it has app rollback
# switched on), not espflash's bundled bootloader. This command is unverified until the first
# bench run (#740).
set -euo pipefail

target="${1:-sticks3_u173}"
cd "$(dirname "${BASH_SOURCE[0]}")"

release_dir="target/xtensa-esp32s3-espidf/release"
elf="$release_dir/stockbot-scanner-esp"

if [[ ! -f "$elf" ]]; then
  echo "flash.sh: $elf not found; run: cargo build --release --features $target" >&2
  exit 1
fi
if ! grep -aq "stockbot-fw-target:${target}" "$elf"; then
  echo "flash.sh: $elf was not built with --features $target" >&2
  exit 1
fi

mapfile -t bootloaders < <(find "$release_dir/build" -name bootloader.bin -path '*esp-idf-sys*' 2>/dev/null)
if [[ ${#bootloaders[@]} -ne 1 ]]; then
  echo "flash.sh: expected exactly one esp-idf-sys bootloader.bin under $release_dir/build, found ${#bootloaders[@]}:" >&2
  printf '  %s\n' "${bootloaders[@]}" >&2
  exit 1
fi

exec espflash flash \
  --bootloader "${bootloaders[0]}" \
  --partition-table partitions_8mb_ota.csv \
  --monitor \
  "$elf"
