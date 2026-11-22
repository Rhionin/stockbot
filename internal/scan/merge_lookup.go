package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Rhionin/stockbot/internal/barcode"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func mergeableScanEntrySelectSQL() string {
	return `
		SELECT 
			se.id, se.user_id, se.barcode, se.scanned_at, se.direction, se.unit_count, 
			se.expires_at, se.status, se.product_id, se.committed_at, se.created_at,
			se.removed_count, se.requested_count,
			p.id, ` + product.DisplayNameSQL("p") + `, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), p.pack_count
		FROM scan_entries se
		LEFT JOIN products p ON p.id = se.product_id`
}

func mergeableScanEntryFilters() string {
	return ` WHERE se.household_id = ? AND se.user_id = ? AND se.direction IS ? AND se.status IN ('pending', 'flagged')`
}

func mergeableBarcodeExactMatch() string {
	return ` AND se.barcode = ?`
}

func mergeableBarcodePaddedLegacyMatch() string {
	return ` AND ` + barcode.SQLTrimExpr("se.barcode") + ` = ?`
}

func queryMergeableScanEntry(ctx context.Context, q queryRower, scope tenant.Scope, userID, barcode string, direction *ScanDirection, barcodePredicate string) (*ScanEntry, error) {
	var directionStr *string
	if direction != nil {
		directionStr = ptr(string(*direction))
	}
	row := q.QueryRowContext(ctx,
		mergeableScanEntrySelectSQL()+mergeableScanEntryFilters()+barcodePredicate+`
		ORDER BY se.scanned_at ASC, se.id ASC
		LIMIT 1`,
		scope.HouseholdID(), scope.HouseholdID(), userID, nullableString(directionStr), barcode,
	)
	entry, err := scanScanEntry(row, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("queryMergeableScanEntry: %w", err)
	}
	return entry, nil
}

func findMergeableScanEntryOn(ctx context.Context, q queryRower, scope tenant.Scope, userID, barcode string, direction *ScanDirection) (*ScanEntry, error) {
	entry, err := queryMergeableScanEntry(ctx, q, scope, userID, barcode, direction, mergeableBarcodeExactMatch())
	if err != nil {
		return nil, err
	}
	if entry != nil {
		return entry, nil
	}
	return queryMergeableScanEntry(ctx, q, scope, userID, barcode, direction, mergeableBarcodePaddedLegacyMatch())
}
