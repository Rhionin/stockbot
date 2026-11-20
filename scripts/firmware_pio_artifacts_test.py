#!/usr/bin/env python3
"""Tests for scripts/firmware_pio_artifacts.py."""

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from firmware_pio_artifacts import (
    BootloaderFlashInfo,
    FirmwarePioArtifactsError,
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
FIXTURES = Path(__file__).resolve().parent / "testdata" / "firmware_pio"
PARTITIONS_CSV = ROOT / "firmware" / "partitions_8mb_ota.csv"
BUILD_DIR = ROOT / "firmware" / ".pio" / "build" / "sticks3_u173"


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
    def test_prepare_from_local_build_when_present(self) -> None:
        if not BUILD_DIR.is_dir():
            self.skipTest("local PlatformIO build dir not present")
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "bundle"
            prepare_artifact_dir(
                BUILD_DIR,
                out,
                "sticks3_u173",
                "sticks3_u173",
            )
            self.assertTrue((out / "bootloader.bin").is_file())
            self.assertTrue((out / "partitions.bin").is_file())
            self.assertTrue((out / "firmware.bin").is_file())
            flash_info = (out / "flash-info.txt").read_text()
            self.assertIn("target=sticks3_u173", flash_info)
            self.assertIn("flash_mode=dio", flash_info)
            self.assertIn("flash_size=8MB", flash_info)

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

    def test_module_runnable(self) -> None:
        script = Path(__file__).resolve().parent / "firmware_pio_artifacts.py"
        proc = subprocess.run(
            [sys.executable, str(script), "--help"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 0)
        self.assertIn("verify-partitions", proc.stdout)