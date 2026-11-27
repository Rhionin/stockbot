#!/usr/bin/env python3
"""Generate a 3072-byte ESP-IDF partition table from CSV (gen_esp32part.py equivalent).

Used in Pester BeforeAll / CI to assert firmware/partitions_8mb_ota.csv still
compiles to the committed reference bytes (8b3183d3…3dc1).
"""

from __future__ import annotations

import hashlib
import struct
import sys
from pathlib import Path

PARTITION_TYPES = {"app": 0x00, "data": 0x01}
SUBTYPES: dict[int, dict[str, int]] = {
    0x00: {"factory": 0x00, "test": 0x20},
    0x01: {
        "ota": 0x00,
        "phy": 0x01,
        "nvs": 0x02,
        "coredump": 0x03,
        "nvs_keys": 0x04,
        "efuse": 0x05,
        "undefined": 0x06,
        "esphttpd": 0x80,
        "fat": 0x81,
        "spiffs": 0x82,
    },
}
for i in range(16):
    SUBTYPES[0x00][f"ota_{i}"] = 0x10 + i

TABLE_LEN = 0xC00  # 3072


def parse_csv(path: Path) -> list[tuple[str, int, int, int, int]]:
    rows: list[tuple[str, int, int, int, int]] = []
    for raw in path.read_text().splitlines():
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        fields = [part.strip() for part in line.split(",")]
        if len(fields) < 5:
            raise ValueError(f"{path}: expected 5 columns: {line!r}")
        name = fields[0]
        ptype = PARTITION_TYPES[fields[1]]
        subtype = SUBTYPES[ptype][fields[2]]
        offset = int(fields[3], 0)
        size = int(fields[4], 0)
        rows.append((name, ptype, subtype, offset, size))
    return rows


def encode_partition_table(rows: list[tuple[str, int, int, int, int]]) -> bytes:
    buf = bytearray()
    for name, ptype, subtype, off, size in rows:
        name_b = name.encode("ascii")[:16].ljust(16, b"\x00")
        buf += struct.pack("<2sBBII16sI", b"\xaa\x50", ptype, subtype, off, size, name_b, 0)
    # MD5 digest covers partition rows only (matches atoms3lite-arduino-2.0.17 reference).
    md5 = hashlib.md5(buf).digest()
    md5_row = bytearray(b"\xeb\xeb")
    md5_row.extend(b"\xff" * (32 - 2 - len(md5)))
    md5_row.extend(md5)
    buf += bytes(md5_row[:32])
    if len(buf) < TABLE_LEN:
        buf += b"\xff" * (TABLE_LEN - len(buf))
    return bytes(buf[:TABLE_LEN])


def write_default_8mb_style(path: Path) -> bytes:
    """Classic dual-OTA layout with app0 at 0x10000 (test fixture only)."""
    rows = [
        ("nvs", 0x01, 0x02, 0x9000, 0x5000),
        ("otadata", 0x01, 0x00, 0xe000, 0x2000),
        ("app0", 0x00, 0x10, 0x10000, 0x1C0000),
        ("app1", 0x00, 0x11, 0x1D0000, 0x1C0000),
        ("spiffs", 0x01, 0x82, 0x390000, 0x70000),
    ]
    data = encode_partition_table(rows)
    path.write_bytes(data)
    return data


def main() -> int:
    repo = Path(__file__).resolve().parents[5]
    csv_path = repo / "firmware" / "partitions_8mb_ota.csv"
    fixture_dir = Path(__file__).resolve().parent
    ota_bin = encode_partition_table(parse_csv(csv_path))
    (fixture_dir / "partitions_8mb_ota.bin").write_bytes(ota_bin)
    write_default_8mb_style(fixture_dir / "partitions_default_8mb.bin")
    print(hashlib.sha256(ota_bin).hexdigest())
    return 0


if __name__ == "__main__":
    sys.exit(main())