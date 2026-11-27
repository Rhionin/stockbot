#!/usr/bin/env python3
"""Mutant check for 032_group_goals.sql. Run from the repo root:
   python3 docs/specs/771-mutants/032-mutants.py
Each mutant edits the migration text, runs the 771 migration tests, and must FAIL."""
import pathlib, subprocess, sys
SQL = pathlib.Path("internal/app/migrations/032_group_goals.sql")
CMD = ["go", "test", "./internal/app", "-run", "TestMigration032", "-count=1"]
MUTANTS = [
    ("largest->smallest amount", "THEN MAX(CASE WHEN b.kind = 'amount'", "THEN MIN(CASE WHEN b.kind = 'amount'"),
    ("largest->smallest months", "ELSE MAX(CASE WHEN b.kind = 'months'", "ELSE MIN(CASE WHEN b.kind = 'months'"),
    ("months beats amount", "> 0 THEN 'items' ELSE 'months' END", "> 0 THEN 'months' ELSE 'items' END"),
    ("back up don't-restock 0 too", "WHERE t.value > 0\nON CONFLICT", "WHERE t.value >= 0\nON CONFLICT"),
    ("delete without the backup check", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value > 0\n  AND 1 OR EXISTS"),
    ("delete don't-restock 0 too (no backup check)", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value >= 0\n  AND 1 OR EXISTS"),
    ("overwrite an existing goal", "WHERE b.migration_id = '032'\n  AND g.window_months IS NULL AND g.quantity_base_value IS NULL AND g.goal_items IS NULL", "WHERE b.migration_id = '032'"),
    ("un-mark group lines too", "AND COALESCE(group_id, '') = ''", ""),
    ("no edit lifetime", "SET touched_expires_trip = (SELECT h.trip_seq + 1", "SET touched_expires_trip = (SELECT h.trip_seq"),
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
finally:
    SQL.write_text(orig)
sys.exit(1 if survived else 0)
