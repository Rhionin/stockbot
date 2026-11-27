#!/usr/bin/env python3
"""Mutant check for 032_group_goals.sql. Run from the repo root:
   python3 docs/specs/771-mutants/032-mutants.py
Each mutant edits the migration (or the Appendix B restore) text, runs
   go test ./internal/app -run TestMigration032 -count=1
and must make it FAIL (KILLED)."""
import pathlib, subprocess, sys
SQL = pathlib.Path("internal/app/migrations/032_group_goals.sql")
RESTORE = pathlib.Path("internal/app/testdata/032_restore.sql")
CMD = ["go", "test", "./internal/app", "-run", "TestMigration032", "-count=1"]
MUTANTS = [
    ("largest->smallest amount", "THEN MAX(CASE WHEN b.kind = 'amount'", "THEN MIN(CASE WHEN b.kind = 'amount'"),
    ("largest->smallest months", "ELSE MAX(CASE WHEN b.kind = 'months'", "ELSE MIN(CASE WHEN b.kind = 'months'"),
    ("months beats amount", "> 0 THEN 'items' ELSE 'months' END", "> 0 THEN 'months' ELSE 'items' END"),
    ("back up don't-restock 0 too", "WHERE t.value > 0\nON CONFLICT", "WHERE t.value >= 0\nON CONFLICT"),
    ("delete without the backup check", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value > 0\n  AND 1 OR EXISTS"),
    ("delete don't-restock 0 too (no backup check)", "DELETE FROM item_targets\nWHERE value > 0\n  AND EXISTS", "DELETE FROM item_targets\nWHERE value >= 0\n  AND 1 OR EXISTS"),
    ("plan a goal for groups that already have one", "WHERE b.migration_id = '032'\n  AND g.window_months IS NULL AND g.quantity_base_value IS NULL AND g.goal_items IS NULL", "WHERE b.migration_id = '032'"),
    ("un-mark group lines too", "AND COALESCE(group_id, '') = ''", ""),
    ("no edit lifetime", "SET touched_expires_trip = (SELECT h.trip_seq + 1", "SET touched_expires_trip = (SELECT h.trip_seq"),
    ("CHECK allows 0 and 1000", "goal_items BETWEEN 1 AND 999", "goal_items BETWEEN 0 AND 1000"),
    ("CHECK allows goal_items with an amount goal", "AND window_months IS NULL\n        AND quantity_base_value IS NULL\n        AND quantity_dimension IS NULL));", "AND window_months IS NULL));"),
    ("restore clears an items goal a person changed", "AND product_groups.goal_items = bg.new_value ", "", RESTORE),
    ("restore clears a months goal a person changed", "AND product_groups.window_months = bg.new_value ", "", RESTORE),
    ("restore overwrites a re-added target", "ON CONFLICT (household_id, item_id) DO NOTHING", "ON CONFLICT (household_id, item_id) DO UPDATE SET kind = excluded.kind, value = excluded.value", RESTORE),
    ("restore skips untouched rows too", "SET goal_items = NULL, window_months = NULL\nWHERE EXISTS", "SET goal_items = goal_items, window_months = window_months\nWHERE EXISTS", RESTORE),
]
origs = {SQL: SQL.read_text(), RESTORE: RESTORE.read_text()}
survived = []
try:
    for m in MUTANTS:
        name, old, new = m[:3]
        path = m[3] if len(m) > 3 else SQL
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