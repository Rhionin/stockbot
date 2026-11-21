#!/usr/bin/env python3
"""Tests for scripts/firmware_pio_artifacts.py."""

from __future__ import annotations

import struct
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from firmware_pio_artifacts import (
    ESP32_FLASH_SIZE_BY_NIBBLE,
    ESP32_S3_CHIP_ID,
    ESP32_S3_FLASH_FREQUENCY_BY_NIBBLE,
    BootloaderFlashInfo,
    FirmwarePioArtifactsError,
    IMAGE_MAGIC,
    decode_partition_table_binary,
    load_partition_csv,
    main,
    parse_bootloader_image_header,
    partitions_match_binary_csv,
    prepare_artifact_dir,
    resolve_build_bins,
    write_flash_info,
)

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = Path(__file__).resolve().parent / "firmware_pio_artifacts.py"
FIXTURES = Path(__file__).resolve().parent / "testdata" / "firmware_pio"
PARTITIONS_CSV = ROOT / "firmware" / "partitions_8mb_ota.csv"


def _image_header(mode: int, size_nibble: int, freq_nibble: int) -> bytes:
    return bytes(
        [
            IMAGE_MAGIC,
            1,
            mode,
            ((size_nibble & 0x0F) << 4) | (freq_nibble & 0x0F),
        ]
    )


def _image_header_with_chip_id(
    mode: int, size_nibble: int, freq_nibble: int, chip_id: int
) -> bytes:
    header = bytearray(_image_header(mode, size_nibble, freq_nibble))
    header.extend(b"\x00" * (IMAGE_HEADER_CHIP_ID_OFFSET + 1 - len(header)))
    header[IMAGE_HEADER_CHIP_ID_OFFSET] = chip_id & 0xFF
    return bytes(header)


def _good_partition_bytes() -> bytearray:
    return bytearray((FIXTURES / "partitions_8mb.bin").read_bytes())


def _write_mutated_partition(tmp_path: Path, **mutations: object) -> Path:
    data = _good_partition_bytes()
    if "offset" in mutations:
        struct.pack_into("<I", data, 4, int(mutations["offset"]))
    if "size" in mutations:
        struct.pack_into("<I", data, 8, int(mutations["size"]))
    if "subtype" in mutations:
        data[3] = int(mutations["subtype"])
    if "name" in mutations:
        raw = str(mutations["name"]).encode()[:16]
        data[12 : 12 + len(raw)] = raw
    if "entry_magic" in mutations:
        data[0:2] = bytes(mutations["entry_magic"])
    out = tmp_path / "partitions_mutated.bin"
    out.write_bytes(data)
    return out


IMAGE_HEADER_CHIP_ID_OFFSET = 12


class BootloaderHeaderTests(unittest.TestCase):
    def test_dio_80mhz_8mb(self) -> None:
        data = (FIXTURES / "bootloader_dio_80m_8mb.bin").read_bytes()
        info = parse_bootloader_image_header(data)
        self.assertEqual(
            info,
            BootloaderFlashInfo("dio", "8MB", "80MHz"),
        )

    def test_qio_40mhz_4mb(self) -> None:
        data = (FIXTURES / "bootloader_qio_40m_4mb.bin").read_bytes()
        info = parse_bootloader_image_header(data)
        self.assertEqual(info.flash_mode, "qio")
        self.assertEqual(info.flash_size, "4MB")
        self.assertEqual(info.flash_frequency, "40MHz")

    def test_qout_26mhz_2mb(self) -> None:
        data = (FIXTURES / "bootloader_qout_26m_2mb.bin").read_bytes()
        info = parse_bootloader_image_header(data)
        self.assertEqual(info, BootloaderFlashInfo("qout", "2MB", "26MHz"))

    def test_dout_20mhz_1mb(self) -> None:
        data = (FIXTURES / "bootloader_dout_20m_1mb.bin").read_bytes()
        info = parse_bootloader_image_header(data)
        self.assertEqual(info, BootloaderFlashInfo("dout", "1MB", "20MHz"))

    def test_rejects_short_buffer(self) -> None:
        with self.assertRaises(FirmwarePioArtifactsError):
            parse_bootloader_image_header(b"\xe9\x02")

    def test_rejects_bad_magic(self) -> None:
        bad = bytearray(_image_header(2, 3, 0xF))
        bad[0] = 0xEA
        with self.assertRaises(FirmwarePioArtifactsError) as ctx:
            parse_bootloader_image_header(bytes(bad))
        self.assertIn("invalid ESP32 image magic", str(ctx.exception))

    def test_rejects_unknown_flash_mode(self) -> None:
        with self.assertRaises(FirmwarePioArtifactsError) as ctx:
            parse_bootloader_image_header(_image_header(0x07, 3, 0xF))
        self.assertIn("unknown flash mode code 0x07", str(ctx.exception))

    def test_rejects_unknown_flash_size_nibble(self) -> None:
        with self.assertRaises(FirmwarePioArtifactsError) as ctx:
            parse_bootloader_image_header(_image_header(2, 0x08, 0x0))
        self.assertIn("unknown flash size nibble 0x8", str(ctx.exception))

    def test_rejects_unknown_flash_frequency_nibble(self) -> None:
        with self.assertRaises(FirmwarePioArtifactsError) as ctx:
            parse_bootloader_image_header(_image_header(2, 3, 0x03))
        self.assertIn("unknown flash frequency nibble 0x3", str(ctx.exception))

    def test_esp32_s3_flash_size_table(self) -> None:
        expected = {
            0x0: "1MB",
            0x1: "2MB",
            0x2: "4MB",
            0x3: "8MB",
            0x4: "16MB",
            0x5: "32MB",
            0x6: "64MB",
            0x7: "128MB",
        }
        self.assertEqual(ESP32_FLASH_SIZE_BY_NIBBLE, expected)
        for nibble, label in expected.items():
            with self.subTest(size=label):
                info = parse_bootloader_image_header(_image_header(2, nibble, 0x0))
                self.assertEqual(info.flash_size, label)
                self.assertEqual(info.flash_frequency, "40MHz")

    def test_esp32_s3_flash_frequency_table(self) -> None:
        expected = {
            0x0: "40MHz",
            0x1: "26MHz",
            0x2: "20MHz",
            0xF: "80MHz",
        }
        self.assertEqual(ESP32_S3_FLASH_FREQUENCY_BY_NIBBLE, expected)

    def test_accepts_esp32_s3_chip_id_when_present(self) -> None:
        data = _image_header_with_chip_id(2, 3, 0xF, ESP32_S3_CHIP_ID)
        info = parse_bootloader_image_header(data)
        self.assertEqual(info.flash_size, "8MB")

    def test_rejects_wrong_chip_id(self) -> None:
        data = _image_header_with_chip_id(2, 3, 0xF, 0x00)
        with self.assertRaises(FirmwarePioArtifactsError) as ctx:
            parse_bootloader_image_header(data)
        self.assertIn("unexpected chip_id", str(ctx.exception))


class PartitionTableTests(unittest.TestCase):
    def test_committed_csv_matches_fixture_binary(self) -> None:
        partitions_match_binary_csv(
            FIXTURES / "partitions_8mb.bin",
            PARTITIONS_CSV,
        )

    def test_mismatch_raises(self) -> None:
        with self.assertRaises(FirmwarePioArtifactsError):
            partitions_match_binary_csv(
                FIXTURES / "partitions_mismatch.bin",
                PARTITIONS_CSV,
            )

    def test_mismatch_size_only(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = _write_mutated_partition(Path(tmp), size=0x5000)
            with self.assertRaises(FirmwarePioArtifactsError) as ctx:
                partitions_match_binary_csv(path, PARTITIONS_CSV)
            self.assertIn("does not match", str(ctx.exception))

    def test_mismatch_offset_only(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = _write_mutated_partition(Path(tmp), offset=0x9100)
            with self.assertRaises(FirmwarePioArtifactsError):
                partitions_match_binary_csv(path, PARTITIONS_CSV)

    def test_mismatch_name_only(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = _write_mutated_partition(Path(tmp), name="badnvs")
            with self.assertRaises(FirmwarePioArtifactsError):
                partitions_match_binary_csv(path, PARTITIONS_CSV)

    def test_mismatch_subtype_only(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = _write_mutated_partition(Path(tmp), subtype=0x99)
            with self.assertRaises(FirmwarePioArtifactsError):
                partitions_match_binary_csv(path, PARTITIONS_CSV)

    def test_decode_rejects_bad_partition_entry_magic(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = _write_mutated_partition(Path(tmp), entry_magic=b"\xbb\x50")
            with self.assertRaises(FirmwarePioArtifactsError) as ctx:
                decode_partition_table_binary(path.read_bytes())
            self.assertIn("invalid partition entry magic", str(ctx.exception))

    def test_csv_and_binary_row_counts_align(self) -> None:
        csv_rows = load_partition_csv(PARTITIONS_CSV)
        bin_rows = decode_partition_table_binary(
            (FIXTURES / "partitions_8mb.bin").read_bytes()
        )
        self.assertEqual(len(csv_rows), 5)
        self.assertEqual(len(bin_rows), 5)
        self.assertEqual(
            [row.as_tuple() for row in csv_rows],
            [row.as_tuple() for row in bin_rows],
        )


class PrepareArtifactTests(unittest.TestCase):
    def test_prepare_artifact_dir_from_fixtures(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            build_dir = Path(tmp) / "build"
            build_dir.mkdir()
            (build_dir / "bootloader.bin").write_bytes(
                (FIXTURES / "bootloader_dio_80m_8mb.bin").read_bytes()
            )
            (build_dir / "partitions.bin").write_bytes(
                (FIXTURES / "partitions_8mb.bin").read_bytes()
            )
            (build_dir / "firmware.bin").write_bytes(b"\xe9" + b"\x00" * 8)
            out = Path(tmp) / "bundle"
            bl_src = (FIXTURES / "bootloader_dio_80m_8mb.bin").read_bytes()
            pt_src = (FIXTURES / "partitions_8mb.bin").read_bytes()
            fw_src = b"\xe9" + b"\x00" * 8
            prepare_artifact_dir(
                build_dir,
                out,
                "sticks3_u173",
                "sticks3_u173",
            )
            self.assertEqual((out / "bootloader.bin").read_bytes(), bl_src)
            self.assertEqual((out / "partitions.bin").read_bytes(), pt_src)
            self.assertEqual((out / "firmware.bin").read_bytes(), fw_src)
            self.assertEqual(
                (out / "flash-info.txt").read_text().splitlines(),
                [
                    "target=sticks3_u173",
                    "env=sticks3_u173",
                    "flash_mode=dio",
                    "flash_frequency=80MHz",
                    "flash_size=8MB",
                ],
            )

    def test_missing_bin_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            build_dir = Path(tmp)
            (build_dir / "bootloader.bin").write_bytes(
                (FIXTURES / "bootloader_dio_80m_8mb.bin").read_bytes()
            )
            with self.assertRaises(FirmwarePioArtifactsError):
                resolve_build_bins(build_dir)

    def test_write_flash_info_format(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            bootloader = Path(tmp) / "bootloader.bin"
            bootloader.write_bytes(
                (FIXTURES / "bootloader_qio_40m_4mb.bin").read_bytes()
            )
            dest = Path(tmp) / "flash-info.txt"
            write_flash_info(dest, "atoms3lite_u173_bench", "atoms3lite_u173_bench", bootloader)
            lines = dest.read_text().splitlines()
            self.assertEqual(
                lines,
                [
                    "target=atoms3lite_u173_bench",
                    "env=atoms3lite_u173_bench",
                    "flash_mode=qio",
                    "flash_frequency=40MHz",
                    "flash_size=4MB",
                ],
            )


class CliTests(unittest.TestCase):
    def test_verify_partitions_cli_ok(self) -> None:
        self.assertEqual(
            main(
                [
                    "verify-partitions",
                    "--partitions",
                    str(FIXTURES / "partitions_8mb.bin"),
                    "--expected",
                    str(PARTITIONS_CSV),
                ]
            ),
            0,
        )

    def test_verify_partitions_subprocess_ok(self) -> None:
        proc = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "verify-partitions",
                "--partitions",
                str(FIXTURES / "partitions_8mb.bin"),
                "--expected",
                str(PARTITIONS_CSV),
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_verify_partitions_subprocess_mismatch_exit_one(self) -> None:
        proc = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "verify-partitions",
                "--partitions",
                str(FIXTURES / "partitions_mismatch.bin"),
                "--expected",
                str(PARTITIONS_CSV),
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("does not match", proc.stderr)
        self.assertIn("partitions_mismatch.bin", proc.stderr)

    def test_module_runnable(self) -> None:
        proc = subprocess.run(
            [sys.executable, str(SCRIPT), "--help"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 0)
        self.assertIn("verify-partitions", proc.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)