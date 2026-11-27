#!/usr/bin/env python3
"""Mutant check for the in_group guard in targets.go. Run from repo root:
   python3 docs/specs/771-mutants/guard-mutants.py"""
import pathlib
import subprocess
import sys

TARGETS = pathlib.Path("internal/restock/targets.go")
INGROUP = pathlib.Path("internal/restock/in_group.go")
HANDLER = pathlib.Path("internal/server/handler_restock.go")
CMD = ["go", "test", "./internal/restock", "./internal/server", "-run", "InGroup|TestRestock", "-count=1"]

MUTANTS = [
    ("guard off", "if target.Value > 0 {", "if false && target.Value > 0 {", TARGETS),
    (">0 becomes >=0", "if target.Value > 0 {", "if target.Value >= 0 {", TARGETS),
    (
        "unscoped membership lookup",
        "JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id",
        "JOIN product_group_members m ON m.product_id = i.product_id",
        INGROUP,
    ),
    (
        "refusal writes anyway",
        "\t\tif inGroup != nil {\n\t\t\treturn inGroup\n\t\t}",
        "\t\tif inGroup != nil {\n\t\t\t_ = inGroup\n\t\t}",
        TARGETS,
    ),
    (
        "wrong group name",
        "return &InGroupError{GroupID: id, GroupName: name}",
        "return &InGroupError{GroupID: id, GroupName: id}",
        INGROUP,
    ),
    (
        "handler-only guard",
        "if errors.As(err, &inGroup) {",
        "if false && errors.As(err, &inGroup) {",
        HANDLER,
    ),
]

origs = {p: p.read_text() for p in {TARGETS, INGROUP, HANDLER}}
survived = []
try:
    for entry in MUTANTS:
        name, old, new, path = entry
        orig = origs[path]
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        path.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        status = "KILLED" if r.returncode != 0 else "SURVIVED"
        print(f"{status}: {name}")
        if r.returncode == 0:
            survived.append(name)
        path.write_text(orig)
finally:
    for path, text in origs.items():
        path.write_text(text)
sys.exit(1 if survived else 0)