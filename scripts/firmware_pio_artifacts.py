#!/usr/bin/env python3
"""PlatformIO firmware build artifacts: ESP32 image header and partition checks."""

from __future__ import annotations

import argparse
import re
import struct
import sys
from dataclasses import dataclass
from pathlib import Path

IMAGE_MAGIC = 0xE9
ESP32_S3_CHIP_ID = 0x09
IMAGE_HEADER_CHIP_ID_OFFSET = 12

# esp_image_header_t byte 2 — SPI flash mode (esptool FLASH_MODES).
FLASH_MODE_BY_CODE: dict[int, str] = {
    0: "qio",
    1: "qout",
    2: "dio",
    3: "dout",
}

# ESP32 / ESP32-S3 image header byte 3: high nibble flash size (esp-idf esp_image_flash_size_t).
ESP32_FLASH_SIZE_BY_NIBBLE: dict[int, str] = {
    0x0: "1MB",
    0x1: "2MB",
    0x2: "4MB",
    0x3: "8MB",
    0x4: "16MB",
    0x5: "32MB",
    0x6: "64MB",
    0x7: "128MB",
}

# ESP32-S3 image header byte 3: low nibble SPI speed (esp-idf).
ESP32_S3_FLASH_FREQUENCY_BY_NIBBLE: dict[int, str] = {
    0x0: "40MHz",
    0x1: "26MHz",
    0x2: "20MHz",
    0xF: "80MHz",
}

REQUIRED_BUILD_BINS = ("bootloader.bin", "partitions.bin", "firmware.bin")

APP_TYPE = 0x00
DATA_TYPE = 0x01

PARTITION_TYPES = {
    "app": APP_TYPE,
    "data": DATA_TYPE,
}

PARTITION_SUBTYPES: dict[int, dict[str, int]] = {
    APP_TYPE: {
        "factory": 0x00,
        "test": 0x20,
    },
    DATA_TYPE: {
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

for _ota_slot in range(16):
    PARTITION_SUBTYPES[APP_TYPE][f"ota_{_ota_slot}"] = 0x10 + _ota_slot

MD5_PARTITION_MAGIC = b"\xeb\xeb"
PARTITION_ENTRY_MAGIC = b"\xaa\x50"
PARTITION_STRUCT = struct.Struct("<2sBBII16sI")


@dataclass(frozen=True)
class BootloaderFlashInfo:
    flash_mode: str
    flash_size: str
    flash_frequency: str

    def as_flash_info_lines(self, target_id: str, env_name: str) -> list[str]:
        return [
            f"target={target_id}",
            f"env={env_name}",
            f"flash_mode={self.flash_mode}",
            f"flash_frequency={self.flash_frequency}",
            f"flash_size={self.flash_size}",
        ]


@dataclass(frozen=True)
class PartitionRow:
    name: str
    ptype: int
    subtype: int
    offset: int
    size: int

    def as_tuple(self) -> tuple[str, int, int, int, int]:
        return (self.name, self.ptype, self.subtype, self.offset, self.size)


class FirmwarePioArtifactsError(Exception):
    pass


def parse_bootloader_image_header(data: bytes) -> BootloaderFlashInfo:
    if len(data) < 4:
        raise FirmwarePioArtifactsError("bootloader image too short for ESP32 header")
    magic, _segments, spi_mode, spi_speed_size = data[0], data[1], data[2], data[3]
    if magic != IMAGE_MAGIC:
        raise FirmwarePioArtifactsError(
            f"invalid ESP32 image magic 0x{magic:02x} (expected 0x{IMAGE_MAGIC:02x})"
        )
    if spi_mode not in FLASH_MODE_BY_CODE:
        raise FirmwarePioArtifactsError(f"unknown flash mode code 0x{spi_mode:02x}")
    size_nibble = (spi_speed_size >> 4) & 0x0F
    freq_nibble = spi_speed_size & 0x0F
    if size_nibble not in ESP32_FLASH_SIZE_BY_NIBBLE:
        raise FirmwarePioArtifactsError(f"unknown flash size nibble 0x{size_nibble:x}")
    if freq_nibble not in ESP32_S3_FLASH_FREQUENCY_BY_NIBBLE:
        raise FirmwarePioArtifactsError(f"unknown flash frequency nibble 0x{freq_nibble:x}")
    if len(data) > IMAGE_HEADER_CHIP_ID_OFFSET:
        chip_id = data[IMAGE_HEADER_CHIP_ID_OFFSET]
        if chip_id != ESP32_S3_CHIP_ID:
            raise FirmwarePioArtifactsError(
                f"unexpected chip_id 0x{chip_id:02x} "
                f"(expected ESP32-S3 0x{ESP32_S3_CHIP_ID:02x})"
            )
    return BootloaderFlashInfo(
        flash_mode=FLASH_MODE_BY_CODE[spi_mode],
        flash_size=ESP32_FLASH_SIZE_BY_NIBBLE[size_nibble],
        flash_frequency=ESP32_S3_FLASH_FREQUENCY_BY_NIBBLE[freq_nibble],
    )


def _parse_int(value: str) -> int:
    value = value.strip()
    if not value:
        raise FirmwarePioArtifactsError("empty numeric field")
    return int(value, 0)


def _parse_partition_type(value: str) -> int:
    value = value.strip()
    if value in PARTITION_TYPES:
        return PARTITION_TYPES[value]
    return _parse_int(value)


def _parse_partition_subtype(ptype: int, value: str) -> int:
    value = value.strip()
    if not value:
        if ptype == APP_TYPE:
            raise FirmwarePioArtifactsError("app partition subtype cannot be empty")
        return PARTITION_SUBTYPES[DATA_TYPE]["undefined"]
    subtypes = PARTITION_SUBTYPES.get(ptype, {})
    if value in subtypes:
        return subtypes[value]
    return _parse_int(value)


def load_partition_csv(csv_path: Path) -> list[PartitionRow]:
    rows: list[PartitionRow] = []
    for line_no, raw in enumerate(csv_path.read_text().splitlines(), start=1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        fields = [part.strip() for part in line.split(",")]
        if len(fields) < 5:
            raise FirmwarePioArtifactsError(
                f"{csv_path}:{line_no}: expected at least 5 columns"
            )
        name = fields[0]
        ptype = _parse_partition_type(fields[1])
        subtype = _parse_partition_subtype(ptype, fields[2])
        offset = _parse_int(fields[3])
        size = _parse_int(fields[4])
        rows.append(PartitionRow(name, ptype, subtype, offset, size))
    return rows


def decode_partition_table_binary(data: bytes) -> list[PartitionRow]:
    rows: list[PartitionRow] = []
    pos = 0
    end = len(data)
    while pos + 32 <= end:
        chunk = data[pos : pos + 32]
        pos += 32
        if chunk == b"\xff" * 32:
            break
        if chunk[:2] == MD5_PARTITION_MAGIC:
            continue
        magic, ptype, subtype, offset, size, name_raw, _flags = PARTITION_STRUCT.unpack(
            chunk
        )
        if magic != PARTITION_ENTRY_MAGIC:
            raise FirmwarePioArtifactsError(
                f"invalid partition entry magic at offset {pos - 32}"
            )
        name = name_raw.split(b"\x00", 1)[0].decode()
        rows.append(PartitionRow(name, ptype, subtype, offset, size))
    if not rows:
        raise FirmwarePioArtifactsError("partition table is empty")
    return rows


def partitions_match_binary_csv(partitions_bin: Path, csv_path: Path) -> None:
    expected = load_partition_csv(csv_path)
    actual = decode_partition_table_binary(partitions_bin.read_bytes())
    if [row.as_tuple() for row in expected] != [row.as_tuple() for row in actual]:
        raise FirmwarePioArtifactsError(
            f"{partitions_bin} does not match {csv_path} "
            f"(expected {len(expected)} entries, got {len(actual)})"
        )


def resolve_build_bins(build_dir: Path) -> dict[str, Path]:
    found: dict[str, Path] = {}
    missing: list[str] = []
    for name in REQUIRED_BUILD_BINS:
        path = build_dir / name
        if path.is_file():
            found[name] = path
        else:
            missing.append(name)
    if missing:
        raise FirmwarePioArtifactsError(
            f"missing PlatformIO outputs in {build_dir}: {', '.join(missing)}"
        )
    return found


def write_flash_info(
    dest: Path, target_id: str, env_name: str, bootloader_bin: Path
) -> BootloaderFlashInfo:
    info = parse_bootloader_image_header(bootloader_bin.read_bytes())
    dest.write_text(
        "\n".join(info.as_flash_info_lines(target_id, env_name)) + "\n",
        encoding="utf-8",
    )
    return info


def prepare_artifact_dir(
    build_dir: Path,
    output_dir: Path,
    target_id: str,
    env_name: str,
) -> None:
    if not re.fullmatch(r"[a-z0-9_]+", target_id):
        raise FirmwarePioArtifactsError(f"invalid target id {target_id!r}")
    bins = resolve_build_bins(build_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    for name, src in bins.items():
        dest = output_dir / name
        dest.write_bytes(src.read_bytes())
    write_flash_info(output_dir / "flash-info.txt", target_id, env_name, bins["bootloader.bin"])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)

    verify = sub.add_parser("verify-partitions")
    verify.add_argument("--partitions", type=Path, required=True)
    verify.add_argument("--expected", type=Path, required=True)

    prepare = sub.add_parser("prepare")
    prepare.add_argument("--build-dir", type=Path, required=True)
    prepare.add_argument("--output-dir", type=Path, required=True)
    prepare.add_argument("--target", required=True)
    prepare.add_argument("--env", required=True)

    args = parser.parse_args(argv)
    try:
        if args.command == "verify-partitions":
            partitions_match_binary_csv(args.partitions, args.expected)
        elif args.command == "prepare":
            prepare_artifact_dir(
                args.build_dir, args.output_dir, args.target, args.env
            )
    except FirmwarePioArtifactsError as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())