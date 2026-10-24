#!/usr/bin/env python3
"""Count Stockbot test size and test calorie density.

Density is non-blank, non-comment test lines divided by production lines that
a test actually ran. It is an advisory signal, not a merge gate. Go and web
definitions match count-tests.py 1.0, which the 2026-10-17 master baseline was
measured with (Go about 2.62, web unit about 3.15). 1.1 changes only how mobile
test files are found: from the mobile Jest config, not the web naming rule.
1.2 counts shared/ TypeScript with the web unit suite, because Vitest runs it.
Change the counting rules only with a version bump.

Usage:
  scripts/count-tests.py report --repo . \\
      --go-cover go.cover --go-cover-local go-local.cover \\
      --go-json go-test.json \\
      --web-cover vitest-coverage/coverage-final.json \\
      --web-json vitest-results.json \\
      --json metrics.json

  scripts/count-tests.py diff base.json head.json \\
      --base-repo base --head-repo head --markdown comment.md --json delta.json

Inputs are a Go coverprofile (from go test -coverpkg over the whole module)
and a Vitest v8 istanbul coverage-final.json. The optional package-local
coverprofile (go test without -coverpkg) does not change density. It attributes
flag C: new production lines covered only by tests in other packages.

Flag B (mutation score) is intentionally not computed. See the TODO in
flag_b_todo().
"""

from __future__ import annotations

import argparse
import difflib
import json
import os
import re
import subprocess
import sys
from collections import defaultdict
from decimal import Decimal, ROUND_HALF_UP
from pathlib import Path

VERSION = "1.2"

# Twice the 2026-10-17 baseline densities (Go 2.62, web 3.15), as agreed.
# Flag A fires only when marginal density is strictly above these.
FLAG_A_ABOVE = {"go": Decimal("5.2"), "web_unit": Decimal("6.3")}

EXCL_DIRS = {
    "node_modules",
    "vendor",
    "dist",
    "build",
    "coverage",
    ".git",
    "testdata",
    "fixtures",
    "__snapshots__",
    "test-results",
    "playwright-report",
    "bin",
    "reports",
}
FIXTURE_DIRS = {"testdata", "fixtures", "__snapshots__"}
GEN_RE = re.compile(r"^// Code generated .* DO NOT EDIT\.$", re.M)
GO_ASSERT = re.compile(
    r"\b(?:t|rt|tb|tt|b)\.(?:Error|Errorf|Fatal|Fatalf)\(|\b(?:assert|require)\.\w+\("
)
TS_TESTCASE = re.compile(
    r"(?<![\w.])(?:it|test)(?:\.(?:each\([^)]*\)|prop\([^)]*\)|only|skip|fixme|concurrent))?\s*\("
)
VI_FN = re.compile(r"\bvi\.fn\(")
FLAG_C_JSON_LIMIT = 100
FLAG_C_MARKDOWN_LIMIT = 30

# Shown in the sticky comment. The JSON diff keeps every numeric field.
COMMENT_KEYS = (
    "test_loc",
    "covered_prod_loc",
    "density",
    "assertions_per_100_test_loc",
    "vi_fn",
    "rapid_check",
    "fast_check",
    "sleeps",
    "skips",
)


def flag_b_todo() -> str:
    """Why flag B is absent, kept next to the only place that would compute it.

    TODO(flag-b): pair this delta with diff-scoped mutation scores — gremlins
    for Go, Stryker for web — once Stryker's scores match a known mutant.
    Flag B fires when test LOC falls in a changed area and mutation efficacy
    falls, or a mutant that was killed on the base survives on the head.
    Density alone must not become a merge gate.
    """
    return (
        "Not computed. TODO: pair this delta with diff-scoped mutation scores "
        "(gremlins for Go, Stryker for web). Flag B fires when test LOC falls "
        "in a changed area and mutation efficacy falls, or a mutant killed on "
        "the base survives on the head. Density is not a gate without that."
    )


def code_lines(text: str) -> list[int]:
    """Return 1-based line numbers that contain code.

    Blank lines and lines that are only // or /* */ comments are omitted.
    A line with code and a trailing comment counts. Quote forms ", ', and `
    hide a // that sits inside a string on that same line.

    Block comments continue across lines. A raw string or template quote does
    not: the next line is scanned on its own. That is the v1.0 rule the master
    baseline was measured with.
    """
    out: list[int] = []
    in_block = False
    for number, line in enumerate(text.splitlines(), 1):
        i = 0
        has_code = False
        length = len(line)
        quote = None
        while i < length:
            ch = line[i]
            if in_block:
                close = line.find("*/", i)
                if close < 0:
                    i = length
                else:
                    in_block = False
                    i = close + 2
                continue
            if quote:
                if ch == "\\":
                    i += 2
                    continue
                if ch == quote:
                    quote = None
                i += 1
                continue
            if ch in "\"'`":
                quote = ch
                has_code = True
                i += 1
                continue
            if line.startswith("//", i):
                break
            if line.startswith("/*", i):
                in_block = True
                i += 2
                continue
            if not ch.isspace():
                has_code = True
            i += 1
        if has_code:
            out.append(number)
    return out


def walk_files(root: Path) -> list[str]:
    """Repo-relative paths, directories pruned, names sorted for stable output."""
    files: list[str] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(
            name for name in dirnames if name not in EXCL_DIRS and not name.startswith(".")
        )
        for name in sorted(filenames):
            full = Path(dirpath) / name
            files.append(full.relative_to(root).as_posix())
    return files


def read_text(root: Path, rel: str) -> str:
    path = root / rel
    return path.read_text(encoding="utf-8", errors="replace")


def is_excluded_file(rel: str) -> bool:
    return rel.endswith((".bak", ".snap", ".d.ts"))


# Jest's own defaults, used when the mobile app sets neither testMatch nor testRegex.
JEST_DEFAULT_TEST_MATCH = [
    "**/__tests__/**/*.?([mc])[jt]s?(x)",
    "**/?(*.)+(spec|test).?([mc])[jt]s?(x)",
]
JEST_JS_CONFIGS = ("jest.config.js", "jest.config.ts", "jest.config.mjs", "jest.config.cjs")


def jest_glob_regex(glob: str) -> str:
    """Translate the micromatch subset Jest testMatch uses into a regex.

    Supports **, *, ?, [...], {a,b} and the extglobs ?(..) *(..) +(..) @(..).
    A leading ! (negation) is handled by the caller.
    """
    out: list[str] = []
    i = 0
    stack: list[str] = []
    while i < len(glob):
        c = glob[i]
        nxt = glob[i + 1] if i + 1 < len(glob) else ""
        if c in "?*+@" and nxt == "(":
            stack.append(c)
            out.append("(?:")
            i += 2
            continue
        if c == ")" and stack:
            op = stack.pop()
            out.append({"?": ")?", "*": ")*", "+": ")+", "@": ")"}[op])
            i += 1
            continue
        if c == "|" and stack:
            out.append("|")
        elif c == "{":
            stack.append("{")
            out.append("(?:")
        elif c == "}" and stack and stack[-1] == "{":
            stack.pop()
            out.append(")")
        elif c == "," and stack and stack[-1] == "{":
            out.append("|")
        elif c == "*" and nxt == "*":
            if glob[i + 2 : i + 3] == "/":
                out.append("(?:.*/)?")
                i += 3
                continue
            out.append(".*")
            i += 2
            continue
        elif c == "*":
            out.append("[^/]*")
        elif c == "?":
            out.append("[^/]")
        elif c == "[":
            end = glob.find("]", i + 1)
            if end == -1:
                out.append(re.escape(c))
            else:
                out.append("[" + glob[i + 1 : end] + "]")
                i = end + 1
                continue
        else:
            out.append(re.escape(c))
        i += 1
    return "".join(out)


def mobile_test_matcher(root: Path):
    """Return a predicate for repo-relative paths that the mobile Jest run treats as tests.

    Reads testMatch or testRegex from mobile/package.json "jest" or mobile/jest.config.json,
    falling back to Jest's defaults. A JavaScript Jest config cannot be read here, so it is
    an error rather than a guess.
    """
    mobile = root / "mobile"
    config: dict = {}
    package = mobile / "package.json"
    if package.is_file():
        try:
            config = json.loads(package.read_text(encoding="utf-8")).get("jest") or {}
        except json.JSONDecodeError as err:
            raise ValueError(f"mobile/package.json could not be parsed: {err}") from err
    if (mobile / "jest.config.json").is_file():
        config = json.loads((mobile / "jest.config.json").read_text(encoding="utf-8"))
    for name in JEST_JS_CONFIGS:
        if (mobile / name).is_file():
            raise ValueError(
                f"mobile/{name} is a JavaScript Jest config; count-tests.py reads only "
                "package.json \"jest\" or jest.config.json to find mobile tests"
            )
    test_regex = config.get("testRegex")
    if test_regex:
        patterns = [re.compile(r) for r in ([test_regex] if isinstance(test_regex, str) else test_regex)]
    else:
        patterns, negated = [], []
        for glob in config.get("testMatch") or JEST_DEFAULT_TEST_MATCH:
            target = negated if glob.startswith("!") else patterns
            glob = glob.lstrip("!").replace("<rootDir>/", "mobile/")
            if not glob.startswith(("mobile/", "**")):
                glob = "**/" + glob
            target.append(re.compile("^" + jest_glob_regex(glob) + "$"))

    def is_mobile_test(rel: str) -> bool:
        if "/node_modules/" in "/" + rel:
            return False
        if test_regex:
            return any(p.search("/" + rel) for p in patterns)
        return any(p.match(rel) for p in patterns) and not any(p.match(rel) for p in negated)

    return is_mobile_test


def classify(root: Path) -> dict[str, list[str]]:
    """Group source files. Fixture and testdata files are listed, not measured."""
    groups: dict[str, list[str]] = defaultdict(list)
    is_mobile_test = mobile_test_matcher(root)
    for rel in walk_files(root):
        if is_excluded_file(rel):
            continue
        parts = rel.split("/")
        if rel.endswith(".go"):
            if rel.endswith("_test.go"):
                groups["go_test"].append(rel)
            elif GEN_RE.search(read_text(root, rel)[:2000]):
                groups["generated"].append(rel)
            else:
                groups["go_prod"].append(rel)
            continue
        if not rel.endswith((".ts", ".tsx")):
            continue
        is_test = bool(re.search(r"\.(test|spec)\.tsx?$", rel)) or "__tests__" in parts
        if parts[0] == "frontend" and len(parts) > 1 and parts[1] == "e2e":
            groups["e2e"].append(rel)
        elif parts[0] == "frontend" and len(parts) > 1 and parts[1] == "src":
            if is_test or rel == "frontend/src/test-setup.ts":
                groups["web_test"].append(rel)
            else:
                groups["web_prod"].append(rel)
        elif parts[0] == "shared":
            # Vitest runs shared tests from the web app. Count them with web unit.
            key = "web_test" if is_test else "web_prod"
            groups[key].append(rel)
        elif parts[0] == "mobile":
            # Mobile Jest names tests by its own testMatch (for example
            # client-test.ts), not by the web .test.ts rule.
            key = "mobile_test" if is_mobile_test(rel) else "mobile_prod"
            groups[key].append(rel)

    # The walk above prunes fixture directories so they cannot inflate density.
    # Count them in a second pass so the report can still say they were excluded.
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(name for name in dirnames if name not in {"node_modules", ".git"})
        relative = Path(dirpath).relative_to(root).as_posix()
        segments = [] if relative == "." else relative.split("/")
        if not any(segment in FIXTURE_DIRS for segment in segments):
            continue
        for name in sorted(filenames):
            groups["fixtures"].append((Path(dirpath) / name).relative_to(root).as_posix())
    return groups


def go_module(root: Path) -> str:
    match = re.search(r"^module\s+(\S+)", read_text(root, "go.mod"), re.M)
    if not match:
        raise ValueError(f"could not find a Go module path in {root / 'go.mod'}")
    return match.group(1)


def parse_go_cover(root: Path, cover_path: str) -> tuple[dict[str, set[int]], dict[str, set[int]], int, int]:
    """Map each file to covered and coverable line numbers.

    A coverprofile block is a basic block. Every line in the block is coverable,
    and the whole block is covered when its count is greater than zero. The same
    block can appear in more than one package's test binary; any count above
    zero wins.
    """
    module = go_module(root)
    text = Path(cover_path).read_text(encoding="utf-8", errors="replace")
    if "mode:" not in text and not text.strip():
        raise ValueError(f"go coverage profile is empty or not a coverprofile: {cover_path}")
    covered: dict[str, set[int]] = defaultdict(set)
    coverable: dict[str, set[int]] = defaultdict(set)
    statements: dict[tuple, tuple[int, bool]] = {}
    saw_mode = False
    for raw in text.splitlines():
        line = raw.strip()
        if not line:
            continue
        if line.startswith("mode:"):
            saw_mode = True
            continue
        match = re.match(r"^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$", line)
        if not match:
            continue
        filename, start, _sc, end, _ec, num_stmt, count = match.groups()
        if filename.startswith(module + "/"):
            rel = filename[len(module) + 1 :]
        else:
            rel = filename
        key = (rel, start, _sc, end, _ec)
        previous = statements.get(key, (int(num_stmt), False))
        statements[key] = (int(num_stmt), previous[1] or int(count) > 0)
        span = range(int(start), int(end) + 1)
        coverable[rel].update(span)
        if int(count) > 0:
            covered[rel].update(span)
    if not saw_mode and not statements:
        raise ValueError(f"go coverage profile is empty or not a coverprofile: {cover_path}")
    total = sum(item[0] for item in statements.values())
    hit = sum(item[0] for item in statements.values() if item[1])
    return covered, coverable, hit, total


def parse_istanbul(root: Path, cover_path: str) -> tuple[dict[str, set[int]], dict[str, set[int]]]:
    """Covered and coverable lines from a Vitest v8 istanbul coverage-final.json.

    A line is coverable when it is the start line of a statement, and covered
    when at least one such statement has a hit count above zero. That is
    istanbul's own line rule: a function body is not covered just because its
    declaration statement ran.
    """
    try:
        data = json.loads(Path(cover_path).read_text(encoding="utf-8"))
    except json.JSONDecodeError as err:
        raise ValueError(f"web coverage JSON could not be parsed: {cover_path}") from err
    if not isinstance(data, dict):
        raise ValueError(f"web coverage JSON must be an object keyed by file: {cover_path}")
    covered: dict[str, set[int]] = defaultdict(set)
    coverable: dict[str, set[int]] = defaultdict(set)
    saw_statement = False
    root_resolved = root.resolve()
    for raw_path, payload in data.items():
        if not isinstance(payload, dict) or "statementMap" not in payload:
            continue
        saw_statement = True
        rel = coverage_relpath(root_resolved, raw_path)
        statement_map = payload.get("statementMap") or {}
        hits = payload.get("s") or {}
        for statement_id, loc in statement_map.items():
            line = loc["start"]["line"]
            coverable[rel].add(line)
            if hits.get(statement_id, hits.get(str(statement_id), 0)) > 0:
                covered[rel].add(line)
    if data and not saw_statement:
        raise ValueError(
            "web coverage JSON has no statementMap; pass Vitest v8 coverage-final.json "
            f"({cover_path})"
        )
    return covered, coverable


def coverage_relpath(root: Path, raw_path: str) -> str:
    path = raw_path
    if path.startswith("file://"):
        path = path[len("file://") :]
    candidate = Path(path)
    if candidate.is_absolute():
        try:
            return candidate.resolve().relative_to(root).as_posix()
        except ValueError:
            return candidate.as_posix()
    return candidate.as_posix()


def count_matches(text: str, pattern: re.Pattern[str]) -> int:
    return len(pattern.findall(text))


def dir_key(rel: str) -> str:
    """Parent directory of a file, using the report's forward-slash paths.

    This is the Go package, or the web folder that holds the file. It is a
    diagnostic only. The v1.0 helper had a special case for kinds named
    web_test and web_prod, but the measured baseline called it with kind "web",
    so that branch never ran and directories were the parent folder.
    """
    if "/" not in rel:
        return "."
    return rel.rsplit("/", 1)[0]


def language_section(
    root: Path,
    test_files: list[str],
    prod_files: list[str],
    covered: dict[str, set[int]] | None,
    kind: str,
    extra: dict | None = None,
    coverable: dict[str, set[int]] | None = None,
    local_covered: dict[str, set[int]] | None = None,
) -> dict:
    per_dir: dict[str, dict] = defaultdict(
        lambda: {
            "test_loc": 0,
            "prod_loc": 0,
            "coverable_loc": 0,
            "covered_loc": 0,
            "covered_elsewhere_loc": 0,
            "test_files": 0,
        }
    )
    section: dict = {
        "test_files": len(test_files),
        "prod_files": len(prod_files),
        "test_loc": 0,
        "prod_loc": 0,
        "coverable_prod_loc": 0,
        "covered_prod_loc": 0,
        "assertions": 0,
    }
    signals: dict[str, int] = defaultdict(int)
    go = kind == "go"
    for rel in test_files:
        text = read_text(root, rel)
        loc = len(code_lines(text))
        section["test_loc"] += loc
        bucket = per_dir[dir_key(rel)]
        bucket["test_loc"] += loc
        bucket["test_files"] += 1
        if go:
            signals["test_funcs"] += len(re.findall(r"^func (?:Test|Fuzz)\w*\(", text, re.M))
            signals["t_run"] += len(re.findall(r"\.Run\(", text))
            signals["rapid_check"] += len(re.findall(r"\brapid\.Check\(", text))
            signals["skips"] += len(re.findall(r"\.Skip(?:f|Now)?\(", text))
            signals["sleeps"] += len(re.findall(r"\btime\.Sleep\(", text))
            section["assertions"] += count_matches(text, GO_ASSERT)
        else:
            signals["test_cases_static"] += count_matches(text, TS_TESTCASE)
            signals["vi_fn"] += count_matches(text, VI_FN)
            signals["vi_mock"] += len(re.findall(r"\bvi\.mock\(", text))
            signals["fast_check"] += len(re.findall(r"\bfc\.assert\(|\.prop\(|\bfc\.property\(", text))
            signals["skips"] += len(re.findall(r"\b(?:it|test|describe)\.(?:skip|fixme|todo)\(", text))
            section["assertions"] += len(re.findall(r"\bexpect(?:\.soft|\.poll)?\(", text))

    files: dict[str, dict] = {}
    elsewhere_total = 0
    for rel in prod_files:
        text = read_text(root, rel)
        code = set(code_lines(text))
        any_covered = code & covered.get(rel, set()) if covered is not None else set()
        any_coverable = code & coverable.get(rel, set()) if coverable is not None else set()
        section["prod_loc"] += len(code)
        section["covered_prod_loc"] += len(any_covered)
        section["coverable_prod_loc"] += len(any_coverable)
        bucket = per_dir[dir_key(rel)]
        bucket["prod_loc"] += len(code)
        bucket["coverable_loc"] += len(any_coverable)
        bucket["covered_loc"] += len(any_covered)
        record = {
            "code_lines": sorted(code),
            "covered_lines": sorted(any_covered) if covered is not None else None,
        }
        if local_covered is not None:
            local_lines = code & local_covered.get(rel, set())
            record["local_covered_lines"] = sorted(local_lines)
            # Lines the package's own tests did not run. Other packages did.
            elsewhere = any_covered - local_lines
            bucket["covered_elsewhere_loc"] += len(elsewhere)
            elsewhere_total += len(elsewhere)
        else:
            record["local_covered_lines"] = None
        files[rel] = record

    section.update(signals)
    if covered is not None:
        coverable_total = section["coverable_prod_loc"]
        covered_total = section["covered_prod_loc"]
        section["covered_pct_of_coverable_loc"] = (
            round(100 * covered_total / coverable_total, 1) if coverable_total else None
        )
        section["density"] = density_value(section["test_loc"], covered_total)
    if local_covered is not None:
        section["covered_elsewhere_loc"] = elsewhere_total
    section["test_to_code_ratio"] = (
        round(section["test_loc"] / section["prod_loc"], 3) if section["prod_loc"] else None
    )
    section["assertions_per_100_test_loc"] = (
        round(100 * section["assertions"] / section["test_loc"], 2) if section["test_loc"] else None
    )
    if extra:
        section.update(extra)
    dirs = {}
    for key in sorted(per_dir):
        bucket = per_dir[key]
        covered_loc = bucket["covered_loc"]
        coverable_loc = bucket["coverable_loc"]
        bucket["density"] = round(bucket["test_loc"] / covered_loc, 3) if covered_loc else None
        bucket["covered_pct"] = round(100 * covered_loc / coverable_loc, 1) if coverable_loc else None
        if local_covered is None:
            bucket.pop("covered_elsewhere_loc", None)
        dirs[key] = bucket
    section["dirs"] = dirs
    section["files"] = files
    return section


def density_value(test_loc: int, covered_loc: int) -> float | None:
    if not covered_loc:
        return None
    return round(test_loc / covered_loc, 3)


def go_runtime(path: str) -> dict:
    passed, failed = set(), set()
    with open(path, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if event.get("Test") and event.get("Action") in ("pass", "fail"):
                target = passed if event["Action"] == "pass" else failed
                target.add((event["Package"], event["Test"]))
    top = {item for item in passed | failed if "/" not in item[1]}
    return {
        "runtime_tests_and_subtests": len(passed | failed),
        "runtime_top_level_tests": len(top),
        "runtime_failed": len(failed),
    }


def web_runtime(path: str) -> dict:
    try:
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
    except json.JSONDecodeError as err:
        raise ValueError(f"web test JSON could not be parsed: {path}") from err
    if not isinstance(payload, dict):
        raise ValueError(f"web test JSON must be an object: {path}")
    return {
        "runtime_tests": payload.get("numTotalTests"),
        "runtime_failed": payload.get("numFailedTests"),
    }


def head_sha(root: Path) -> str:
    try:
        result = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=False,
        )
    except OSError:
        return ""
    if result.returncode != 0:
        return ""
    return result.stdout.strip()


def e2e_section(root: Path, files: list[str]) -> dict:
    specs = [rel for rel in files if rel.endswith(".spec.ts")]
    loc = sum(len(code_lines(read_text(root, rel))) for rel in files)
    tests = sum(count_matches(read_text(root, rel), TS_TESTCASE) for rel in specs)
    assertions = sum(len(re.findall(r"\bexpect(?:\.soft|\.poll)?\(", read_text(root, rel))) for rel in files)
    timeouts = sum(len(re.findall(r"\btest\.setTimeout\(", read_text(root, rel))) for rel in files)
    return {
        "spec_files": len(specs),
        "all_files": len(files),
        "loc": loc,
        "tests_static": tests,
        "loc_per_test": round(loc / tests, 1) if tests else None,
        "assertions": assertions,
        "set_timeout_calls": timeouts,
    }


def build_report(args: argparse.Namespace) -> dict:
    root = Path(args.repo).resolve()
    if not root.is_dir():
        raise ValueError(f"repository path is not a directory: {args.repo}")
    for label, value in (
        ("go coverage profile", args.go_cover),
        ("package-local go coverage profile", args.go_cover_local),
        ("go test JSON", args.go_json),
        ("web coverage JSON", args.web_cover),
        ("package-local web coverage JSON", args.web_cover_local),
        ("web test JSON", args.web_json),
        ("mobile coverage JSON", args.mobile_cover),
    ):
        if value and not Path(value).is_file():
            raise ValueError(f"could not read {label}: {value}")

    groups = classify(root)
    report = {"tool": "count-tests.py", "version": VERSION, "sha": head_sha(root)}

    go_covered = go_coverable = None
    statement_hit = statement_total = 0
    if args.go_cover:
        go_covered, go_coverable, statement_hit, statement_total = parse_go_cover(root, args.go_cover)
    go_local = parse_go_cover(root, args.go_cover_local)[0] if args.go_cover_local else None
    go_extra: dict = {}
    if args.go_cover:
        go_extra["go_tool_statement_coverage_pct"] = (
            round(100 * statement_hit / statement_total, 1) if statement_total else None
        )
    if args.go_json:
        go_extra.update(go_runtime(args.go_json))
    report["go"] = language_section(
        root,
        groups["go_test"],
        groups["go_prod"],
        go_covered,
        "go",
        go_extra,
        go_coverable,
        go_local,
    )

    web_covered = web_coverable = None
    if args.web_cover:
        web_covered, web_coverable = parse_istanbul(root, args.web_cover)
    web_local = parse_istanbul(root, args.web_cover_local)[0] if args.web_cover_local else None
    web_extra = web_runtime(args.web_json) if args.web_json else {}
    report["web_unit"] = language_section(
        root,
        groups["web_test"],
        groups["web_prod"],
        web_covered,
        "web_prod",
        web_extra,
        web_coverable,
        web_local,
    )

    report["e2e"] = e2e_section(root, groups["e2e"])

    if groups["mobile_test"] or groups["mobile_prod"]:
        mobile_covered = mobile_coverable = None
        if args.mobile_cover:
            mobile_covered, mobile_coverable = parse_istanbul(root, args.mobile_cover)
        # Mobile uses the web signal names (vi.fn, expect) and per-directory
        # buckets, matching the v1.0 report.
        report["mobile"] = language_section(
            root,
            groups["mobile_test"],
            groups["mobile_prod"],
            mobile_covered,
            "mobile",
            None,
            mobile_coverable,
            None,
        )
    else:
        report["mobile"] = None

    report["excluded"] = {
        "generated_go_files": len(groups["generated"]),
        "fixture_testdata_files": len(groups["fixtures"]),
    }
    languages = [
        report[name]
        for name in ("go", "web_unit", "mobile")
        if report.get(name) and report[name].get("density") is not None
    ]
    test_total = sum(item["test_loc"] for item in languages)
    covered_total = sum(item["covered_prod_loc"] for item in languages)
    report["overall_unit"] = {
        "test_loc": test_total,
        "covered_prod_loc": covered_total,
        "density": density_value(test_total, covered_total),
    }
    return report


def format_human(report: dict, top: int) -> str:
    lines = [f"count-tests.py v{report['version']}  sha {report['sha'] or 'unknown'}"]
    for name in ("go", "web_unit", "mobile"):
        section = report.get(name)
        if not section:
            lines.append(f"\n[{name}] not present")
            continue
        lines.append(f"\n[{name}]")
        for key in sorted(key for key in section if key not in {"dirs", "files"}):
            lines.append(f"  {key:34} {section[key]}")
        dirs = section["dirs"]
        ranked = [
            (key, bucket)
            for key, bucket in dirs.items()
            if bucket["density"] is not None and bucket["covered_loc"] >= 50 and bucket["test_loc"] > 0
        ]
        elsewhere = sorted(
            key for key, bucket in dirs.items() if bucket["test_loc"] == 0 and bucket["covered_loc"] > 0
        )
        lines.append(f"  -- dirs with no local tests (covered only by tests elsewhere): {elsewhere}")
        ranked.sort(key=lambda item: (item[1]["density"], item[0]))
        lines.append(f"  -- leanest {top} dirs (covered_loc>=50): density test_loc/covered_loc cov%")
        for key, bucket in ranked[:top]:
            lines.append(
                f"     {key:44} {bucket['density']:7}  {bucket['test_loc']:6}/{bucket['covered_loc']:<6} {bucket['covered_pct']}%"
            )
        lines.append(f"  -- heaviest {top} dirs")
        for key, bucket in ranked[::-1][:top]:
            lines.append(
                f"     {key:44} {bucket['density']:7}  {bucket['test_loc']:6}/{bucket['covered_loc']:<6} {bucket['covered_pct']}%"
            )
        lowest = sorted(
            (bucket["covered_pct"], key)
            for key, bucket in dirs.items()
            if bucket["coverable_loc"] >= 30 and bucket["covered_pct"] is not None
        )
        lines.append(f"  -- lowest coverage {top} dirs (coverable_loc>=30), % of coverable LOC")
        for pct, key in lowest[:top]:
            lines.append(f"     {key:44} {pct}%  prod_loc {dirs[key]['prod_loc']}")
    lines.append("\n[e2e]")
    for key, value in sorted(report["e2e"].items()):
        lines.append(f"  {key:34} {value}")
    lines.append(f"\n[overall_unit] {report['overall_unit']}")
    lines.append(f"[excluded] {report['excluded']}")
    return "\n".join(lines)


def write_json(path: str, payload: dict) -> None:
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        json.dump(payload, handle, indent=1, sort_keys=True)
        handle.write("\n")


def load_report(path: str) -> dict:
    try:
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
    except json.JSONDecodeError as err:
        raise ValueError(f"report JSON could not be parsed: {path}") from err
    if not isinstance(payload, dict):
        raise ValueError(f"report JSON must be an object: {path}")
    return payload


def field_delta(base: dict, head: dict, key: str) -> dict | None:
    if key not in base or key not in head:
        return None
    before, after = base[key], head[key]
    if before is None or after is None:
        return {"base": before, "head": after, "delta": None}
    if isinstance(before, bool) or isinstance(after, bool):
        return None
    if isinstance(before, (int, float)) and isinstance(after, (int, float)):
        if isinstance(before, int) and isinstance(after, int) and not isinstance(before, bool):
            delta: int | float | None = after - before
        else:
            delta = round(after - before, 3)
        return {"base": before, "head": after, "delta": delta}
    return None


def marginal_density(delta_test: int, delta_covered: int) -> float | None:
    """Δ test LOC / Δ covered prod LOC. Undefined unless covered lines grew."""
    if delta_covered <= 0:
        return None
    quantized = (Decimal(delta_test) / Decimal(delta_covered)).quantize(
        Decimal("0.001"), rounding=ROUND_HALF_UP
    )
    return float(quantized)


def marginal_exceeds(delta_test: int, delta_covered: int, threshold: Decimal) -> bool:
    if delta_covered <= 0:
        return False
    return Decimal(delta_test) / Decimal(delta_covered) > threshold


def language_diff(base: dict | None, head: dict | None, name: str) -> dict | None:
    if not base or not head:
        return None
    fields = {}
    for key in (
        "test_loc",
        "prod_loc",
        "covered_prod_loc",
        "coverable_prod_loc",
        "density",
        "assertions",
        "assertions_per_100_test_loc",
        "test_funcs",
        "t_run",
        "rapid_check",
        "test_cases_static",
        "vi_fn",
        "vi_mock",
        "fast_check",
        "skips",
        "sleeps",
        "runtime_tests_and_subtests",
        "runtime_top_level_tests",
        "runtime_failed",
        "runtime_tests",
        "go_tool_statement_coverage_pct",
        "covered_pct_of_coverable_loc",
        "test_to_code_ratio",
        "covered_elsewhere_loc",
    ):
        delta = field_delta(base, head, key)
        if delta is not None:
            fields[key] = delta
    test_delta = head.get("test_loc", 0) - base.get("test_loc", 0)
    covered_delta = head.get("covered_prod_loc", 0) - base.get("covered_prod_loc", 0)
    fields["marginal_density"] = marginal_density(test_delta, covered_delta)
    threshold = FLAG_A_ABOVE.get(name)
    fields["flag_a"] = bool(threshold and marginal_exceeds(test_delta, covered_delta, threshold))
    return fields


def added_line_numbers(base_text: str | None, head_text: str) -> set[int]:
    head_lines = head_text.splitlines()
    if base_text is None:
        return set(range(1, len(head_lines) + 1))
    added: set[int] = set()
    matcher = difflib.SequenceMatcher(a=base_text.splitlines(), b=head_lines, autojunk=False)
    for tag, _i1, _i2, j1, j2 in matcher.get_opcodes():
        if tag in {"insert", "replace"}:
            added.update(range(j1 + 1, j2 + 1))
    return added


def read_repo_file(root: Path | None, rel: str) -> str | None:
    if root is None:
        return None
    if rel.startswith("/") or ".." in rel.split("/"):
        return None
    path = root / rel
    if not path.is_file():
        return None
    return path.read_text(encoding="utf-8", errors="replace")


def flag_c(base: dict, head: dict, base_repo: Path | None, head_repo: Path | None) -> dict:
    """New production lines run only by tests outside the file's package.

    Go (and web, when a package-local coverage file was passed) uses the
    set difference between the all-tests profile and the package-local profile.
    Without a local profile, a new covered line counts only when its directory
    has no test file. Lines in a directory that already has tests stay
    unattributed, because one istanbul file cannot say which test ran them.
    """
    result = {
        "fired": False,
        "evaluated": False,
        "count": 0,
        "lines": [],
        "unattributed_new_covered_lines": 0,
        "reason": "",
    }
    if base_repo is None or head_repo is None:
        result["reason"] = "Pass --base-repo and --head-repo to attribute new lines."
        return result

    lines: list[dict] = []
    unattributed = 0
    files_present = 0
    files_read = 0
    for language in ("go", "web_unit", "mobile"):
        section = head.get(language)
        if not section or "files" not in section:
            continue
        dirs = section.get("dirs") or {}
        for rel, record in sorted(section["files"].items()):
            files_present += 1
            if record.get("covered_lines") is None:
                continue
            head_text = read_repo_file(head_repo, rel)
            if head_text is None:
                continue
            files_read += 1
            base_text = read_repo_file(base_repo, rel)
            new_lines = added_line_numbers(base_text, head_text)
            code = set(record.get("code_lines") or [])
            new_covered = new_lines & code & set(record["covered_lines"])
            if not new_covered:
                continue
            local = record.get("local_covered_lines")
            if local is None:
                bucket = dirs.get(dir_key(rel)) or {}
                if bucket.get("test_loc", 0) == 0:
                    flagged = new_covered
                else:
                    unattributed += len(new_covered)
                    flagged = set()
            else:
                flagged = new_covered - set(local)
            head_lines = head_text.splitlines()
            for number in sorted(flagged):
                text = head_lines[number - 1].strip() if 0 < number <= len(head_lines) else ""
                lines.append(
                    {
                        "language": language,
                        "file": rel,
                        "line": number,
                        "text": text.replace("`", "'")[:120],
                    }
                )

    if files_present and files_read == 0:
        result["reason"] = "Source files for the head report were not in --head-repo."
        return result

    lines.sort(key=lambda item: (item["language"], item["file"], item["line"]))
    result["evaluated"] = True
    result["count"] = len(lines)
    result["fired"] = bool(lines)
    result["lines"] = lines[:FLAG_C_JSON_LIMIT]
    result["truncated"] = len(lines) > FLAG_C_JSON_LIMIT
    result["unattributed_new_covered_lines"] = unattributed
    result["reason"] = (
        "Go uses the package-local coverprofile when the report has one. "
        "Web uses a package-local coverage file when one was passed; otherwise "
        "only new covered lines in directories with no test file are counted. "
        "New covered lines in a web directory that already has tests are unattributed."
    )
    return result


def build_diff(base: dict, head: dict, base_repo: Path | None, head_repo: Path | None) -> dict:
    languages = {}
    fired = []
    for name in ("go", "web_unit", "mobile"):
        diff = language_diff(base.get(name), head.get(name), name)
        if diff is None:
            continue
        languages[name] = diff
        if diff.get("flag_a"):
            fired.append(name)
    overall = language_diff(base.get("overall_unit"), head.get("overall_unit"), "overall_unit")
    # Overall has no flag A threshold. language_diff sets flag_a false because
    # "overall_unit" is not in FLAG_A_ABOVE.
    return {
        "tool": "count-tests.py",
        "version": VERSION,
        "base_sha": base.get("sha") or "",
        "head_sha": head.get("sha") or "",
        "languages": languages,
        "overall_unit": overall,
        "flag_a": {
            "fired": bool(fired),
            "languages": fired,
            "thresholds": {"go": 5.2, "web_unit": 6.3},
        },
        "flag_b": {"implemented": False, "todo": flag_b_todo()},
        "flag_c": flag_c(base, head, base_repo, head_repo),
    }


def short_sha(sha: str) -> str:
    return sha[:7] if sha else "unknown"


def format_delta(delta, digits: int | None = None) -> str:
    if delta is None:
        return "n/a"
    if isinstance(delta, int):
        return f"{delta:+,}"
    places = 3 if digits is None else digits
    return f"{delta:+.{places}f}"


def format_pair(field: dict | None, digits: int | None = None) -> str:
    if not field:
        return "n/a"
    before, after, delta = field["base"], field["head"], field["delta"]
    if isinstance(before, int) and isinstance(after, int):
        return f"{before:,} → {after:,} ({format_delta(delta)})"
    places = 3 if digits is None else digits
    before_text = "n/a" if before is None else f"{before:.{places}f}"
    after_text = "n/a" if after is None else f"{after:.{places}f}"
    return f"{before_text} → {after_text} ({format_delta(delta, places)})"


def format_markdown(diff: dict) -> str:
    lines = [
        "<!-- stockbot-test-metrics -->",
        "",
        "**Test calorie density** is advisory. It does not fail this check and it does not block merge.",
        "",
        "Density is non-blank, non-comment test lines divided by production lines a test actually ran. "
        "Marginal density is the test lines added in this pull request divided by the covered production "
        "lines added. It is undefined when covered lines did not increase.",
        "",
        "| Language | Test LOC | Covered prod LOC | Density | Marginal density |",
        "| --- | ---: | ---: | ---: | ---: |",
    ]
    labels = {"go": "Go", "web_unit": "Web unit", "mobile": "Mobile"}
    for name, label in labels.items():
        section = diff["languages"].get(name)
        if not section:
            continue
        marginal = section.get("marginal_density")
        marginal_text = "n/a (covered lines did not increase)" if marginal is None else f"{marginal:.3f}"
        lines.append(
            "| {label} | {tests} | {covered} | {density} | {marginal} |".format(
                label=label,
                tests=format_pair(section.get("test_loc")),
                covered=format_pair(section.get("covered_prod_loc")),
                density=format_pair(section.get("density")),
                marginal=marginal_text,
            )
        )
    overall = diff.get("overall_unit") or {}
    if overall:
        marginal = overall.get("marginal_density")
        marginal_text = "n/a (covered lines did not increase)" if marginal is None else f"{marginal:.3f}"
        lines.append(
            "| {label} | {tests} | {covered} | {density} | {marginal} |".format(
                label="Overall unit",
                tests=format_pair(overall.get("test_loc")),
                covered=format_pair(overall.get("covered_prod_loc")),
                density=format_pair(overall.get("density")),
                marginal=marginal_text,
            )
        )

    flag_a = diff["flag_a"]
    lines.extend(["", "**Flag A — marginal density above 2× baseline** (Go > 5.2, web > 6.3)."])
    if flag_a["fired"]:
        for name in flag_a["languages"]:
            section = diff["languages"][name]
            lines.append(
                f"- {labels.get(name, name)} marginal density is {section['marginal_density']:.3f}, "
                f"above {float(FLAG_A_ABOVE[name]):.1f}."
            )
    else:
        lines.append("Not fired.")

    flag_c = diff["flag_c"]
    lines.extend(["", "**Flag C — new lines covered only by other packages' tests.**"])
    if not flag_c["evaluated"]:
        lines.append(f"Not evaluated. {flag_c['reason']}")
    elif not flag_c["fired"]:
        lines.append("Not fired.")
    else:
        lines.append(f"{flag_c['count']} new production line(s):")
        for item in flag_c["lines"][:FLAG_C_MARKDOWN_LIMIT]:
            text = f" `{item['text']}`" if item.get("text") else ""
            lines.append(f"- `{item['file']}:{item['line']}`{text}")
        hidden = flag_c["count"] - min(flag_c["count"], FLAG_C_MARKDOWN_LIMIT)
        if hidden > 0:
            lines.append(f"- …and {hidden} more")
    if flag_c["evaluated"] and flag_c["unattributed_new_covered_lines"]:
        lines.append(
            f"{flag_c['unattributed_new_covered_lines']} new covered line(s) sit in a directory that "
            "already has tests and had no package-local coverage file, so they were not attributed."
        )
    if flag_c["evaluated"]:
        lines.append(flag_c["reason"])

    lines.extend(["", f"**Flag B — shorter but weaker.** {diff['flag_b']['todo']}"])

    signals = []
    for name, label in labels.items():
        section = diff["languages"].get(name) or {}
        for key, title in (
            ("vi_fn", "vi.fn("),
            ("rapid_check", "rapid.Check"),
            ("fast_check", "fast-check"),
            ("sleeps", "time.Sleep"),
            ("assertions_per_100_test_loc", "assertions/100 test lines"),
        ):
            field = section.get(key)
            if not field or field.get("delta") in (None, 0):
                continue
            digits = 2 if key == "assertions_per_100_test_loc" else None
            signals.append(f"{label} {title} {format_pair(field, digits)}")
    if signals:
        lines.extend(["", "Other signals: " + "; ".join(signals) + "."])

    lines.extend(
        [
            "",
            f"`count-tests.py` v{diff['version']} · base `{short_sha(diff['base_sha'])}` · "
            f"head `{short_sha(diff['head_sha'])}`",
        ]
    )
    return "\n".join(lines) + "\n"


def format_diff_text(diff: dict) -> str:
    lines = [f"delta {short_sha(diff['base_sha'])} -> {short_sha(diff['head_sha'])}"]
    for name in ("go", "web_unit", "mobile"):
        section = diff["languages"].get(name)
        if not section:
            continue
        lines.append(f"[{name}]")
        for key in COMMENT_KEYS:
            field = section.get(key)
            if field and field.get("base") is not None and field.get("head") is not None:
                lines.append(f"  {key:28} {field['base']} -> {field['head']}  ({field['delta']})")
        lines.append(f"  {'marginal_density':28} {section.get('marginal_density')}")
        lines.append(f"  {'flag_a':28} {section.get('flag_a')}")
    overall = diff.get("overall_unit") or {}
    if overall.get("test_loc") and overall.get("covered_prod_loc"):
        lines.append(
            "[pr] added test LOC {tests}, added covered prod LOC {covered}, marginal density {marginal}".format(
                tests=format_delta(overall["test_loc"]["delta"]),
                covered=format_delta(overall["covered_prod_loc"]["delta"]),
                marginal=overall.get("marginal_density"),
            )
        )
    lines.append(f"[flag A] fired={diff['flag_a']['fired']} languages={diff['flag_a']['languages']}")
    flag_c = diff["flag_c"]
    lines.append(
        f"[flag C] evaluated={flag_c['evaluated']} fired={flag_c['fired']} "
        f"lines={flag_c['count']} unattributed={flag_c['unattributed_new_covered_lines']}"
    )
    lines.append(f"[flag B] {diff['flag_b']['todo']}")
    return "\n".join(lines)


def require_repo(path: str | None) -> Path | None:
    if not path:
        return None
    root = Path(path).resolve()
    if not root.is_dir():
        raise ValueError(f"repository path is not a directory: {path}")
    return root


def cmd_report(args: argparse.Namespace) -> int:
    report = build_report(args)
    if args.json:
        write_json(args.json, report)
    print(format_human(report, args.top))
    return 0


def cmd_diff(args: argparse.Namespace) -> int:
    for label, value in (("base report", args.base), ("head report", args.head)):
        if not Path(value).is_file():
            raise ValueError(f"could not read {label}: {value}")
    base_repo = require_repo(args.base_repo)
    head_repo = require_repo(args.head_repo)
    if (base_repo is None) != (head_repo is None):
        raise ValueError("pass both --base-repo and --head-repo, or neither")
    diff = build_diff(load_report(args.base), load_report(args.head), base_repo, head_repo)
    if args.json:
        write_json(args.json, diff)
    if args.markdown:
        Path(args.markdown).write_text(format_markdown(diff), encoding="utf-8", newline="\n")
    print(format_diff_text(diff))
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Count test calorie density (advisory; never a merge gate).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    sub = parser.add_subparsers(dest="cmd", required=True)

    report = sub.add_parser("report", help="Count test size and calorie density for one tree")
    report.add_argument("--repo", required=True, help="Checkout to measure")
    report.add_argument("--go-cover", help="Coverprofile from go test -coverpkg over the module")
    report.add_argument(
        "--go-cover-local",
        help="Coverprofile from go test without -coverpkg. Used for flag C, not for density",
    )
    report.add_argument("--go-json", help="go test -json output, for runtime test counts")
    report.add_argument("--web-cover", help="Vitest v8 coverage-final.json")
    report.add_argument(
        "--web-cover-local",
        help="Optional per-directory Vitest coverage. Used for flag C, not for density",
    )
    report.add_argument("--web-json", help="Vitest JSON reporter output")
    report.add_argument("--mobile-cover", help="Istanbul coverage-final.json for mobile/, when present")
    report.add_argument("--json", help="Write the machine-readable report to this path")
    report.add_argument("--top", type=int, default=8, help="How many directories to list at each extreme")

    diff = sub.add_parser("diff", help="Marginal density and advisory flags between two reports")
    diff.add_argument("base", help="Report JSON for the base commit")
    diff.add_argument("head", help="Report JSON for the head commit")
    diff.add_argument("--base-repo", help="Checkout the base report was measured on")
    diff.add_argument("--head-repo", help="Checkout the head report was measured on")
    diff.add_argument("--json", help="Write the machine-readable delta to this path")
    diff.add_argument("--markdown", help="Write the sticky pull-request comment to this path")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.cmd == "report":
            return cmd_report(args)
        return cmd_diff(args)
    except (OSError, ValueError, KeyError) as err:
        print(f"count-tests.py: {err}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
