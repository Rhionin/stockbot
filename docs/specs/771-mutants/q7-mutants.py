#!/usr/bin/env python3
"""Mutant check for #771 Q7 (household amount 0 is don't restock for groups).
Run from the repo root:
   python3 docs/specs/771-mutants/q7-mutants.py
Each mutant must make go test ./internal/supply -run 'TestReadMonthsQ7|TestZZ771Q7' fail."""
import pathlib, subprocess, sys
SQL = pathlib.Path("internal/supply/sql.go")
GOAL = pathlib.Path("internal/restock/group_goal.go")
CMD = ["go", "test", "./internal/supply", "-run", "TestReadMonthsQ7|TestZZ771Q7", "-count=1"]
MUTANTS = [
    ("amount 0 -> default 3",
     "if window == 0 {\n\t\treturn 0, nil",
     "if window == 0 {\n\t\treturn DefaultMonths, nil",
     SQL),
    ("every amount -> 0",
     "if home.Kind == KindAmount {\n\t\treturn \"amount\", 3",
     "if home.Kind == KindAmount {\n\t\treturn \"dont_restock\", 0",
     GOAL),
    ("inverted == 0",
     "if window == 0 {\n\t\treturn 0, nil",
     "if window != 0 {\n\t\treturn 0, nil",
     SQL),
    ("reads the kind key instead of the value",
     "raw, err := readHouseholdSetting(ctx, scope, q, keyTargetValue)",
     "raw, err := readHouseholdSetting(ctx, scope, q, keyTargetKind)",
     SQL),
]
origs = {p: p.read_text() for p in {SQL, GOAL}}
survived = []
try:
    for name, old, new, path in MUTANTS:
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