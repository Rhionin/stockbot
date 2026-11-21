#!/usr/bin/env python3
"""Ensure path-filtered workflows include globs for fixture trees their tests read."""

from __future__ import annotations

import os
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterable

import yaml

_DEFAULT_ROOT = Path(__file__).resolve().parent.parent
ROOT = Path(os.environ.get("STOCKBOT_REPO_ROOT", _DEFAULT_ROOT)).resolve()


class PathFilterError(Exception):
    pass


def _loader() -> type[yaml.SafeLoader]:
    loader = yaml.SafeLoader
    loader.yaml_implicit_resolvers = {
        k: [(t, r) for t, r in v if t != "tag:yaml.org,2002:bool"]
        for k, v in loader.yaml_implicit_resolvers.items()
    }
    return loader


def load_workflow(path: Path) -> dict[str, Any]:
    doc = yaml.load(path.read_text(encoding="utf-8"), Loader=_loader())
    if not isinstance(doc, dict):
        raise PathFilterError(f"{path}: root must be a mapping")
    return doc


def github_path_matches(pattern: str, filepath: str) -> bool:
    """True when a change to filepath would match a GitHub Actions paths filter glob."""
    norm = filepath.replace("\\", "/").lstrip("./")
    pat = pattern.replace("\\", "/")
    if pat == "**":
        return True

    regex_parts: list[str] = ["^"]
    i = 0
    while i < len(pat):
        if pat[i : i + 2] == "**":
            regex_parts.append(".*")
            i += 2
            if i < len(pat) and pat[i] == "/":
                i += 1
        elif pat[i] == "*":
            regex_parts.append("[^/]*")
            i += 1
        elif pat[i] == "?":
            regex_parts.append("[^/]")
            i += 1
        else:
            j = i
            while j < len(pat) and pat[j] not in "*?":
                j += 1
            regex_parts.append(re.escape(pat[i:j]))
            i = j
    regex_parts.append("$")
    return re.match("".join(regex_parts), norm) is not None


def filter_covers_path(patterns: Iterable[str], repo_relative_path: str) -> bool:
    return any(github_path_matches(p, repo_relative_path) for p in patterns)


def filter_covers_fixture(patterns: Iterable[str], fixture: FixtureRef) -> bool:
    for probe in fixture.change_probes():
        if filter_covers_path(patterns, probe):
            return True
    return False


@dataclass(frozen=True)
class FixtureRef:
    """Directory or lone testdata file referenced by workflow tests."""

    kind: str  # "dir" | "file"
    path: str

    def change_probes(self) -> list[str]:
        if self.kind == "file":
            return [self.path]
        return [f"{self.path}/fixture-probe.json"]


FIXTURE_STRING_RE = re.compile(
    r"""["']((?:testdata|firmware/test)/[a-zA-Z0-9_./-]+)["']"""
)

FIRMWARE_TEST_FIXTURE_ROOTS = (
    "firmware/test/vectors",
    "firmware/test/scenarios",
)


def raw_fixture_strings_from_text(text: str) -> set[str]:
    found: set[str] = set()
    for m in FIXTURE_STRING_RE.finditer(text):
        found.add(m.group(1).replace("\\", "/"))
    return found


def _firmware_test_fixture_dir(path: str) -> str | None:
    for root in FIRMWARE_TEST_FIXTURE_ROOTS:
        if path == root or path.startswith(root + "/"):
            return root
    return None


def normalize_fixture_ref(raw: str) -> FixtureRef | None:
    path = raw.replace("\\", "/").strip()
    if path.startswith("firmware/test/"):
        fixture_root = _firmware_test_fixture_dir(path)
        if fixture_root is None:
            return None
        return FixtureRef("dir", fixture_root)
    if not path.startswith("testdata/"):
        return None
    leaf = path.rsplit("/", 1)[-1]
    if "." in leaf:
        if path.count("/") == 1:
            return FixtureRef("file", path)
        parent = path.rsplit("/", 1)[0]
        return FixtureRef("dir", parent)
    return FixtureRef("dir", path)


def collect_fixture_refs(scan_roots: Iterable[Path]) -> set[FixtureRef]:
    refs: set[FixtureRef] = set()
    for root in scan_roots:
        if not root.is_dir():
            continue
        for path in root.rglob("*.rs"):
            for s in raw_fixture_strings_from_text(
                path.read_text(encoding="utf-8", errors="replace")
            ):
                ref = normalize_fixture_ref(s)
                if ref is not None:
                    refs.add(ref)
    return refs


def workflow_path_lists(workflow: dict[str, Any]) -> tuple[list[str], list[str]]:
    on = workflow.get("on")
    if not isinstance(on, dict):
        raise PathFilterError("workflow on: must be a mapping")
    pr = on.get("pull_request")
    push = on.get("push")
    if not isinstance(pr, dict) or "paths" not in pr:
        raise PathFilterError("workflow missing on.pull_request.paths")
    if not isinstance(push, dict) or "paths" not in push:
        raise PathFilterError("workflow missing on.push.paths")
    pr_paths = pr["paths"]
    push_paths = push["paths"]
    if not isinstance(pr_paths, list) or not isinstance(push_paths, list):
        raise PathFilterError("paths must be lists")
    return [str(p) for p in pr_paths], [str(p) for p in push_paths]


@dataclass(frozen=True)
class WorkflowFixtureRule:
    """One path-filtered workflow and where to scan for fixture references."""

    workflow_relpath: str
    scan_dirs: tuple[str, ...]

    def workflow_path(self, root: Path) -> Path:
        return root / self.workflow_relpath


WORKFLOW_FIXTURE_RULES: tuple[WorkflowFixtureRule, ...] = (
    WorkflowFixtureRule(
        workflow_relpath=".github/workflows/firmware-rust.yml",
        scan_dirs=("firmware/rust",),
    ),
)


def validate_rule(rule: WorkflowFixtureRule, *, root: Path = ROOT) -> None:
    wf_path = rule.workflow_path(root)
    if not wf_path.is_file():
        raise PathFilterError(f"missing workflow {rule.workflow_relpath}")
    workflow = load_workflow(wf_path)
    pr_paths, push_paths = workflow_path_lists(workflow)
    scan_roots = [root / d for d in rule.scan_dirs]
    fixtures = collect_fixture_refs(scan_roots)
    for fixture in sorted(fixtures, key=lambda f: (f.kind, f.path)):
        if not filter_covers_fixture(pr_paths, fixture):
            raise PathFilterError(
                f"{rule.workflow_relpath}: on.pull_request.paths does not cover "
                f"fixture {fixture.path!r} (probes {fixture.change_probes()})"
            )
        if not filter_covers_fixture(push_paths, fixture):
            raise PathFilterError(
                f"{rule.workflow_relpath}: on.push.paths does not cover "
                f"fixture {fixture.path!r} (probes {fixture.change_probes()})"
            )


def validate_all(*, root: Path = ROOT, rules: Iterable[WorkflowFixtureRule] = WORKFLOW_FIXTURE_RULES) -> None:
    for rule in rules:
        validate_rule(rule, root=root)


def main() -> int:
    try:
        validate_all(root=ROOT)
    except PathFilterError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    print("check_workflow_path_filters ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())