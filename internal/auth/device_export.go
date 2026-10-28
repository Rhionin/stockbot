package auth

import (
	"context"
	"database/sql"
	"time"
)

// Queryer is the database or an open transaction.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PreparedScanItem is one normalized device scan passed to devicingest.
type PreparedScanItem struct {
	inner preparedScan
}

func preparedItem(item preparedScan) PreparedScanItem {
	return PreparedScanItem{inner: item}
}

func (p PreparedScanItem) ClientID() string  { return p.inner.item.ID }
func (p PreparedScanItem) Barcode() string   { return p.inner.barcode }
func (p PreparedScanItem) Direction() string { return p.inner.item.Direction }
func (p PreparedScanItem) Unit() int         { return p.inner.unit }
func (p PreparedScanItem) At() time.Time     { return p.inner.at }
func (p PreparedScanItem) Approx() bool      { return p.inner.approx }
func (p PreparedScanItem) BootID() string    { return p.inner.item.BootID }
func (p PreparedScanItem) Match() DeviceProductMatch { return p.inner.match }

// CreatedScan records one new scan row from device ingest.
type CreatedScan struct {
	ScanID  string
	Barcode string
	Stock   bool
}

// DeviceLCD is the scanner display payload stored for idempotent replay.
type DeviceLCD struct {
	ScanStatus  string
	Known       bool
	ProductName *string
	Barcode     string
	Direction   string
	QueuedCount int
	OnHand      int
	Delta       int
	Backfilled  bool
}

// ApplyDeviceLCD copies LCD fields onto a scan result.
func ApplyDeviceLCD(lcd DeviceLCD, result *ScanResult) {
	lcd.apply(result)
}

// DisplayProductName returns the resolved name when a barcode is known.
func DisplayProductName(match DeviceProductMatch) *string {
	return displayProductName(match)
}

// DeviceSettings loads scanner configuration for ingest.
func (s *Service) DeviceSettings(ctx context.Context, q Queryer, householdID, deviceID string) (DeviceSettingsSnapshot, error) {
	settings, err := s.loadDeviceSettings(ctx, q, householdID, deviceID)
	if err != nil {
		return DeviceSettingsSnapshot{}, err
	}
	return DeviceSettingsSnapshot{CommitMode: settings.CommitMode}, nil
}

// ProductOnHand counts active shelf units for a product in a household.
func (s *Service) ProductOnHand(ctx context.Context, q Queryer, householdID, productID string) (int, error) {
	return productOnHand(ctx, q, householdID, productID)
}

// RecordDeviceScanReceipt stores idempotent replay data and updates LCD columns on the scan row.
func (s *Service) RecordDeviceScanReceipt(ctx context.Context, q Queryer, p Principal, item PreparedScanItem, scanEntryID string, lcd DeviceLCD) error {
	return s.recordDeviceScanReceipt(ctx, q, p, item.inner, scanEntryID, lcd)
}

// Validation returns a validation error for device ingest.
func Validation() error { return validation() }
