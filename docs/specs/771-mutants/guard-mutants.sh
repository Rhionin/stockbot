#!/usr/bin/env bash
# Mutant check for the #771 in_group guard. Run from the repo root:
#   bash docs/specs/771-mutants/guard-mutants.sh
# Each mutant edits the guard, runs the guard tests, and must make them FAIL.
set -u
TARGETS=internal/restock/targets.go
GUARD=internal/restock/in_group.go
CMD=(go test ./internal/restock ./internal/server -run 'InGroup|TestRestock' -count=1)

mutants=(
  "guard off|$TARGETS|		if inGroup != nil {\n			return inGroup\n		}|		_ = inGroup"
  "value >0 -> >=0|$TARGETS|	if target.Value > 0 {\n		inGroup, err|	if target.Value >= 0 {\n		inGroup, err"
  "household-unscoped membership|$GUARD|JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id|JOIN product_group_members m ON m.product_id = i.product_id"
)

survived=0
for entry in "${mutants[@]}"; do
  IFS='|' read -r name file old new <<<"$entry"
  backup=$(mktemp)
  cp "$file" "$backup"
  if ! python3 -c 'import sys,pathlib
p=pathlib.Path(sys.argv[1]); old=sys.argv[2].encode().decode("unicode_escape"); new=sys.argv[3].encode().decode("unicode_escape")
s=p.read_text()
if old not in s: sys.exit("anchor missing")
p.write_text(s.replace(old,new,1))' "$file" "$old" "$new"; then
    echo "anchor missing: $name"; cp "$backup" "$file"; rm -f "$backup"; exit 2
  fi
  if "${CMD[@]}" >/dev/null 2>&1; then
    echo "SURVIVED: $name"; survived=1
  else
    echo "KILLED: $name"
  fi
  cp "$backup" "$file"; rm -f "$backup"
done
exit $survived
