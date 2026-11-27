#!/usr/bin/env python3
"""Mutant check for the in_group guard in targets.go. Run from repo root:
   python3 docs/specs/771-mutants/guard-mutants.py"""
import pathlib
import subprocess
import sys

TARGETS = pathlib.Path("internal/restock/targets.go")
HANDLER = pathlib.Path("internal/server/handler_restock.go")
CMD = ["go", "test", "./internal/restock", "./internal/server", "-run", "InGroup|TestRestock", "-count=1"]

MUTANTS = [
    ("guard off", "if target.Value > 0 {", "if false && target.Value > 0 {", TARGETS),
    (">0 becomes >=0", "if target.Value > 0 {", "if target.Value >= 0 {", TARGETS),
    (
        "unscoped membership lookup",
        "func itemGroupMembership(ctx context.Context, scope tenant.Scope, q Queryer, itemID string) (groupID, groupName string, err error) {\n\tif err := scope.Require(); err != nil {\n\t\treturn \"\", \"\", err\n\t}\n\terr = q.QueryRowContext(ctx, `\n\t\tSELECT m.group_id, g.name\n\t\tFROM items i\n\t\tJOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id",
        "func itemGroupMembership(ctx context.Context, scope tenant.Scope, q Queryer, itemID string) (groupID, groupName string, err error) {\n\tif err := scope.Require(); err != nil {\n\t\treturn \"\", \"\", err\n\t}\n\terr = q.QueryRowContext(ctx, `\n\t\tSELECT m.group_id, g.name\n\t\tFROM items i\n\t\tJOIN product_group_members m ON m.product_id = i.product_id",
        TARGETS,
    ),
    (
        "refusal writes anyway",
        "return &InGroupError{GroupID: groupID, GroupName: groupName}",
        "/*in_group*/",
        TARGETS,
    ),
    (
        "wrong group name",
        "GroupName: groupName",
        'GroupName: "wrong"',
        TARGETS,
    ),
    (
        "handler-only guard",
        "var inGroup *restock.InGroupError",
        "var inGroupRemoved *restock.InGroupError",
        HANDLER,
    ),
]

origs = {p: p.read_text() for p in {TARGETS, HANDLER}}
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