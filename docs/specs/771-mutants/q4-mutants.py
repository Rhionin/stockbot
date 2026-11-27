#!/usr/bin/env python3
"""Mutant check for grouped items skipping category (§6.5 F14). Run from the repo root:
   python3 docs/specs/771-mutants/q4-mutants.py"""
import pathlib, subprocess, sys
TARGET = pathlib.Path("internal/restock/target.go")
TARGETS = pathlib.Path("internal/restock/targets.go")
CMD = ["go", "test", "./internal/supply", "-run", "TestRestockGroupedItemSkipsCategoryInGroup|TestPlanGroupIgnoresCategoryTarget", "-count=1"]
MUTANTS = [
    ("group next falls through categories", "\t\tnext := Resolution{Target: in.Household, Level: LevelHousehold}", "\t\tnext := resolveCategories(in)", TARGET),
    ("drop grouped-without-goal household path", "\tif in.GroupName != \"\" {\n\t\treturn Resolution{Target: in.Household, Level: LevelHousehold}\n\t}", "\tif in.GroupName != \"\" {\n\t\treturn resolveCategories(in)\n\t}", TARGET),
    ("grouped items still get categories in Load", "\t\tif item.view.GroupID != \"\" {", "\t\tif false && item.view.GroupID != \"\" {", TARGETS),
    ("ungrouped skip categories branch", "} else {\n\t\t\tinput.Categories = item.categories\n\t\t}", "} else {\n\t\t\tinput.Categories = nil\n\t\t}", TARGETS),
]
origs = {TARGET: TARGET.read_text(), TARGETS: TARGETS.read_text()}
survived = []
try:
    for m in MUTANTS:
        name, old, new, path = m
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