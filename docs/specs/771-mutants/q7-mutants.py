#!/usr/bin/env python3
"""Mutant check for readMonths Q7 (§5.6). Run from the repo root:
   python3 docs/specs/771-mutants/q7-mutants.py"""
import pathlib, subprocess, sys
SQL = pathlib.Path("internal/supply/sql.go")
CMD = ["go", "test", "./internal/supply", "-run", "TestReadMonthsQ7|TestZZ771Q7", "-count=1"]
MUTANTS = [
    ("amount 0 maps to default 3", "if n == 0 {\n\t\t\treturn DefaultKindDontRestock, 0, nil\n\t\t}", "/* removed zero amount */"),
    ("every amount maps to 0", "\t\treturn DefaultKindAmount, DefaultMonths, nil", "\t\treturn DefaultKindDontRestock, 0, nil"),
    ("amount zero check inverted", "if n == 0 {", "if n != 0 {"),
    ("amount reads kind key not value", 'value, err := readHouseholdSetting(ctx, scope, q, keyTargetValue)', 'value, err := readHouseholdSetting(ctx, scope, q, keyTargetKind)'),
]
orig = SQL.read_text()
survived = []
try:
    for name, old, new in MUTANTS:
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        SQL.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        status = "KILLED" if r.returncode != 0 else "SURVIVED"
        print(f"{status}: {name}")
        if r.returncode == 0:
            survived.append(name)
        SQL.write_text(orig)
finally:
    SQL.write_text(orig)
sys.exit(1 if survived else 0)