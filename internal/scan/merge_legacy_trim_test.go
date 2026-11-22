package scan_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/barcode"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestCreateScanEntry_MergesTrimmedRescanIntoPaddedLegacyRow(t *testing.T) {
	const upc = "036000291452"
	padded := " " + upc + "\t\r\n"
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	t0 := time.Now()
	t1 := t0.Add(time.Minute)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO scan_entries (id, user_id, household_id, barcode, scanned_at, direction, unit_count, status)
		VALUES ('legacy-padded', ?, ?, ?, ?, 'stock_in', 1, 'pending')`,
		"merge-user", scope.HouseholdID(), padded, t0); err != nil {
		t.Fatal(err)
	}

	merged, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "merge-user", Barcode: upc, ScannedAt: t1,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged == nil || merged.ID != "legacy-padded" {
		t.Fatalf("merged into %+v, want legacy-padded id", merged)
	}
	if merged.UnitCount != 2 {
		t.Fatalf("unit_count = %d, want 2", merged.UnitCount)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND `+barcode.SQLTrimExpr("barcode")+` = ?`,
		scope.HouseholdID(), upc).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("open rows = %d err=%v, want 1", rows, err)
	}
}

func TestFindMergeableScanEntryExplainQueryPlanBoundsByHousehold(t *testing.T) {
	_, db, scope := newTestQueue(t)
	exact := mergeableExplainSQL(mergeableBarcodeExactMatchForTest())
	padded := mergeableExplainSQL(mergeableBarcodePaddedLegacyMatchForTest())
	for name, q := range map[string]string{"exact": exact, "padded_legacy": padded} {
		t.Run(name, func(t *testing.T) {
			plan, err := explainQueryPlan(db, q, scope.HouseholdID(), scope.HouseholdID(), scope.HouseholdID(), "stock_in", "036000291452")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("EXPLAIN QUERY PLAN (%s):\n%s", name, plan)
			if !strings.Contains(strings.ToLower(plan), "household") && !strings.Contains(plan, "se.household_id") {
				t.Fatalf("EXPLAIN QUERY PLAN does not show household filter:\n%s", plan)
			}
		})
	}
}

func mergeableExplainSQL(barcodePredicate string) string {
	return `EXPLAIN QUERY PLAN ` + scanMergeSelectForExplain() + scanMergeFiltersForExplain() + barcodePredicate + ` ORDER BY se.scanned_at ASC, se.id ASC LIMIT 1`
}

// Test hooks mirror merge_lookup.go predicates without exporting them.
func mergeableBarcodeExactMatchForTest() string { return ` AND se.barcode = ?` }
func mergeableBarcodePaddedLegacyMatchForTest() string {
	return ` AND ` + barcode.SQLTrimExpr("se.barcode") + ` = ?`
}
func scanMergeSelectForExplain() string {
	return `SELECT se.id FROM scan_entries se`
}
func scanMergeFiltersForExplain() string {
	return ` WHERE se.household_id = ? AND se.user_id = ? AND se.direction IS ? AND se.status IN ('pending', 'flagged')`
}

func explainQueryPlan(db *sql.DB, query string, args ...any) (string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%d|%d|%d|%s", id, parent, notused, detail))
	}
	return strings.Join(lines, "\n"), rows.Err()
}
