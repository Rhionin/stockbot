package devicingest

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// afterDeviceStockCommitHook is set via export_test.go to force a failure after stock is
// committed but before the device scan receipt is finalized, inside the ingest transaction.
var afterDeviceStockCommitHook func() error

// Attach wires queue-aware device scan handling onto the auth service.
func Attach(svc *auth.Service, queue *scan.Queue, lookup *product.LookupService) {
	if svc == nil || queue == nil || lookup == nil {
		return
	}
	resolver := deviceProductResolver{lookup: lookup}
	svc.SetDeviceProductLookup(resolver)
	svc.SetDeviceIngestEventPublisher(queue)
	svc.SetDeviceScanProcessor(func(ctx context.Context, q auth.Queryer, s *auth.Service, p auth.Principal, item auth.PreparedScanItem, group int) (auth.ScanResult, auth.CreatedScan, error) {
		return process(ctx, q, s, queue, resolver, p, item, group)
	})
}

type deviceProductResolver struct {
	lookup *product.LookupService
}

func (r deviceProductResolver) Lookup(ctx context.Context, scope tenant.Scope, barcode string) (auth.DeviceProductMatch, error) {
	res, err := r.lookup.Lookup(ctx, scope, barcode)
	if err != nil {
		return auth.DeviceProductMatch{}, err
	}
	if !res.IsFound() {
		return auth.DeviceProductMatch{}, nil
	}
	return auth.DeviceProductMatch{
		Found:     true,
		ProductID: res.Product.ID,
		Name:      res.Product.Name,
	}, nil
}

func process(ctx context.Context, q auth.Queryer, svc *auth.Service, queue *scan.Queue, _ deviceProductResolver, p auth.Principal, item auth.PreparedScanItem, group int) (auth.ScanResult, auth.CreatedScan, error) {
	settings, err := svc.DeviceSettings(ctx, q, p.HouseholdID, p.DeviceID)
	if err != nil {
		return auth.ScanResult{}, auth.CreatedScan{}, err
	}
	scope, err := tenant.New(p.HouseholdID)
	if err != nil {
		return auth.ScanResult{}, auth.CreatedScan{}, auth.AsError(err)
	}
	match := item.Match()
	if settings.CommitMode == "immediate" && match.Found {
		lcd, scanID, stockBump, err := commitImmediate(ctx, q, svc, queue, scope, p, item, group, match)
		if err != nil {
			return auth.ScanResult{}, auth.CreatedScan{}, err
		}
		result := auth.ScanResult{ID: item.ClientID(), Status: "created", Cursor: scanID, ScanID: scanID}
		auth.ApplyDeviceLCD(lcd, &result)
		created := auth.CreatedScan{ScanID: scanID, Barcode: item.Barcode()}
		if stockBump {
			created.Stock = true
		}
		return result, created, nil
	}
	lcd, scanID, err := queueScan(ctx, q, svc, queue, scope, p, item, group, match)
	if err != nil {
		return auth.ScanResult{}, auth.CreatedScan{}, err
	}
	result := auth.ScanResult{ID: item.ClientID(), Status: "created", Cursor: scanID, ScanID: scanID}
	auth.ApplyDeviceLCD(lcd, &result)
	return result, auth.CreatedScan{ScanID: scanID, Barcode: item.Barcode()}, nil
}

func queueScan(ctx context.Context, q auth.Queryer, svc *auth.Service, queue *scan.Queue, scope tenant.Scope, p auth.Principal, item auth.PreparedScanItem, group int, match auth.DeviceProductMatch) (auth.DeviceLCD, string, error) {
	dir := scanDirection(item.Direction())
	entry := entryFromMatch(p.DeviceID, item.Barcode(), match, dir, item.At(), item.Unit())
	entry.DeviceID = p.DeviceID
	entry.ClientScanID = item.ClientID()
	sqlTx, err := ingestTx(q)
	if err != nil {
		return auth.DeviceLCD{}, "", err
	}
	merged, err := queue.CreateScanEntryTx(ctx, scope, sqlTx, entry)
	if err != nil {
		return auth.DeviceLCD{}, "", auth.AsError(err)
	}
	onHand := 0
	scanStatus := "flagged"
	known := false
	var productName *string
	if match.Found {
		scanStatus = "queued"
		known = true
		productName = auth.DisplayProductName(match)
		var err error
		onHand, err = svc.ProductOnHand(ctx, q, p.HouseholdID, match.ProductID)
		if err != nil {
			return auth.DeviceLCD{}, "", auth.AsError(err)
		}
	}
	lcd := auth.DeviceLCD{
		ScanStatus: scanStatus, Known: known, ProductName: productName,
		Barcode: item.Barcode(), Direction: item.Direction(),
		QueuedCount: merged.UnitCount, OnHand: onHand,
	}
	if afterDeviceStockCommitHook != nil {
		if err := afterDeviceStockCommitHook(); err != nil {
			return auth.DeviceLCD{}, "", auth.AsError(err)
		}
	}
	if err := svc.RecordDeviceScanReceipt(ctx, q, p, item, merged.ID, lcd); err != nil {
		return auth.DeviceLCD{}, "", err
	}
	return lcd, merged.ID, nil
}

func commitImmediate(ctx context.Context, q auth.Queryer, svc *auth.Service, queue *scan.Queue, scope tenant.Scope, p auth.Principal, item auth.PreparedScanItem, group int, match auth.DeviceProductMatch) (auth.DeviceLCD, string, bool, error) {
	dir := scanDirection(item.Direction())
	entry := entryFromMatch(p.DeviceID, item.Barcode(), match, dir, item.At(), item.Unit())
	entry.DeviceID = p.DeviceID
	entry.ClientScanID = item.ClientID()
	sqlTx, err := ingestTx(q)
	if err != nil {
		return auth.DeviceLCD{}, "", false, err
	}
	created, err := queue.CreateScanEntryTx(ctx, scope, sqlTx, entry)
	if err != nil {
		return auth.DeviceLCD{}, "", false, auth.AsError(err)
	}
	onHandBefore, err := svc.ProductOnHand(ctx, q, p.HouseholdID, match.ProductID)
	if err != nil {
		return auth.DeviceLCD{}, "", false, auth.AsError(err)
	}
	delta := 0
	backfilled := false
	stockMoved := false
	switch item.Direction() {
	case "stock_in":
		if err := queue.CommitStockInTx(ctx, scope, sqlTx, created); err != nil {
			return auth.DeviceLCD{}, "", false, auth.AsError(err)
		}
		delta = item.Unit()
		stockMoved = true
	case "stock_out":
		if onHandBefore > 0 {
			removed, err := queue.CommitStockOutTx(ctx, scope, sqlTx, created, nil)
			if err != nil {
				return auth.DeviceLCD{}, "", false, auth.AsError(err)
			}
			delta = -removed
			stockMoved = removed > 0
		} else {
			if _, err := queue.CommitBackfillTx(ctx, scope, sqlTx, created); err != nil {
				return auth.DeviceLCD{}, "", false, auth.AsError(err)
			}
			delta = -item.Unit()
			backfilled = true
			stockMoved = true
		}
	default:
		return auth.DeviceLCD{}, "", false, auth.Validation()
	}
	if afterDeviceStockCommitHook != nil {
		if err := afterDeviceStockCommitHook(); err != nil {
			return auth.DeviceLCD{}, "", false, auth.AsError(err)
		}
	}
	onHandAfter, err := svc.ProductOnHand(ctx, q, p.HouseholdID, match.ProductID)
	if err != nil {
		return auth.DeviceLCD{}, "", false, auth.AsError(err)
	}
	lcd := auth.DeviceLCD{
		ScanStatus: "committed", Known: true, ProductName: auth.DisplayProductName(match),
		Barcode: item.Barcode(), Direction: item.Direction(),
		QueuedCount: 0, OnHand: onHandAfter, Delta: delta, Backfilled: backfilled,
	}
	if err := svc.RecordDeviceScanReceipt(ctx, q, p, item, created.ID, lcd); err != nil {
		return auth.DeviceLCD{}, "", false, err
	}
	return lcd, created.ID, stockMoved, nil
}

func ingestTx(q auth.Queryer) (*sql.Tx, error) {
	tx, ok := q.(*sql.Tx)
	if !ok {
		return nil, errors.New("device ingest requires a transaction")
	}
	return tx, nil
}

func scanDirection(dir string) *scan.ScanDirection {
	if dir == "" {
		return nil
	}
	d := scan.ScanDirection(dir)
	return &d
}

func entryFromMatch(userID, barcode string, match auth.DeviceProductMatch, dir *scan.ScanDirection, at time.Time, unit int) scan.ScanEntry {
	status := scan.Pending
	var productID *string
	if match.Found {
		productID = &match.ProductID
	} else {
		status = scan.Flagged
	}
	if unit <= 0 {
		unit = 1
	}
	return scan.ScanEntry{
		UserID: userID, Barcode: barcode, ScannedAt: at, Direction: dir,
		UnitCount: unit, Status: status, ProductID: productID,
	}
}
