#!/usr/bin/env python3
"""Tests for workflow path-filter vs fixture directory coverage."""

from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from check_workflow_path_filters import (
    FixtureRef,
    PathFilterError,
    WorkflowFixtureRule,
    collect_fixture_refs,
    filter_covers_fixture,
    github_path_matches,
    main,
    normalize_fixture_ref,
    validate_all,
    validate_rule,
)

ROOT = Path(__file__).resolve().parent.parent


class GlobMatchingTests(unittest.TestCase):
    def test_double_star_directory(self) -> None:
        self.assertTrue(
            github_path_matches("testdata/scenarios/**", "testdata/scenarios/a.json")
        )
        self.assertFalse(
            github_path_matches("testdata/scenarios/**", "testdata/other/a.json")
        )

    def test_exact_file(self) -> None:
        self.assertTrue(
            github_path_matches("testdata/pairing-codes.json", "testdata/pairing-codes.json")
        )
        self.assertFalse(
            github_path_matches("testdata/pairing-codes.json", "testdata/pairing-codes-extra.json")
        )


class NormalizeFixtureTests(unittest.TestCase):
    def test_deep_file_becomes_directory(self) -> None:
        self.assertEqual(
            normalize_fixture_ref("testdata/scenarios/foo.json"),
            FixtureRef("dir", "testdata/scenarios"),
        )

    def test_shallow_testdata_file_stays_file(self) -> None:
        self.assertEqual(
            normalize_fixture_ref("testdata/pairing-codes.json"),
            FixtureRef("file", "testdata/pairing-codes.json"),
        )


class LiveRepoTests(unittest.TestCase):
    def test_committed_repo_passes(self) -> None:
        validate_all(root=ROOT)

    def test_main_exit_ok(self) -> None:
        self.assertEqual(main(), 0)

    def test_live_firmware_rust_scan_finds_fixture_refs(self) -> None:
        refs = collect_fixture_refs([ROOT / "firmware/rust"])
        self.assertGreater(len(refs), 0)


class FailClosedGuardTests(unittest.TestCase):
    FIRMWARE_RULE = WorkflowFixtureRule(
        workflow_relpath=".github/workflows/firmware-rust.yml",
        scan_dirs=("firmware/rust",),
    )

    def test_missing_workflow_file_raises(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with self.assertRaises(PathFilterError) as ctx:
                validate_rule(self.FIRMWARE_RULE, root=root)
            self.assertIn("missing workflow", str(ctx.exception))

    def test_pull_request_without_paths_raises(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            wf = root / ".github/workflows/firmware-rust.yml"
            wf.parent.mkdir(parents=True, exist_ok=True)
            wf.write_text(
                "\n".join(
                    [
                        "name: Firmware Rust",
                        "on:",
                        "  pull_request:",
                        "    branches: [master]",
                        "  push:",
                        "    branches: [master]",
                        "    paths:",
                        "      - '**'",
                        "jobs:",
                        "  rust-core:",
                        "    runs-on: ubuntu-latest",
                    ]
                )
                + "\n",
                encoding="utf-8",
            )
            with self.assertRaises(PathFilterError) as ctx:
                validate_rule(self.FIRMWARE_RULE, root=root)
            self.assertIn("missing on.pull_request.paths", str(ctx.exception))

    def test_cli_invalid_yaml_exits_nonzero(self) -> None:
        script = Path(__file__).resolve().parent / "check_workflow_path_filters.py"
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "firmware/rust/crates/x/tests").mkdir(parents=True)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".github/workflows/firmware-rust.yml").write_text(
                "on:\n  pull_request:\n    paths: [[[\n",
                encoding="utf-8",
            )
            proc = subprocess.run(
                [sys.executable, str(script)],
                env={
                    **dict(__import__("os").environ),
                    "STOCKBOT_REPO_ROOT": str(root),
                },
                capture_output=True,
                text=True,
                cwd=str(script.parent),
            )
            self.assertNotEqual(proc.returncode, 0)
            self.assertNotIn("check_workflow_path_filters ok", proc.stdout)


class TempWorkflowTests(unittest.TestCase):
    def _write_firmware_rust_workflow(self, dest: Path, *, paths: list[str]) -> None:
        lines = [
            "name: Firmware Rust",
            "on:",
            "  pull_request:",
            "    paths:",
        ]
        for p in paths:
            lines.append(f"      - '{p}'")
        lines.extend(
            [
                "  push:",
                "    branches: [master]",
                "    paths:",
            ]
        )
        for p in paths:
            lines.append(f"      - '{p}'")
        lines.extend(
            [
                "jobs:",
                "  rust-core:",
                "    runs-on: ubuntu-latest",
                "    steps:",
                "      - run: true",
            ]
        )
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text("\n".join(lines) + "\n", encoding="utf-8")

    def _seed_minimal_rust_tree(self, root: Path, *, fixture_line: str) -> None:
        crate = root / "firmware/rust/crates/demo/tests"
        crate.mkdir(parents=True)
        (crate / "demo.rs").write_text(
            f'fn demo() {{ let _ = "{fixture_line}"; }}\n',
            encoding="utf-8",
        )

    def test_missing_fixture_dir_in_pr_paths_fails(self) -> None:
        paths = [
            "firmware/rust/**",
            "testdata/pairing-codes.json",
            "testdata/device-protocol/**",
            "testdata/display-model/**",
            "firmware/test/vectors/**",
        ]
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            wf = root / ".github/workflows/firmware-rust.yml"
            self._write_firmware_rust_workflow(wf, paths=paths)
            self._seed_minimal_rust_tree(
                root, fixture_line="testdata/scenarios/missing.json"
            )
            rule = WorkflowFixtureRule(
                workflow_relpath=".github/workflows/firmware-rust.yml",
                scan_dirs=("firmware/rust",),
            )
            with self.assertRaises(PathFilterError) as ctx:
                validate_rule(rule, root=root)
            self.assertIn("pull_request", str(ctx.exception))
            self.assertIn("testdata/scenarios", str(ctx.exception))

    def test_missing_fixture_dir_in_push_paths_only_fails(self) -> None:
        pr_paths = [
            "firmware/rust/**",
            "testdata/scenarios/**",
            "firmware/test/scenarios/**",
        ]
        push_paths = [
            "firmware/rust/**",
            "testdata/scenarios/**",
        ]
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            wf = root / ".github/workflows/firmware-rust.yml"
            wf.parent.mkdir(parents=True)
            lines = [
                "name: Firmware Rust",
                "on:",
                "  pull_request:",
                "    paths:",
            ]
            for p in pr_paths:
                lines.append(f"      - '{p}'")
            lines += ["  push:", "    branches: [master]", "    paths:"]
            for p in push_paths:
                lines.append(f"      - '{p}'")
            lines += ["jobs:", "  rust-core:", "    runs-on: ubuntu-latest", "    steps:", "      - run: true"]
            wf.write_text("\n".join(lines) + "\n", encoding="utf-8")
            self._seed_minimal_rust_tree(
                root, fixture_line="firmware/test/scenarios/x.json"
            )
            # push_paths omit firmware/test/scenarios/**
            rule = WorkflowFixtureRule(
                workflow_relpath=".github/workflows/firmware-rust.yml",
                scan_dirs=("firmware/rust",),
            )
            with self.assertRaises(PathFilterError) as ctx:
                validate_rule(rule, root=root)
            self.assertIn("push.paths", str(ctx.exception))

    def test_complete_paths_pass(self) -> None:
        paths = [
            "firmware/rust/**",
            "testdata/scenarios/**",
            "firmware/test/scenarios/**",
        ]
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            wf = root / ".github/workflows/firmware-rust.yml"
            self._write_firmware_rust_workflow(wf, paths=paths)
            self._seed_minimal_rust_tree(
                root, fixture_line="testdata/scenarios/x.json"
            )
            rule = WorkflowFixtureRule(
                workflow_relpath=".github/workflows/firmware-rust.yml",
                scan_dirs=("firmware/rust",),
            )
            validate_rule(rule, root=root)


class CollectionTests(unittest.TestCase):
    def test_collect_from_rust_tree(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self._write_rs(root, "testdata/device-protocol/a.json")
            refs = collect_fixture_refs([root])
            self.assertIn(FixtureRef("dir", "testdata/device-protocol"), refs)

    def _write_rs(self, root: Path, fixture: str) -> None:
        d = root / "sub"
        d.mkdir()
        (d / "t.rs").write_text(f'const X: &str = "{fixture}";\n', encoding="utf-8")


class FilterCoversFixtureTests(unittest.TestCase):
    def test_dir_covered_by_glob(self) -> None:
        fixture = FixtureRef("dir", "testdata/scenarios")
        patterns = ["firmware/rust/**", "testdata/scenarios/**"]
        self.assertTrue(filter_covers_fixture(patterns, fixture))

    def test_dir_not_covered(self) -> None:
        fixture = FixtureRef("dir", "testdata/scenarios")
        patterns = ["firmware/rust/**"]
        self.assertFalse(filter_covers_fixture(patterns, fixture))


class MainCliReplayTests(unittest.TestCase):
    def test_cli_fails_on_poisoned_temp_workflow(self) -> None:
        script = Path(__file__).resolve().parent / "check_workflow_path_filters.py"
        wf_path = ROOT / ".github/workflows/firmware-rust.yml"
        wf_text = wf_path.read_text(encoding="utf-8")
        if "testdata/scenarios/**" not in wf_text:
            self.skipTest("repo already missing scenarios path (checker should fail)")
        poisoned = wf_text.replace("      - 'testdata/scenarios/**'\n", "")
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "firmware/rust/crates/x/tests").mkdir(parents=True)
            (root / "firmware/rust/crates/x/tests/t.rs").write_text(
                'const A: &str = "testdata/scenarios/a.json";\n',
                encoding="utf-8",
            )
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".github/workflows/firmware-rust.yml").write_text(poisoned, encoding="utf-8")
            proc = subprocess.run(
                [
                    sys.executable,
                    str(script),
                ],
                env={**dict(__import__("os").environ), "STOCKBOT_REPO_ROOT": str(root)},
                capture_output=True,
                text=True,
                cwd=str(script.parent),
            )
            self.assertEqual(proc.returncode, 1)
            self.assertIn("testdata/scenarios", proc.stderr)


if __name__ == "__main__":
    unittest.main()