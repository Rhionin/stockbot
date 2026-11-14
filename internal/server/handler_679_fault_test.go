package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/trip"
)

// TestW3T679ZeroAdjustmentAdvanceFaultRollback — AC8 #12: SetFaultAfterAdvanceSeq on ledger reset rolls back lapsed-zero work.
func TestW3T679ZeroAdjustmentAdvanceFaultRollback(t *testing.T) {
	testW3T679ZeroAdjustmentAdvanceFaultRollback(t)
}

func testW3T679ZeroAdjustmentAdvanceFaultRollback(t *testing.T) {
	t.Helper()
	handler, env := setupTestWithDB(t)
	registerOpenAndKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-679-zfault"
	const lineID = "line-679-zfault"
	const lineKey = "item:" + itemID
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-679-zfault", "ZFault", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, hid, itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusCreated)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)

	var tripBefore int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripBefore); err != nil {
		t.Fatal(err)
	}
	var changesBefore int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	var adjBefore int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE household_id = ? AND entry_id = ?`,
		hid, lineID).Scan(&adjBefore); err != nil {
		t.Fatal(err)
	}
	if adjBefore != 1 {
		t.Fatalf("adjustment rows before fault %d want 1", adjBefore)
	}
	adjSnap, err := snapshotZeroAdjustmentRow(t, env.DB, hid, lineID)
	if err != nil {
		t.Fatal(err)
	}

	faultEntered := make(chan struct{}, 1)
	releaseFault := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFault) }) }
	restore := trip.SetFaultAfterAdvanceSeqForTest(func() error {
		select {
		case faultEntered <- struct{}{}:
		default:
		}
		<-releaseFault
		return errors.New("injected fault after advance seq (679)")
	})
	t.Cleanup(func() {
		restore()
		release()
	})

	observerDone := make(chan error, 1)
	go func() {
		var obsErr error
		defer func() { observerDone <- obsErr }()
		<-faultEntered
		listDone := make(chan struct{})
		sumDone := make(chan struct{})
		var listErr error
		go func() {
			defer close(listDone)
			req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1&provider=kroger", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				listErr = fmt.Errorf("observer list status %d", rec.Code)
				return
			}
			var doc struct {
				Items   []map[string]any `json:"items"`
				OffList []map[string]any `json:"offList"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				listErr = err
				return
			}
			for _, row := range doc.Items {
				if row["lineKey"] == lineKey {
					listErr = fmt.Errorf("observer saw zero line still in items")
					return
				}
			}
			var sawZero bool
			for _, row := range doc.OffList {
				if row["id"] == lineKey && row["offListReason"] == "zero_for_trip" {
					sawZero = true
					break
				}
			}
			if !sawZero {
				listErr = fmt.Errorf("observer offList missing zero_for_trip for %s", lineKey)
			}
		}()
		go func() {
			defer close(sumDone)
			req := httptest.NewRequest(http.MethodGet, "/api/shopping-list/summary", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
		time.Sleep(50 * time.Millisecond)
		release()
		<-listDone
		<-sumDone
		obsErr = listErr
	}()

	exchangeJSON(t, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusInternalServerError)
	if err := <-observerDone; err != nil {
		t.Fatal(err)
	}

	var tripAfter int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripAfter); err != nil {
		t.Fatal(err)
	}
	if tripAfter != tripBefore {
		t.Fatalf("trip_seq %d -> %d after rollback", tripBefore, tripAfter)
	}
	var changesAfter int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if changesAfter != changesBefore {
		t.Fatalf("list_line_changes %d -> %d", changesBefore, changesAfter)
	}
	var adjAfter int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE household_id = ? AND entry_id = ?`,
		hid, lineID).Scan(&adjAfter); err != nil {
		t.Fatal(err)
	}
	if adjAfter != adjBefore {
		t.Fatalf("lapsed zero adjustment deleted on failed advance: %d -> %d", adjBefore, adjAfter)
	}
	adjAfterSnap, err := snapshotZeroAdjustmentRow(t, env.DB, hid, lineID)
	if err != nil {
		t.Fatal(err)
	}
	if adjAfterSnap != adjSnap {
		t.Fatalf("adjustment row changed on rollback: before %q after %q", adjSnap, adjAfterSnap)
	}
	var nextTripAfter int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND trip_seq = ? AND line_key = ? AND cause = 'new_trip'`,
		hid, tripBefore+1, lineKey).Scan(&nextTripAfter); err != nil {
		t.Fatal(err)
	}
	if nextTripAfter != 0 {
		t.Fatalf("wrote new_trip boundary for zero line on rolled-back advance")
	}
}

type zeroAdjSnapshot string

func snapshotZeroAdjustmentRow(t *testing.T, db *sql.DB, householdID, entryID string) (zeroAdjSnapshot, error) {
	t.Helper()
	var qty int
	var provider string
	var expires sql.NullInt64
	var actor sql.NullString
	var setAt sql.NullString
	err := db.QueryRow(`
		SELECT quantity, provider_id, expires_trip, actor_user_id, set_at
		FROM shopping_list_entry_adjustments WHERE household_id = ? AND entry_id = ?`,
		householdID, entryID).Scan(&qty, &provider, &expires, &actor, &setAt)
	if err != nil {
		return "", err
	}
	return zeroAdjSnapshot(fmt.Sprintf("q=%d prov=%s exp=%v actor=%v set=%v", qty, provider, expires, actor, setAt)), nil
}