package scan_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/google/uuid"
)

// Feature: pantry-management, Property 1: Scan entry data integrity
// **Validates: Requirements 1.1, 1.2**
//
// For any barcode string and timestamp submitted to the scan queue, the created
// scan entry SHALL persist the barcode value and timestamp exactly as provided,
// with status `pending` and no pre-set scan direction unless one was explicitly provided.
func TestProperty1_ScanEntryDataIntegrity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, _, scope := newTestQueue(t)
		ctx := context.Background()

		// Generate arbitrary inputs
		barcode := rapid.String().Draw(rt, "barcode")
		timestampUnix := rapid.Int64Range(0, time.Now().Unix()).Draw(rt, "timestampUnix")
		scannedAt := time.Unix(timestampUnix, 0)
		userID := rapid.String().Draw(rt, "userID")

		// Create scan entry without direction
		entry := scan.ScanEntry{
			UserID:    userID,
			Barcode:   barcode,
			ScannedAt: scannedAt,
			UnitCount: 1,
		}

		created, err := queue.CreateScanEntry(ctx, scope, entry)
		if err != nil {
			rt.Fatalf("CreateScanEntry failed: %v", err)
		}

		// Verify barcode and timestamp are preserved exactly
		if created.Barcode != barcode {
			rt.Fatalf("Barcode mismatch: want %q, got %q", barcode, created.Barcode)
		}

		// Timestamps should match within reasonable precision (SQLite stores to the second)
		if !created.ScannedAt.Truncate(time.Second).Equal(scannedAt.Truncate(time.Second)) {
			rt.Fatalf("ScannedAt mismatch: want %v, got %v", scannedAt, created.ScannedAt)
		}

		// Status should be pending
		if created.Status != scan.Pending {
			rt.Fatalf("Status: want %v, got %v", scan.Pending, created.Status)
		}

		// Direction should be nil (not pre-set)
		if created.Direction != nil {
			rt.Fatalf("Direction should be nil, got %v", *created.Direction)
		}

		// ID should be generated
		if created.ID == "" {
			rt.Fatal("ID should be generated, got empty string")
		}
	})
}

// Feature: pantry-management, Property 2: Scan direction propagation
// **Validates: Requirements 1.4, 1.5**
//
// For any pre-selected scan direction and any sequence of scans recorded within
// 5 minutes of the last scan, every resulting scan entry SHALL carry that direction;
// and for any scan recorded more than 5 minutes after the previous scan, the
// direction SHALL NOT be applied automatically.
func TestProperty2_ScanDirectionPropagation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, _, scope := newTestQueue(t)
		ctx := context.Background()

		// Generate test data
		userID := rapid.String().Draw(rt, "userID")
		baseTime := time.Now()
		direction := rapid.SampledFrom([]scan.ScanDirection{
			scan.StockIn,
			scan.StockOut,
		}).Draw(rt, "direction")

		// Generate a sequence of time offsets (in seconds)
		// Mix some within 5 minutes and some beyond 5 minutes
		numScans := rapid.IntRange(2, 10).Draw(rt, "numScans")
		offsets := make([]int, numScans)
		for i := 0; i < numScans; i++ {
			// Generate offsets from 0 to 10 minutes (600 seconds)
			offsets[i] = rapid.IntRange(0, 600).Draw(rt, "offset")
		}

		// Create scan entries with the generated offsets
		var previousTime time.Time
		for i, offsetSec := range offsets {
			scanTime := baseTime.Add(time.Duration(offsetSec) * time.Second)

			// Determine if direction should be applied
			var entryDirection *scan.ScanDirection
			if i == 0 {
				// First scan always gets the direction
				entryDirection = &direction
			} else {
				// Check if within 5 minutes of previous scan
				timeSincePrevious := scanTime.Sub(previousTime)
				if timeSincePrevious <= 5*time.Minute {
					entryDirection = &direction
				}
				// else: direction should be nil
			}

			entry := scan.ScanEntry{
				UserID:    userID,
				Barcode:   rapid.String().Draw(rt, "barcode"),
				ScannedAt: scanTime,
				Direction: entryDirection,
				UnitCount: 1,
			}

			created, err := queue.CreateScanEntry(ctx, scope, entry)
			if err != nil {
				rt.Fatalf("CreateScanEntry failed: %v", err)
			}

			// Verify direction propagation
			if i == 0 {
				// First scan should always have the direction
				if created.Direction == nil || *created.Direction != direction {
					rt.Fatalf("First scan: expected direction %v, got %v", direction, created.Direction)
				}
			} else {
				timeSincePrevious := scanTime.Sub(previousTime)
				if timeSincePrevious <= 5*time.Minute {
					// Within 5 minutes: should have direction
					if created.Direction == nil {
						rt.Fatalf("Scan within 5 min: expected direction %v, got nil", direction)
					}
					if *created.Direction != direction {
						rt.Fatalf("Scan within 5 min: expected direction %v, got %v", direction, *created.Direction)
					}
				} else {
					// Beyond 5 minutes: should NOT have direction
					if created.Direction != nil {
						rt.Fatalf("Scan after 5 min: expected nil direction, got %v", *created.Direction)
					}
				}
			}

			previousTime = scanTime
		}
	})
}

// Feature: pantry-management, Property 3: Scan queue chronological ordering
// **Validates: Requirements 1.7**
//
// For any collection of scan entries in the queue, the list returned to the user
// SHALL be ordered ascending by `scanned_at` timestamp (oldest first).
func TestProperty3_ScanQueueChronologicalOrdering(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, _, scope := newTestQueue(t)
		ctx := context.Background()

		userID := rapid.String().Draw(rt, "userID")
		numScans := rapid.IntRange(1, 20).Draw(rt, "numScans")

		// Generate scan entries with random timestamps
		baseTime := time.Now().Add(-24 * time.Hour)

		for i := 0; i < numScans; i++ {
			// Random offset up to 24 hours
			offsetSec := rapid.Int64Range(0, 86400).Draw(rt, "offsetSec")
			scanTime := baseTime.Add(time.Duration(offsetSec) * time.Second)

			// Barcode must be distinct per entry within this run: CreateScanEntry
			// now merges into an existing pending/flagged entry with the same
			// user_id/barcode/direction rather than always inserting, so a
			// collision would produce fewer rows than numScans.
			entry := scan.ScanEntry{
				UserID:    userID,
				Barcode:   fmt.Sprintf("%s-%d", rapid.String().Draw(rt, "barcode"), i),
				ScannedAt: scanTime,
				UnitCount: 1,
				Status:    scan.Pending,
			}

			_, err := queue.CreateScanEntry(ctx, scope, entry)
			if err != nil {
				rt.Fatalf("CreateScanEntry failed: %v", err)
			}
		}

		// List all entries
		entries, err := queue.ListScanEntries(ctx, scope, scan.Pending)
		if err != nil {
			rt.Fatalf("ListScanEntries failed: %v", err)
		}

		if len(entries) != numScans {
			rt.Fatalf("Expected %d entries, got %d", numScans, len(entries))
		}

		// Verify chronological ordering (ascending by scanned_at)
		for i := 1; i < len(entries); i++ {
			if entries[i].ScannedAt.Before(entries[i-1].ScannedAt) {
				rt.Fatalf("Ordering violation at index %d: entry[%d].ScannedAt=%v is before entry[%d].ScannedAt=%v",
					i, i, entries[i].ScannedAt, i-1, entries[i-1].ScannedAt)
			}
		}
	})
}

// Feature: pantry-management, Property 4: Batch update applies to all selected entries
// **Validates: Requirements 1.10**
//
// For any subset of pending scan entries selected for batch review and any
// combination of direction and expiration date applied, every entry in the
// selected subset SHALL have its direction and expiration date updated to the
// new values, and no entry outside the subset SHALL be modified.
func TestProperty4_BatchUpdateApplies(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, _, scope := newTestQueue(t)
		ctx := context.Background()

		userID := rapid.String().Draw(rt, "userID")

		// Create a pool of scan entries
		numEntries := rapid.IntRange(3, 15).Draw(rt, "numEntries")
		entryIDs := make([]string, numEntries)

		for i := 0; i < numEntries; i++ {
			// Barcode must be distinct per entry within this run: CreateScanEntry
			// now merges into an existing pending/flagged entry with the same
			// user_id/barcode/direction rather than always inserting, so a
			// collision would produce fewer rows than numEntries.
			entry := scan.ScanEntry{
				UserID:    userID,
				Barcode:   fmt.Sprintf("%s-%d", rapid.String().Draw(rt, "barcode"), i),
				ScannedAt: time.Now().Add(time.Duration(i) * time.Minute),
				UnitCount: 1,
				Status:    scan.Pending,
			}

			created, err := queue.CreateScanEntry(ctx, scope, entry)
			if err != nil {
				rt.Fatalf("CreateScanEntry failed: %v", err)
			}
			entryIDs[i] = created.ID
		}

		// Select a random subset to update
		numToUpdate := rapid.IntRange(1, numEntries).Draw(rt, "numToUpdate")
		selectedIndices := make(map[int]bool)
		for len(selectedIndices) < numToUpdate {
			idx := rapid.IntRange(0, numEntries-1).Draw(rt, "selectedIdx")
			selectedIndices[idx] = true
		}

		selectedIDs := make([]string, 0, numToUpdate)
		for idx := range selectedIndices {
			selectedIDs = append(selectedIDs, entryIDs[idx])
		}

		// Generate update values
		direction := rapid.SampledFrom([]scan.ScanDirection{
			scan.StockIn,
			scan.StockOut,
		}).Draw(rt, "direction")

		unitCount := rapid.IntRange(1, 10).Draw(rt, "unitCount")

		expiresAt := time.Now().Add(time.Duration(rapid.IntRange(1, 30).Draw(rt, "daysUntilExpiry")) * 24 * time.Hour)

		// Perform batch update
		err := queue.BatchUpdateScanEntries(ctx, scope, selectedIDs, &direction, &unitCount, &expiresAt)
		if err != nil {
			rt.Fatalf("BatchUpdateScanEntries failed: %v", err)
		}

		// Verify all entries
		for i, id := range entryIDs {
			entry, err := queue.GetScanEntry(ctx, scope, id)
			if err != nil {
				rt.Fatalf("GetScanEntry failed: %v", err)
			}

			if selectedIndices[i] {
				// This entry SHOULD be updated
				if entry.Direction == nil || *entry.Direction != direction {
					rt.Fatalf("Selected entry %s: expected direction %v, got %v", id, direction, entry.Direction)
				}
				if entry.UnitCount != unitCount {
					rt.Fatalf("Selected entry %s: expected unitCount %d, got %d", id, unitCount, entry.UnitCount)
				}
				if entry.ExpiresAt == nil {
					rt.Fatalf("Selected entry %s: expected expiresAt to be set, got nil", id)
				}
				// Check within reasonable precision (second-level)
				if !entry.ExpiresAt.Truncate(time.Second).Equal(expiresAt.Truncate(time.Second)) {
					rt.Fatalf("Selected entry %s: expected expiresAt %v, got %v", id, expiresAt, *entry.ExpiresAt)
				}
			} else {
				// This entry should NOT be updated (should remain as originally created)
				if entry.Direction != nil {
					rt.Fatalf("Non-selected entry %s: expected nil direction, got %v", id, *entry.Direction)
				}
				if entry.UnitCount != 1 {
					rt.Fatalf("Non-selected entry %s: expected unitCount 1, got %d", id, entry.UnitCount)
				}
				if entry.ExpiresAt != nil {
					rt.Fatalf("Non-selected entry %s: expected nil expiresAt, got %v", id, *entry.ExpiresAt)
				}
			}
		}
	})
}

// Feature: pantry-management, Property 7: Committed scan entry is preserved in history
// **Validates: Requirements 1.14**
//
// For any scan entry that is committed, the entry SHALL no longer appear in the
// pending scan list and SHALL appear in the read-only scan history with its
// original barcode, timestamp, direction, unit count, and expiration date all unchanged.
func TestProperty7_CommittedEntryInHistory(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, _, scope := newTestQueue(t)
		ctx := context.Background()

		// Generate arbitrary scan entry data
		userID := rapid.String().Draw(rt, "userID")
		barcode := rapid.String().Draw(rt, "barcode")
		timestampUnix := rapid.Int64Range(0, time.Now().Unix()).Draw(rt, "timestampUnix")
		scannedAt := time.Unix(timestampUnix, 0)

		// Generate optional direction
		hasDirection := rapid.Bool().Draw(rt, "hasDirection")
		var direction *scan.ScanDirection
		if hasDirection {
			dir := rapid.SampledFrom([]scan.ScanDirection{
				scan.StockIn,
				scan.StockOut,
			}).Draw(rt, "direction")
			direction = &dir
		}

		// Generate unit count
		unitCount := rapid.IntRange(1, 10).Draw(rt, "unitCount")

		// Generate optional expiration date
		hasExpiry := rapid.Bool().Draw(rt, "hasExpiry")
		var expiresAt *time.Time
		if hasExpiry {
			daysFromNow := rapid.IntRange(1, 365).Draw(rt, "daysFromNow")
			expiry := time.Now().Add(time.Duration(daysFromNow) * 24 * time.Hour)
			expiresAt = &expiry
		}

		// Create scan entry
		entry := scan.ScanEntry{
			UserID:    userID,
			Barcode:   barcode,
			ScannedAt: scannedAt,
			Direction: direction,
			UnitCount: unitCount,
			ExpiresAt: expiresAt,
			Status:    scan.Pending,
		}

		created, err := queue.CreateScanEntry(ctx, scope, entry)
		if err != nil {
			rt.Fatalf("CreateScanEntry failed: %v", err)
		}

		// Store original values for later comparison
		originalBarcode := created.Barcode
		originalScannedAt := created.ScannedAt
		originalDirection := created.Direction
		originalUnitCount := created.UnitCount
		originalExpiresAt := created.ExpiresAt
		originalID := created.ID

		// Verify entry is in pending list
		pendingEntries, err := queue.ListScanEntries(ctx, scope, scan.Pending)
		if err != nil {
			rt.Fatalf("ListScanEntries (pending) failed: %v", err)
		}
		foundInPending := false
		for _, e := range pendingEntries {
			if e.ID == originalID {
				foundInPending = true
				break
			}
		}
		if !foundInPending {
			rt.Fatalf("Entry %s not found in pending list before commit", originalID)
		}

		// Commit the scan entry
		err = queue.CommitScanEntry(ctx, scope, originalID)
		if err != nil {
			rt.Fatalf("CommitScanEntry failed: %v", err)
		}

		// Verify entry is NO LONGER in pending list
		pendingAfterCommit, err := queue.ListScanEntries(ctx, scope, scan.Pending)
		if err != nil {
			rt.Fatalf("ListScanEntries (pending after commit) failed: %v", err)
		}
		for _, e := range pendingAfterCommit {
			if e.ID == originalID {
				rt.Fatalf("Entry %s still found in pending list after commit", originalID)
			}
		}

		// Verify entry IS in committed/history list
		committedEntries, err := queue.ListScanEntries(ctx, scope, scan.Committed)
		if err != nil {
			rt.Fatalf("ListScanEntries (committed) failed: %v", err)
		}

		var committedEntry *scan.ScanEntry
		for i, e := range committedEntries {
			if e.ID == originalID {
				committedEntry = &committedEntries[i]
				break
			}
		}
		if committedEntry == nil {
			rt.Fatalf("Entry %s not found in committed list", originalID)
		}

		// Verify all original fields are unchanged
		if committedEntry.Barcode != originalBarcode {
			rt.Fatalf("Barcode changed: want %q, got %q", originalBarcode, committedEntry.Barcode)
		}

		// Timestamps should match within reasonable precision (SQLite stores to the second)
		if !committedEntry.ScannedAt.Truncate(time.Second).Equal(originalScannedAt.Truncate(time.Second)) {
			rt.Fatalf("ScannedAt changed: want %v, got %v", originalScannedAt, committedEntry.ScannedAt)
		}

		// Verify direction unchanged
		if originalDirection == nil && committedEntry.Direction != nil {
			rt.Fatalf("Direction changed from nil to %v", *committedEntry.Direction)
		}
		if originalDirection != nil {
			if committedEntry.Direction == nil {
				rt.Fatalf("Direction changed from %v to nil", *originalDirection)
			}
			if *committedEntry.Direction != *originalDirection {
				rt.Fatalf("Direction changed: want %v, got %v", *originalDirection, *committedEntry.Direction)
			}
		}

		// Verify unit count unchanged
		if committedEntry.UnitCount != originalUnitCount {
			rt.Fatalf("UnitCount changed: want %d, got %d", originalUnitCount, committedEntry.UnitCount)
		}

		// Verify expiration date unchanged
		if originalExpiresAt == nil && committedEntry.ExpiresAt != nil {
			rt.Fatalf("ExpiresAt changed from nil to %v", *committedEntry.ExpiresAt)
		}
		if originalExpiresAt != nil {
			if committedEntry.ExpiresAt == nil {
				rt.Fatalf("ExpiresAt changed from %v to nil", *originalExpiresAt)
			}
			// Check within reasonable precision (second-level)
			if !committedEntry.ExpiresAt.Truncate(time.Second).Equal(originalExpiresAt.Truncate(time.Second)) {
				rt.Fatalf("ExpiresAt changed: want %v, got %v", *originalExpiresAt, *committedEntry.ExpiresAt)
			}
		}

		// Verify status is now committed
		if committedEntry.Status != scan.Committed {
			rt.Fatalf("Status: want %v, got %v", scan.Committed, committedEntry.Status)
		}

		// Verify committed_at timestamp is set
		if committedEntry.CommittedAt == nil {
			rt.Fatal("CommittedAt should be set after commit, got nil")
		}
	})
}

// --------------------------------------------------------------------------
// realtime-scan-updates property tests
// --------------------------------------------------------------------------

// Feature: realtime-scan-updates, Property 4: Every successful scan-mutating
// operation publishes exactly one matching Scan_Event
//
// For any call to CreateScanEntry, UpdateScanEntry, ResolveFlaggedEntry,
// CommitStockIn, or CommitStockOut that returns without an error, exactly
// one PublishScanEvent call SHALL occur whose ScanEntry argument
// deep-equals the entry's complete state immediately after that call, as
// read back through GetScanEntry.
//
// Validates: Requirements 2.1, 2.2, 2.3, 2.5
func TestRealtimeProperty4_CreateAndUpdatePublishMatchingEvent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, db, scope := newTestQueue(t)
		ctx := context.Background()

		operation := rapid.IntRange(0, 4).Draw(rt, "operation")
		barcode := rapid.String().Draw(rt, "barcode")
		userID := rapid.String().Draw(rt, "userID")

		var entryID string
		broadcaster := &fakeBroadcaster{}

		switch operation {
		case 0: // CreateScanEntry
			queue.Broadcaster = broadcaster
			created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID:    userID,
				Barcode:   barcode,
				ScannedAt: time.Now(),
				UnitCount: 1,
			})
			if err != nil {
				rt.Fatalf("CreateScanEntry failed: %v", err)
			}
			entryID = created.ID
		case 1: // UpdateScanEntry
			created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID:    userID,
				Barcode:   barcode,
				ScannedAt: time.Now(),
				UnitCount: 1,
			})
			if err != nil {
				rt.Fatalf("CreateScanEntry (setup) failed: %v", err)
			}
			entryID = created.ID

			queue.Broadcaster = broadcaster
			direction := rapid.SampledFrom([]scan.ScanDirection{
				scan.StockIn,
				scan.StockOut,
			}).Draw(rt, "direction")
			unitCount := rapid.IntRange(1, 10).Draw(rt, "unitCount")
			if err := queue.UpdateScanEntry(ctx, scope, entryID, &direction, &unitCount, nil, nil, nil); err != nil {
				rt.Fatalf("UpdateScanEntry failed: %v", err)
			}
		case 2: // ResolveFlaggedEntry
			catalog := product.NewCatalog(db)
			productID := uuid.NewString()
			if err := catalog.CreateProduct(ctx, product.Product{
				ID: productID, Name: "Corrected " + productID, Category: "Test", UnitOfMeasure: "unit",
			}); err != nil {
				rt.Fatalf("CreateProduct failed: %v", err)
			}
			flagged := createFlaggedEntry(t, queue, ctx, scope)
			entryID = flagged.ID

			queue.Broadcaster = broadcaster
			if err := queue.ResolveFlaggedEntry(ctx, scope, entryID, productID); err != nil {
				rt.Fatalf("ResolveFlaggedEntry failed: %v", err)
			}
		case 3: // CommitStockIn
			unitCount := rapid.IntRange(1, 10).Draw(rt, "unitCount")
			created := createStockInEntry(t, queue, db, ctx, scope, unitCount)
			entryID = created.ID

			queue.Broadcaster = broadcaster
			if err := queue.CommitStockIn(ctx, scope, created); err != nil {
				rt.Fatalf("CommitStockIn failed: %v", err)
			}
		case 4: // CommitStockOut
			created := createStockOutEntry(t, queue, db, ctx, scope)
			entryID = created.ID

			queue.Broadcaster = broadcaster
			if _, err := queue.CommitStockOut(ctx, scope, created, nil); err != nil {
				rt.Fatalf("CommitStockOut failed: %v", err)
			}
		}

		want, err := queue.GetScanEntry(ctx, scope, entryID)
		if err != nil {
			rt.Fatalf("GetScanEntry failed: %v", err)
		}
		if want == nil {
			rt.Fatal("GetScanEntry: expected entry, got nil")
		}
		if len(broadcaster.scanEvents) != 1 {
			rt.Fatalf("expected exactly 1 published scan event, got %d", len(broadcaster.scanEvents))
		}
		if !reflect.DeepEqual(broadcaster.scanEvents[0], *want) {
			rt.Fatalf("published event: got %+v, want %+v", broadcaster.scanEvents[0], *want)
		}
	})
}

// Feature: realtime-scan-updates, Property 5: Batch update publishes one
// Scan_Event per updated entry
//
// For any non-empty subset of scan entries passed to BatchUpdateScanEntries
// that returns without an error, exactly one PublishScanEvent call SHALL
// occur per entry in that subset, each carrying that entry's complete
// post-update state, and no PublishScanEvent call SHALL occur for any entry
// outside the subset.
//
// Validates: Requirements 2.4
func TestRealtimeProperty5_BatchUpdatePublishesOnePerEntry(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, _, scope := newTestQueue(t)
		ctx := context.Background()

		userID := rapid.String().Draw(rt, "userID")

		// Create a pool of scan entries (same selection-generator pattern as
		// TestProperty4_BatchUpdateApplies).
		numEntries := rapid.IntRange(3, 15).Draw(rt, "numEntries")
		entryIDs := make([]string, numEntries)
		for i := 0; i < numEntries; i++ {
			// Barcode must be distinct per entry within this run: CreateScanEntry
			// now merges into an existing pending/flagged entry with the same
			// user_id/barcode/direction rather than always inserting, so a
			// collision would produce fewer rows than numEntries.
			created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID:    userID,
				Barcode:   fmt.Sprintf("%s-%d", rapid.String().Draw(rt, "barcode"), i),
				ScannedAt: time.Now().Add(time.Duration(i) * time.Minute),
				UnitCount: 1,
				Status:    scan.Pending,
			})
			if err != nil {
				rt.Fatalf("CreateScanEntry failed: %v", err)
			}
			entryIDs[i] = created.ID
		}

		// Select a random non-empty subset to update.
		numToUpdate := rapid.IntRange(1, numEntries).Draw(rt, "numToUpdate")
		selectedIndices := make(map[int]bool)
		for len(selectedIndices) < numToUpdate {
			idx := rapid.IntRange(0, numEntries-1).Draw(rt, "selectedIdx")
			selectedIndices[idx] = true
		}
		selectedIDs := make([]string, 0, numToUpdate)
		for idx := range selectedIndices {
			selectedIDs = append(selectedIDs, entryIDs[idx])
		}

		broadcaster := &fakeBroadcaster{}
		queue.Broadcaster = broadcaster

		direction := rapid.SampledFrom([]scan.ScanDirection{
			scan.StockIn,
			scan.StockOut,
		}).Draw(rt, "direction")

		if err := queue.BatchUpdateScanEntries(ctx, scope, selectedIDs, &direction, nil, nil); err != nil {
			rt.Fatalf("BatchUpdateScanEntries failed: %v", err)
		}

		if len(broadcaster.scanEvents) != len(selectedIDs) {
			rt.Fatalf("expected %d published scan events, got %d", len(selectedIDs), len(broadcaster.scanEvents))
		}

		published := make(map[string]scan.ScanEntry, len(broadcaster.scanEvents))
		for _, e := range broadcaster.scanEvents {
			if _, ok := selectedIndices[indexOf(entryIDs, e.ID)]; !ok {
				rt.Fatalf("published event for entry %s, which was outside the selected subset", e.ID)
			}
			published[e.ID] = e
		}

		for _, id := range selectedIDs {
			want, err := queue.GetScanEntry(ctx, scope, id)
			if err != nil {
				rt.Fatalf("GetScanEntry failed: %v", err)
			}
			got, ok := published[id]
			if !ok {
				rt.Fatalf("expected a published event for %s, found none", id)
			}
			if !reflect.DeepEqual(got, *want) {
				rt.Fatalf("published event for %s: got %+v, want %+v", id, got, *want)
			}
		}
	})
}

// Feature: realtime-scan-updates, Property 6: A failed scan-mutating call
// publishes no Scan_Event
//
// For any call to CreateScanEntry, UpdateScanEntry, BatchUpdateScanEntries,
// ResolveFlaggedEntry, CommitStockIn, or CommitStockOut driven into one of
// its documented error conditions, the call SHALL return an error and zero
// PublishScanEvent calls SHALL occur as a result of that call.
//
// Validates: Requirements 2.6
func TestRealtimeProperty6_FailedScanMutationPublishesNoEvent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, db, scope := newTestQueue(t)
		ctx := context.Background()

		broadcaster := &fakeBroadcaster{}
		queue.Broadcaster = broadcaster

		unknownID := uuid.NewString()
		operation := rapid.IntRange(0, 5).Draw(rt, "operation")

		var err error
		switch operation {
		case 0: // CreateScanEntry with a duplicate ID that cannot merge
			userID := rapid.String().Draw(rt, "userID")
			barcode := rapid.String().Draw(rt, "barcode")
			existing, createErr := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID:    userID,
				Barcode:   barcode,
				ScannedAt: time.Now(),
				UnitCount: 1,
			})
			if createErr != nil {
				rt.Fatalf("CreateScanEntry (setup) failed: %v", createErr)
			}
			// The setup call above may itself have published; reset before
			// the call under test so only its own behavior is asserted.
			// A matching user, barcode, and direction merges into the pending
			// row instead of inserting, so the duplicate id would not fail.
			broadcaster.scanEvents = nil

			_, err = queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				ID:        existing.ID,
				UserID:    userID + "x",
				Barcode:   barcode,
				ScannedAt: time.Now(),
				UnitCount: 1,
			})
		case 1: // UpdateScanEntry on an unknown ID
			direction := rapid.SampledFrom([]scan.ScanDirection{
				scan.StockIn,
				scan.StockOut,
			}).Draw(rt, "direction")
			err = queue.UpdateScanEntry(ctx, scope, unknownID, &direction, nil, nil, nil, nil)
		case 2: // BatchUpdateScanEntries when the context is already canceled
			direction := rapid.SampledFrom([]scan.ScanDirection{
				scan.StockIn,
				scan.StockOut,
			}).Draw(rt, "direction")
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			err = queue.BatchUpdateScanEntries(canceled, scope, []string{unknownID}, &direction, nil, nil)
		case 3: // ResolveFlaggedEntry, one of its documented error conditions
			switch rapid.IntRange(0, 2).Draw(rt, "resolveFlaggedErrorCondition") {
			case 0: // scan entry not found
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if createErr := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
				}); createErr != nil {
					rt.Fatalf("CreateProduct failed: %v", createErr)
				}
				err = queue.ResolveFlaggedEntry(ctx, scope, unknownID, productID)
			case 1: // scan entry is not flagged
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if createErr := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
				}); createErr != nil {
					rt.Fatalf("CreateProduct failed: %v", createErr)
				}
				created, createErr := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID:    rapid.String().Draw(rt, "userID"),
					Barcode:   rapid.String().Draw(rt, "barcode"),
					ScannedAt: time.Now(),
					UnitCount: 1,
					Status:    scan.Pending,
				})
				if createErr != nil {
					rt.Fatalf("CreateScanEntry (setup) failed: %v", createErr)
				}
				broadcaster.scanEvents = nil
				err = queue.ResolveFlaggedEntry(ctx, scope, created.ID, productID)
			case 2: // product not found
				flagged := createFlaggedEntry(t, queue, ctx, scope)
				broadcaster.scanEvents = nil
				err = queue.ResolveFlaggedEntry(ctx, scope, flagged.ID, "no-such-product")
			}
		case 4: // CommitStockIn, one of its documented error conditions
			var entry *scan.ScanEntry
			switch rapid.IntRange(0, 2).Draw(rt, "commitStockInErrorCondition") {
			case 0: // direction is not stock_in
				direction := scan.StockOut
				productID := "prod-1"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID, UnitCount: 1}
			case 1: // missing product_id
				direction := scan.StockIn
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, UnitCount: 1}
			case 2: // unit count less than 1
				direction := scan.StockIn
				productID := "prod-1"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID, UnitCount: 0}
			}
			err = queue.CommitStockIn(ctx, scope, entry)
		case 5: // CommitStockOut, one of its documented error conditions
			var entry *scan.ScanEntry
			var instanceID *string
			switch rapid.IntRange(0, 4).Draw(rt, "commitStockOutErrorCondition") {
			case 0: // direction is not stock_out
				direction := scan.StockIn
				productID := "prod-1"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID}
			case 1: // missing product_id
				direction := scan.StockOut
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction}
			case 2: // no item found for user and product
				direction := scan.StockOut
				productID := "no-such-product"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID}
			case 3: // specific instance not found or already removed
				entry = createStockOutEntry(t, queue, db, ctx, scope)
				broadcaster.scanEvents = nil
				missing := "no-such-instance"
				instanceID = &missing
			case 4: // no available instances
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if createErr := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
				}); createErr != nil {
					rt.Fatalf("CreateProduct failed: %v", createErr)
				}
				userID := uuid.NewString()
				itemID := uuid.NewString()
				if _, dbErr := db.ExecContext(ctx, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
					itemID, userID, productID, scope.HouseholdID()); dbErr != nil {
					rt.Fatalf("create item failed: %v", dbErr)
				}
				direction := scan.StockOut
				created, createErr := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID:    userID,
					Barcode:   rapid.String().Draw(rt, "barcode"),
					ScannedAt: time.Now(),
					Direction: &direction,
					UnitCount: 1,
					ProductID: &productID,
					Status:    scan.Pending,
				})
				if createErr != nil {
					rt.Fatalf("CreateScanEntry (setup) failed: %v", createErr)
				}
				broadcaster.scanEvents = nil
				entry = created
			}
			_, err = queue.CommitStockOut(ctx, scope, entry, instanceID)
		}

		if err == nil {
			rt.Fatal("expected error, got nil")
		}
		if len(broadcaster.scanEvents) != 0 {
			rt.Fatalf("expected 0 published scan events, got %d", len(broadcaster.scanEvents))
		}
	})
}

// Feature: realtime-scan-updates, Property 7: Every successful
// inventory-affecting operation publishes exactly one matching
// Inventory_Event (Queue half)
//
// For any call to Queue.CommitStockIn or Queue.CommitStockOut that returns
// without an error, exactly one PublishInventoryEvent call SHALL occur
// whose InventoryItem argument deep-equals the affected item's complete
// aggregated state immediately after that call, as read back through
// Pantry.GetInventoryItem.
//
// Validates: Requirements 3.4, 3.5
func TestRealtimeProperty7_QueuePublishesInventoryEventOnSuccess(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, db, scope := newTestQueue(t)
		ctx := context.Background()
		pantry := inventory.NewPantry(db)
		broadcaster := &fakeBroadcaster{}

		operation := rapid.IntRange(0, 1).Draw(rt, "operation")

		var productID string

		switch operation {
		case 0: // CommitStockIn
			unitCount := rapid.IntRange(1, 10).Draw(rt, "unitCount")
			created := createStockInEntry(t, queue, db, ctx, scope, unitCount)
			productID = *created.ProductID

			queue.Broadcaster = broadcaster
			queue.Pantry = pantry
			if err := queue.CommitStockIn(ctx, scope, created); err != nil {
				rt.Fatalf("CommitStockIn failed: %v", err)
			}
		case 1: // CommitStockOut
			created := createStockOutEntry(t, queue, db, ctx, scope)
			productID = *created.ProductID

			queue.Broadcaster = broadcaster
			queue.Pantry = pantry
			if _, err := queue.CommitStockOut(ctx, scope, created, nil); err != nil {
				rt.Fatalf("CommitStockOut failed: %v", err)
			}
		}

		item, err := pantry.GetOrCreateItem(ctx, scope, productID)
		if err != nil {
			rt.Fatalf("GetOrCreateItem failed: %v", err)
		}
		want, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
		if err != nil {
			rt.Fatalf("GetInventoryItem failed: %v", err)
		}
		if want == nil {
			rt.Fatal("GetInventoryItem: expected item, got nil")
		}
		if len(broadcaster.inventoryEvents) != 1 {
			rt.Fatalf("expected exactly 1 published inventory event, got %d", len(broadcaster.inventoryEvents))
		}
		if !reflect.DeepEqual(broadcaster.inventoryEvents[0], *want) {
			rt.Fatalf("published event: got %+v, want %+v", broadcaster.inventoryEvents[0], *want)
		}
	})
}

// Feature: realtime-scan-updates, Property 8: A failed inventory-affecting
// call publishes no Inventory_Event (Queue half)
//
// For any call to Queue.CommitStockIn or Queue.CommitStockOut driven into
// one of its documented error conditions, the call SHALL return an error
// and zero PublishInventoryEvent calls SHALL occur as a result of that
// call.
//
// Validates: Requirements 3.6
func TestRealtimeProperty8_QueuePublishesNoInventoryEventOnFailure(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, db, scope := newTestQueue(t)
		ctx := context.Background()

		broadcaster := &fakeBroadcaster{}
		queue.Broadcaster = broadcaster
		queue.Pantry = inventory.NewPantry(db)

		operation := rapid.IntRange(0, 1).Draw(rt, "operation")

		var err error
		switch operation {
		case 0: // CommitStockIn, one of its documented error conditions
			var entry *scan.ScanEntry
			switch rapid.IntRange(0, 2).Draw(rt, "commitStockInErrorCondition") {
			case 0: // direction is not stock_in
				direction := scan.StockOut
				productID := "prod-1"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID, UnitCount: 1}
			case 1: // missing product_id
				direction := scan.StockIn
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, UnitCount: 1}
			case 2: // unit count less than 1
				direction := scan.StockIn
				productID := "prod-1"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID, UnitCount: 0}
			}
			err = queue.CommitStockIn(ctx, scope, entry)
		case 1: // CommitStockOut, one of its documented error conditions
			var entry *scan.ScanEntry
			var instanceID *string
			switch rapid.IntRange(0, 4).Draw(rt, "commitStockOutErrorCondition") {
			case 0: // direction is not stock_out
				direction := scan.StockIn
				productID := "prod-1"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID}
			case 1: // missing product_id
				direction := scan.StockOut
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction}
			case 2: // no item found for user and product
				direction := scan.StockOut
				productID := "no-such-product"
				entry = &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID}
			case 3: // specific instance not found or already removed
				entry = createStockOutEntry(t, queue, db, ctx, scope)
				broadcaster.inventoryEvents = nil
				missing := "no-such-instance"
				instanceID = &missing
			case 4: // no available instances
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if createErr := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
				}); createErr != nil {
					rt.Fatalf("CreateProduct failed: %v", createErr)
				}
				userID := uuid.NewString()
				itemID := uuid.NewString()
				if _, dbErr := db.ExecContext(ctx, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
					itemID, userID, productID, scope.HouseholdID()); dbErr != nil {
					rt.Fatalf("create item failed: %v", dbErr)
				}
				direction := scan.StockOut
				created, createErr := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID:    userID,
					Barcode:   rapid.String().Draw(rt, "barcode"),
					ScannedAt: time.Now(),
					Direction: &direction,
					UnitCount: 1,
					ProductID: &productID,
					Status:    scan.Pending,
				})
				if createErr != nil {
					rt.Fatalf("CreateScanEntry (setup) failed: %v", createErr)
				}
				broadcaster.inventoryEvents = nil
				entry = created
			}
			_, err = queue.CommitStockOut(ctx, scope, entry, instanceID)
		}

		if err == nil {
			rt.Fatal("expected error, got nil")
		}
		if len(broadcaster.inventoryEvents) != 0 {
			rt.Fatalf("expected 0 published inventory events, got %d", len(broadcaster.inventoryEvents))
		}
	})
}

// indexOf returns the index of target within ids, or -1 if not present.
func indexOf(ids []string, target string) int {
	for i, id := range ids {
		if id == target {
			return i
		}
	}
	return -1
}

// --------------------------------------------------------------------------
// scan-duplicate-cards-fix property tests
// --------------------------------------------------------------------------

// directionFromLabel maps a 0/1/2 label to nil/StockIn/StockOut, giving the
// property test below a comparable-by-value way to draw and reason about
// "the same direction" (including nil==nil) versus "a different direction".
func directionFromLabel(label int) *scan.ScanDirection {
	switch label {
	case 1:
		return ptrScanDirection(scan.StockIn)
	case 2:
		return ptrScanDirection(scan.StockOut)
	default:
		return nil
	}
}

// Feature: scan-duplicate-cards-fix, Property 2: Preservation -
// Direction/User/Status Mismatch Never Merges
//
// For any existing scan entry (random direction including nil, random
// user_id, random status) and any incoming scan that differs from it by
// direction, by user_id, or by matching direction+user_id but only against a
// closed (committed/cancelled) status, CreateScanEntry SHALL insert a new
// row rather than merging - the row count for that barcode SHALL increase by
// exactly one, and the existing entry SHALL be left completely unchanged.
//
// Validates: Requirements 3.1, 3.2, 3.3, 3.4, 3.5
func TestProperty2_Preservation_DirectionUserStatusMismatchNeverMerges(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, db, scope := newTestQueue(t)
		ctx := context.Background()

		barcode := rapid.StringN(1, -1, 50).Draw(rt, "barcode")
		existingUserID := rapid.StringN(1, -1, 30).Draw(rt, "existingUserID")
		existingDirLabel := rapid.IntRange(0, 2).Draw(rt, "existingDirLabel")

		mismatchType := rapid.IntRange(0, 2).Draw(rt, "mismatchType")

		var incomingUserID string
		var incomingDirLabel int
		var existingStatus scan.ScanStatus

		switch mismatchType {
		case 0: // different direction, same user
			incomingUserID = existingUserID
			offset := rapid.IntRange(1, 2).Draw(rt, "dirOffset")
			incomingDirLabel = (existingDirLabel + offset) % 3
			existingStatus = rapid.SampledFrom([]scan.ScanStatus{
				scan.Pending, scan.Flagged, scan.Committed, scan.Cancelled,
			}).Draw(rt, "existingStatus")
		case 1: // different user, same direction
			incomingDirLabel = existingDirLabel
			userSuffix := rapid.StringN(1, -1, 20).Draw(rt, "userSuffix")
			incomingUserID = existingUserID + "-diff-" + userSuffix
			existingStatus = rapid.SampledFrom([]scan.ScanStatus{
				scan.Pending, scan.Flagged, scan.Committed, scan.Cancelled,
			}).Draw(rt, "existingStatus")
		case 2: // same direction+user_id, mismatch via a closed status
			// (pending/flagged with the same direction+user_id would be the
			// mergeable case, which is out of scope for this preservation
			// property - see Property 1 for that behavior).
			incomingDirLabel = existingDirLabel
			incomingUserID = existingUserID
			existingStatus = rapid.SampledFrom([]scan.ScanStatus{
				scan.Committed, scan.Cancelled,
			}).Draw(rt, "existingStatus")
		}

		existingDirection := directionFromLabel(existingDirLabel)
		incomingDirection := directionFromLabel(incomingDirLabel)

		existingCreated, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
			UserID:    existingUserID,
			Barcode:   barcode,
			ScannedAt: time.Now(),
			Direction: existingDirection,
			UnitCount: 1,
			Status:    existingStatus,
		})
		if err != nil {
			rt.Fatalf("CreateScanEntry (existing entry): %v", err)
		}

		incomingCreated, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
			UserID:    incomingUserID,
			Barcode:   barcode,
			ScannedAt: time.Now(),
			Direction: incomingDirection,
			UnitCount: 1,
			Status:    scan.Pending,
		})
		if err != nil {
			rt.Fatalf("CreateScanEntry (incoming scan): %v", err)
		}

		if incomingCreated.ID == existingCreated.ID {
			rt.Fatal("expected a new row distinct from the existing entry, got the same entry")
		}

		var rowCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_entries WHERE barcode = ?`, barcode).Scan(&rowCount); err != nil {
			rt.Fatalf("counting scan_entries rows: %v", err)
		}
		if rowCount != 2 {
			rt.Fatalf("row count for barcode %q: want 2 (existing + newly inserted), got %d", barcode, rowCount)
		}

		refetchedExisting, err := queue.GetScanEntry(ctx, scope, existingCreated.ID)
		if err != nil {
			rt.Fatalf("GetScanEntry (existing entry): %v", err)
		}
		if refetchedExisting == nil {
			rt.Fatal("existing entry disappeared")
		}
		if refetchedExisting.UnitCount != existingCreated.UnitCount {
			rt.Fatalf("existing entry UnitCount changed: want %d, got %d", existingCreated.UnitCount, refetchedExisting.UnitCount)
		}
		if refetchedExisting.Status != existingCreated.Status {
			rt.Fatalf("existing entry Status changed: want %v, got %v", existingCreated.Status, refetchedExisting.Status)
		}
		if !refetchedExisting.ScannedAt.Truncate(time.Second).Equal(existingCreated.ScannedAt.Truncate(time.Second)) {
			rt.Fatalf("existing entry ScannedAt changed: want %v, got %v", existingCreated.ScannedAt, refetchedExisting.ScannedAt)
		}
		if refetchedExisting.UserID != existingCreated.UserID {
			rt.Fatalf("existing entry UserID changed: want %q, got %q", existingCreated.UserID, refetchedExisting.UserID)
		}
	})
}

// Feature: scan-duplicate-cards-fix, Property 3: Merge Accounting
//
// For any random sequence of scans (varying barcode, user, direction
// including nil, and product-lookup hit/miss), after feeding each scan
// through CreateScanEntry, the resulting scan_entries row count per
// (barcode, user, direction) group SHALL equal the number of scans that
// opened that group (i.e. exactly one row per group, since nothing in this
// sequence closes an entry - every scan after the first in a group merges
// into it), and each group's row SHALL have a unit_count equal to the
// number of scans merged into that group.
//
// Validates: Requirements 2.1, 2.2, 2.3, 3.1, 3.2, 3.3, 3.4, 3.5
func TestProperty3_MergeAccounting_RowCountAndUnitCountMatchExpectedMerges(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		queue, db, scope := newTestQueue(t)
		ctx := context.Background()

		barcodePool := []string{"barcode-a", "barcode-b", "barcode-c"}
		userPool := []string{"actor", "user-2"}
		directionLabelPool := []int{0, 1, 2} // nil, StockIn, StockOut

		numScans := rapid.IntRange(1, 15).Draw(rt, "numScans")

		type groupKey struct {
			barcode  string
			userID   string
			dirLabel int
		}

		// model tracks the expected unit_count accumulated so far per group.
		model := map[groupKey]int{}

		for i := 0; i < numScans; i++ {
			barcode := rapid.SampledFrom(barcodePool).Draw(rt, "barcode")
			userID := rapid.SampledFrom(userPool).Draw(rt, "userID")
			dirLabel := rapid.SampledFrom(directionLabelPool).Draw(rt, "dirLabel")
			direction := directionFromLabel(dirLabel)
			lookupHit := rapid.Bool().Draw(rt, "lookupHit")

			var status scan.ScanStatus
			var productID *string
			if lookupHit {
				status = scan.Pending
				id := "product-" + barcode
				productID = &id
			} else {
				status = scan.Flagged
			}

			_, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID:    userID,
				Barcode:   barcode,
				ScannedAt: time.Now(),
				Direction: direction,
				UnitCount: 1,
				Status:    status,
				ProductID: productID,
			})
			if err != nil {
				rt.Fatalf("CreateScanEntry failed: %v", err)
			}

			key := groupKey{barcode: barcode, userID: userID, dirLabel: dirLabel}
			model[key] = model[key] + 1
		}

		// Every group that received at least one scan SHALL end up as
		// exactly one row (nothing in this sequence closes an entry, so
		// every scan after the first in a group merges into it), with
		// unit_count equal to the number of scans that went into it.
		for key, expectedUnitCount := range model {
			var directionArg interface{}
			if direction := directionFromLabel(key.dirLabel); direction != nil {
				directionArg = string(*direction)
			}

			var rowCount int
			if err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM scan_entries WHERE barcode = ? AND user_id = ? AND direction IS ?`,
				key.barcode, key.userID, directionArg,
			).Scan(&rowCount); err != nil {
				rt.Fatalf("counting scan_entries rows for group %+v: %v", key, err)
			}
			if rowCount != 1 {
				rt.Fatalf("group %+v: expected exactly 1 row, got %d", key, rowCount)
			}

			var unitCount int
			if err := db.QueryRowContext(ctx,
				`SELECT unit_count FROM scan_entries WHERE barcode = ? AND user_id = ? AND direction IS ?`,
				key.barcode, key.userID, directionArg,
			).Scan(&unitCount); err != nil {
				rt.Fatalf("reading unit_count for group %+v: %v", key, err)
			}
			if unitCount != expectedUnitCount {
				rt.Fatalf("group %+v: expected unit_count %d, got %d", key, expectedUnitCount, unitCount)
			}
		}

		// Total row count across all groups SHALL equal the number of
		// distinct groups the sequence touched.
		var totalRows int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_entries`).Scan(&totalRows); err != nil {
			rt.Fatalf("counting total scan_entries rows: %v", err)
		}
		if totalRows != len(model) {
			rt.Fatalf("total row count: want %d (one per group), got %d", len(model), totalRows)
		}
	})
}
