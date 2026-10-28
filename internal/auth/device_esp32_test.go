package auth

import (
	"context"
	"testing"
	"time"
)

func pairESP32(t *testing.T, ctx context.Context, svc *Service, userID, house string) (Principal, string) {
	t.Helper()
	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Pantry pistol", "esp32_scanner", "1.0.0", "boot-esp")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateDevice(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	return principal, paired.DeviceID
}

func TestESP32PairingReturnsScannerConfig(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Pantry pistol", "esp32_scanner", "1.0.0", "boot-esp")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.HubConfigForDevice(ctx, house, paired.DeviceID, "STOCK-IN", "STOCK-OUT")
	if err != nil || cfg.DefaultDirection != "stock_out" || cfg.CommitMode != "queue" ||
		cfg.InactivityRevertSeconds != 300 || cfg.ControlBarcodes.StockIn == "" {
		t.Fatalf("config %#v %v", cfg, err)
	}
	var kind string
	if err := svc.db.QueryRow(`SELECT kind FROM devices WHERE id = ?`, paired.DeviceID).Scan(&kind); err != nil || kind != "esp32_scanner" {
		t.Fatalf("kind %q %v", kind, err)
	}
}

func TestUpdateDeviceScannerSettings(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	_, deviceID := pairESP32(t, ctx, svc, userID, house)
	label := "Counter"
	patch := UpdateDevicePatch{
		Label:                   &label,
		DefaultDirection:        strPtr("stock_in"),
		InactivityRevertSeconds: intPtr(600),
		CommitMode:              strPtr("immediate"),
	}
	view, err := svc.UpdateDevice(ctx, userID, house, deviceID, patch)
	if err != nil || view.DefaultDirection != "stock_in" || view.CommitMode != "immediate" || view.InactivityRevertSeconds != 600 {
		t.Fatalf("view %#v %v", view, err)
	}
	var version int
	if err := svc.db.QueryRow(`SELECT config_version FROM devices WHERE id = ?`, deviceID).Scan(&version); err != nil || version < 1 {
		t.Fatalf("config_version %d %v", version, err)
	}
	bad := UpdateDevicePatch{InactivityRevertSeconds: intPtr(10)}
	if _, err := svc.UpdateDevice(ctx, userID, house, deviceID, bad); err == nil {
		t.Fatal("expected invalid inactivity")
	}
	other := newID()
	if _, err := svc.db.Exec(`INSERT INTO households (id, name, created_at, channel_key, is_primary) VALUES (?, 'Other', ?, 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 0)`, other, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateDevice(ctx, userID, other, deviceID, patch); err == nil {
		t.Fatal("cross-household update succeeded")
	}
}

func TestHeartbeatPersistsBattery(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	principal, deviceID := pairESP32(t, ctx, svc, userID, house)
	pct := 42
	charging := true
	_, err := svc.Heartbeat(ctx, principal, Heartbeat{
		Version: "1.0.0", BootID: "boot-esp", UptimeS: 1, Queued: 0, ClockSynced: true,
		BatteryPct: &pct, Charging: &charging,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListDevices(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range list {
		if d.ID == deviceID {
			found = d.BatteryPct != nil && *d.BatteryPct == 42 && d.Charging != nil && *d.Charging
		}
	}
	if !found {
		t.Fatalf("devices %#v", list)
	}
	bad := 101
	if _, err := svc.Heartbeat(ctx, principal, Heartbeat{Version: "1.0", BootID: "b", BatteryPct: &bad}); err == nil {
		t.Fatal("bad battery accepted")
	}
}