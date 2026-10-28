package auth

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	defaultInactivityRevertS = 300
)

// DeviceHubConfig is the scanner-facing configuration blob.
type DeviceHubConfig struct {
	Label                   string `json:"label"`
	ControlBarcodes         struct {
		StockIn  string `json:"stockIn"`
		StockOut string `json:"stockOut"`
	} `json:"controlBarcodes"`
	IdleSeconds             int    `json:"idleSeconds"`
	DefaultDirection        string `json:"defaultDirection"`
	InactivityRevertSeconds int    `json:"inactivityRevertSeconds"`
	CommitMode              string `json:"commitMode"`
	ConfigVersion           int    `json:"configVersion"`
}

type deviceSettings struct {
	Label                   string
	DefaultDirection        string
	InactivityRevertSeconds int
	CommitMode              string
	ConfigVersion           int
	BatteryPct              sql.NullInt64
	Charging                sql.NullInt64
}

// DeviceSettingsSnapshot is the scanner configuration used during ingest.
type DeviceSettingsSnapshot struct {
	CommitMode string
}

// UpdateDevicePatch is a partial update from PATCH /api/devices/{id}.
type UpdateDevicePatch struct {
	Label                   *string
	DefaultDirection        *string
	InactivityRevertSeconds *int
	CommitMode              *string
}

// DeviceProductMatch is a resolved barcode for device ingest.
type DeviceProductMatch struct {
	Found     bool
	ProductID string
	Name      string
}

// DeviceProductResolver looks up product barcodes for hardware scans.
type DeviceProductResolver interface {
	Lookup(ctx context.Context, scope tenant.Scope, barcode string) (DeviceProductMatch, error)
}

func (s *Service) loadDeviceSettings(ctx context.Context, q Queryer, householdID, deviceID string) (deviceSettings, error) {
	var out deviceSettings
	var battery, charging sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT label, default_direction, inactivity_revert_s, commit_mode, config_version,
		       battery_pct, charging
		FROM devices WHERE id = ? AND household_id = ?`, deviceID, householdID).Scan(
		&out.Label, &out.DefaultDirection, &out.InactivityRevertSeconds, &out.CommitMode, &out.ConfigVersion,
		&battery, &charging,
	)
	if err != nil {
		return deviceSettings{}, notFound()
	}
	out.BatteryPct = battery
	out.Charging = charging
	return out, nil
}

func populateHubConfig(cfg *DeviceHubConfig, settings deviceSettings, stockIn, stockOut string) {
	cfg.Label = settings.Label
	cfg.ControlBarcodes.StockIn = stockIn
	cfg.ControlBarcodes.StockOut = stockOut
	if cfg.ControlBarcodes.StockIn == "" {
		cfg.ControlBarcodes.StockIn = "STOCK-IN"
	}
	if cfg.ControlBarcodes.StockOut == "" {
		cfg.ControlBarcodes.StockOut = "STOCK-OUT"
	}
	cfg.IdleSeconds = settings.InactivityRevertSeconds
	cfg.DefaultDirection = settings.DefaultDirection
	cfg.InactivityRevertSeconds = settings.InactivityRevertSeconds
	cfg.CommitMode = settings.CommitMode
	cfg.ConfigVersion = settings.ConfigVersion
}

// HubConfigForDevice loads config for heartbeat and scan responses.
func (s *Service) HubConfigForDevice(ctx context.Context, householdID, deviceID, stockIn, stockOut string) (DeviceHubConfig, error) {
	settings, err := s.loadDeviceSettings(ctx, s.db, householdID, deviceID)
	if err != nil {
		return DeviceHubConfig{}, err
	}
	var cfg DeviceHubConfig
	populateHubConfig(&cfg, settings, stockIn, stockOut)
	return cfg, nil
}

func (s *Service) UpdateDevice(ctx context.Context, userID, householdID, deviceID string, patch UpdateDevicePatch) (DeviceView, error) {
	if err := s.mutableDevice(ctx, userID, householdID, deviceID, false); err != nil {
		return DeviceView{}, err
	}
	if patch.Label == nil && patch.DefaultDirection == nil && patch.InactivityRevertSeconds == nil && patch.CommitMode == nil {
		return DeviceView{}, fail(CodeInvalidInput, SentenceValidation)
	}
	settings, err := s.loadDeviceSettings(ctx, s.db, householdID, deviceID)
	if err != nil {
		return DeviceView{}, err
	}
	label := settings.Label
	if patch.Label != nil {
		var nerr error
		label, nerr = normalizeName(*patch.Label, "label")
		if nerr != nil {
			return DeviceView{}, nerr
		}
	}
	dir := settings.DefaultDirection
	if patch.DefaultDirection != nil {
		d := strings.TrimSpace(*patch.DefaultDirection)
		if d != "stock_in" && d != "stock_out" {
			return DeviceView{}, fail(CodeInvalidInput, SentenceValidation)
		}
		dir = d
	}
	revert := settings.InactivityRevertSeconds
	if patch.InactivityRevertSeconds != nil {
		if *patch.InactivityRevertSeconds < 30 || *patch.InactivityRevertSeconds > 3600 {
			return DeviceView{}, fail(CodeInvalidInput, SentenceValidation)
		}
		revert = *patch.InactivityRevertSeconds
	}
	mode := settings.CommitMode
	if patch.CommitMode != nil {
		m := strings.TrimSpace(*patch.CommitMode)
		if m != "queue" && m != "immediate" {
			return DeviceView{}, fail(CodeInvalidInput, SentenceValidation)
		}
		mode = m
	}
	now := stamp(s.clock())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeviceView{}, AsError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE devices SET label = ?, default_direction = ?, inactivity_revert_s = ?,
			commit_mode = ?, config_version = config_version + 1
		WHERE id = ? AND household_id = ?`,
		label, dir, revert, mode, deviceID, householdID); err != nil {
		return DeviceView{}, AsError(err)
	}
	if err := writeAudit(ctx, tx, householdID, "account", userID, "device.updated", "device", deviceID, now); err != nil {
		return DeviceView{}, AsError(err)
	}
	if err := changes.Bump(ctx, tx, householdID, changes.ScopeDevices); err != nil {
		return DeviceView{}, AsError(err)
	}
	if err := tx.Commit(); err != nil {
		return DeviceView{}, AsError(err)
	}
	if s.hints != nil {
		s.hints.PublishHint(householdID, "devices")
	}
	return s.oneDevice(ctx, userID, householdID, deviceID)
}

func boolPtr(v bool) *bool {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func productOnHand(ctx context.Context, db queryer, householdID, productID string) (int, error) {
	var onHand int
	err := db.QueryRowContext(ctx, `
		SELECT CASE
			WHEN i.id IS NULL OR i.shelved != 0 THEN 0
			ELSE COALESCE((
				SELECT COUNT(*) FROM item_instances ii
				WHERE ii.household_id = ? AND ii.item_id = i.id AND ii.removed_at IS NULL
			), 0)
		END
		FROM items i
		WHERE i.household_id = ? AND i.product_id = ?`,
		householdID, householdID, productID).Scan(&onHand)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return onHand, nil
}

func displayProductName(match DeviceProductMatch) *string {
	if !match.Found {
		return nil
	}
	name := match.Name
	return &name
}

func tenantScope(householdID string) (tenant.Scope, error) {
	return tenant.New(householdID)
}

// DeviceScanProcessor continues ingest after idempotency checks when the scan queue is wired.
type DeviceScanProcessor func(ctx context.Context, q Queryer, s *Service, p Principal, item PreparedScanItem, group int) (ScanResult, CreatedScan, error)

// SetDeviceScanProcessor registers queue-aware device scan handling from the HTTP server.
func (s *Service) SetDeviceScanProcessor(fn DeviceScanProcessor) {
	if s == nil {
		return
	}
	s.deviceScanProcessor = fn
}

// SetDeviceProductLookup resolves barcodes for cached replay product names.
func (s *Service) SetDeviceProductLookup(lookup DeviceProductResolver) {
	if s == nil {
		return
	}
	s.productLookup = lookup
}
