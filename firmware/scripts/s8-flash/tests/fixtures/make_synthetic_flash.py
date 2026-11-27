#!/usr/bin/env python3
"""Build synthetic 8 MiB flash images for Pester (no hardware)."""

from __future__ import annotations

import struct
import zlib
from pathlib import Path

FLASH_SIZE = 0x800_000
TABLE_OFF = 0x8000


def esp_rom_crc32_le(initial: int, data: bytes) -> int:
    crc = ~initial & 0xFFFFFFFF
    for b in data:
        crc ^= b
        for _ in range(8):
            if crc & 1:
                crc = (crc >> 1) ^ 0xEDB88320
            else:
                crc >>= 1
    return ~crc & 0xFFFFFFFF


def ota_entry(seq: int, state: int, crc: int | None = None) -> bytes:
    row = bytearray(32)
    struct.pack_into("<I", row, 0, seq & 0xFFFFFFFF)
    struct.pack_into("<I", row, 24, state & 0xFFFFFFFF)
    if crc is None:
        crc = esp_rom_crc32_le(0xFFFFFFFF, row[0:4])
    struct.pack_into("<I", row, 28, crc & 0xFFFFFFFF)
    return bytes(row)


def make_flash(table: bytes, otadata_off: int, otadata_size: int, entries: list[tuple[int, int, int | None]]) -> bytes:
    buf = bytearray(b"\xff" * FLASH_SIZE)
    buf[0:4] = bytes([0xE9, 0x02, 0x02, 0x2F])
    buf[TABLE_OFF : TABLE_OFF + len(table)] = table
    base = otadata_off
    for i, (seq, state, crc) in enumerate(entries[:2]):
        buf[base + i * 0x1000 : base + i * 0x1000 + 32] = ota_entry(seq, state, crc)
    return bytes(buf)


def main() -> None:
    here = Path(__file__).resolve().parent
    table_ota = (here / "partitions_8mb_ota.bin").read_bytes()
    table_def = (here / "partitions_default_8mb.bin").read_bytes()
    (here / "flash_ota_erased.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, []))
    (here / "flash_ota_odd.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(1, 2, None)]))
    (here / "flash_ota_even.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(2, 2, None)]))
    (here / "flash_ota_new_stop.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(3, 0, None)]))
    (here / "flash_ota_pending_stop.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(3, 1, None)]))
    (here / "flash_ota_bad_crc.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(5, 2, 0x12345678)]))
    both = [(4, 2, None), (9, 2, None)]
    (here / "flash_ota_highest.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, both))
    (here / "flash_ota_invalid_skip.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(12, 3, None), (5, 2, None)]))
    (here / "flash_ota_aborted_skip.bin").write_bytes(make_flash(table_ota, 0xF000, 0x2000, [(12, 4, None), (5, 2, None)]))
    (here / "flash_default_8mb.bin").write_bytes(make_flash(table_def, 0xE000, 0x2000, [(1, 2, None)]))
    bad = bytearray(table_ota)
    bad[32] ^= 0x01
    (here / "flash_unknown_table.bin").write_bytes(make_flash(bytes(bad), 0xF000, 0x2000, [(1, 2, None)]))
    synth_header = bytes.fromhex(
        "e905023f28513740ee000000090000000063000000000001"
    )
    (here / "synthetic_firmware_header.bin").write_bytes(synth_header + bytes(512 - len(synth_header)))


if __name__ == "__main__":
    main()