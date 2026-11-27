#!/usr/bin/env python3
"""Mutant check for #771 Q7 (household amount 0 is don't restock for groups).
Run from the repo root:
   python3 docs/specs/771-mutants/q7-mutants.py
Each mutant edits readMonths, runs the supply tests, and must make them FAIL."""
import pathlib, subprocess, sys
SRC = pathlib.Path("internal/supply/sql.go")
CMD = ["go", "test", "./internal/supply", "-count=1"]
MUTANTS = [
    ("amount 0 -> 3", "\tif n == 0 {\n\t\treturn 0, nil\n\t}\n\t// A household amount", "\tif n == 0 && kind != string(restock.KindAmount) {\n\t\treturn 0, nil\n\t}\n\t// A household amount"),
    ("every amount -> 0", "\tif kind == string(restock.KindAmount) {\n\t\treturn DefaultMonths, nil\n\t}", "\tif kind == string(restock.KindAmount) {\n\t\treturn 0, nil\n\t}"),
    ("inverted == 0", "\tif n == 0 {\n\t\treturn 0, nil\n\t}\n\t// A household amount", "\tif n != 0 {\n\t\treturn 0, nil\n\t}\n\t// A household amount"),
    ("reads the kind key instead of the value", "raw, err := readHouseholdSetting(ctx, scope, q, keyTargetValue)\n\tif err != nil {\n\t\treturn 0, fmt.Errorf(\"could not read the supply length", "raw, err := readHouseholdSetting(ctx, scope, q, keyTargetKind)\n\tif err != nil {\n\t\treturn 0, fmt.Errorf(\"could not read the supply length"),
]
orig = SRC.read_text()
survived = []
try:
    for name, old, new in MUTANTS:
        if old not in orig:
            sys.exit(f"mutant anchor missing: {name}")
        SRC.write_text(orig.replace(old, new, 1))
        r = subprocess.run(CMD, capture_output=True, text=True)
        status = "KILLED" if r.returncode != 0 else "SURVIVED"
        print(f"{status}: {name}")
        if r.returncode == 0:
            survived.append(name)
finally:
    SRC.write_text(orig)
sys.exit(1 if survived else 0)
