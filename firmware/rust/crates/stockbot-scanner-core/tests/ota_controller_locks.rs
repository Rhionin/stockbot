//! Negative-path and boundary locks for `OtaController` (Architect § review).

#![allow(clippy::field_reassign_with_default)]

#[path = "common/mod.rs"]
mod common;

use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use stockbot_scanner_core::{
    ota_nvs_keys, NvsStore, CHECKIN_DEADLINE_MS, INSTALL_WINDOW_IDLE_MS, MIN_BATTERY_PCT_STICKS3,
    MIN_WIFI_RSSI_DBM,
};

use common::ota_fakes::{open_window_ctx, test_firmware, OtaHarness};
use common::vector_fixtures::golden_valid;

fn sign_fields(fields: &stockbot_scanner_core::Fields) -> String {
    let canonical = stockbot_scanner_core::canonical_bytes(fields).unwrap();
    let sk = SigningKey::from_bytes(&[0x3b; 32]);
    base64::engine::general_purpose::STANDARD.encode(sk.sign(&canonical).to_bytes())
}

fn table() -> stockbot_scanner_core::TrustedKeyTable {
    let v = golden_valid();
    let pk = stockbot_scanner_core::decode_public_key_b64(&v.public_key_b64).unwrap();
    let mut t = stockbot_scanner_core::TrustedKeyTable::new();
    t.insert(v.fields.key_id.clone(), pk);
    t
}

#[test]
fn equal_running_version_offer_stays_idle_no_download() {
    let target = golden_valid().fields.target.clone();
    let (fw, _) = test_firmware(64, 0x0A);
    let mut h = OtaHarness::new(&target, fw, table());
    h.ctx.running_version = golden_valid().fields.version.clone();
    let mut fields = golden_valid().fields();
    fields.version = h.ctx.running_version.clone();
    let offer = OtaHarness::offer_from_fields(
        "fwr_same_ver",
        fields,
        &golden_valid().sig_b64,
        "/api/device/firmware/fwr_same_ver",
    );
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    c.now_ms.set(INSTALL_WINDOW_IDLE_MS + 1);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert_eq!(c.http_ctl.open_calls.get(), 0);
}

#[test]
fn suppressed_release_replaced_by_different_id_still_blocked_when_still_suppressed() {
    let target = golden_valid().fields.target.clone();
    let (fw, _) = test_firmware(64, 0x0B);
    let mut h = OtaHarness::new(&target, fw, table());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_supp");
    let mut fields = golden_valid().fields();
    fields.version = "9.9.9".to_owned();
    fields.size = 64;
    fields.sha256 = common::ota_fakes::sha256_hex(&h.http.firmware);
    let sig = sign_fields(&fields);
    let offer =
        OtaHarness::offer_from_fields("fwr_supp", fields, &sig, "/api/device/firmware/fwr_supp");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert!(!ctl.phase_is_downloading());
    assert_eq!(c.http_ctl.open_calls.get(), 0);
    assert_eq!(c.slots_ctl.written.get(), 0);
}

#[test]
fn suppression_cleared_on_200_heartbeat_without_update() {
    let target = golden_valid().fields.target.clone();
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_old");
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_http(true, 200, None);
    assert!(h.nvs.stockbot_get_str(ota_nvs_keys::OTA_SUPP).is_none());
}

#[test]
fn image_end_validation_failure_reports_image_invalid() {
    let target = golden_valid().fields.target.clone();
    let (fw, hex) = test_firmware(128, 0xCC);
    let mut fields = golden_valid().fields();
    fields.version = "2.0.4".to_owned();
    fields.size = 128;
    fields.sha256 = hex;
    let sig = sign_fields(&fields);
    let offer =
        OtaHarness::offer_from_fields("fwr_img", fields, &sig, "/api/device/firmware/fwr_img");
    let mut h = OtaHarness::new(&target, fw, table());
    h.slots_ctl.end_write_ok.set(false);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some("image_invalid"));
}

#[test]
fn sticks3_on_dock_allows_download_despite_low_battery() {
    let (fw, hex) = test_firmware(64, 0x0D);
    let mut h = OtaHarness::new("sticks3_u173", fw, table());
    h.power.vbus.set(true);
    h.power.battery_pct.set(Some(20));
    let mut fields = golden_valid().fields();
    fields.target = "sticks3_u173".to_owned();
    fields.version = "2.0.0".to_owned();
    fields.size = 64;
    fields.sha256 = hex;
    let sig = sign_fields(&fields);
    let offer =
        OtaHarness::offer_from_fields("fwr_dock", fields, &sig, "/api/device/firmware/fwr_dock");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller("sticks3_u173", true);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(INSTALL_WINDOW_IDLE_MS + 1);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
}

#[test]
fn install_window_idle_ms_boundary() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = {
        let (fw, hex) = test_firmware(64, 0x0E);
        let mut fields = golden_valid().fields();
        fields.version = "2.0.1".to_owned();
        fields.size = 64;
        fields.sha256 = hex;
        let sig = sign_fields(&fields);
        let offer = OtaHarness::offer_from_fields(
            "fwr_idle",
            fields,
            &sig,
            "/api/device/firmware/fwr_idle",
        );
        (fw, offer, ())
    };
    let mut h = OtaHarness::new(&target, fw, table());
    let c = h.controls();
    let base_ctx = open_window_ctx(&h.ctx);
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer.clone()), &base_ctx);
    ctl.tick(&base_ctx);

    c.now_ms.set(200_000);
    let mut blocked = base_ctx.clone();
    blocked.last_activity_ms = 200_000 - (INSTALL_WINDOW_IDLE_MS - 1);
    ctl.tick(&blocked);
    assert!(!ctl.phase_is_downloading());

    let mut allowed = base_ctx;
    allowed.last_activity_ms = 200_000 - INSTALL_WINDOW_IDLE_MS;
    ctl.tick(&allowed);
    assert!(ctl.phase_is_downloading());
}

#[test]
fn checkin_deadline_boundary_ms() {
    let mut h = OtaHarness::new("atoms3lite_u173_bench", Vec::new(), table());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller("atoms3lite_u173_bench", false);
    ctl.enter_pending_verify_after_boot("fwr_chk");
    c.now_ms.set(CHECKIN_DEADLINE_MS - 1);
    ctl.tick(&ctx);
    assert!(!c.slots_ctl.marked_invalid.get());

    c.now_ms.set(CHECKIN_DEADLINE_MS);
    ctl.tick(&ctx);
    assert!(c.slots_ctl.marked_invalid.get());
}

#[test]
fn battery_threshold_boundary_sticks3() {
    let (fw, hex) = test_firmware(64, 0x0F);
    let mut h = OtaHarness::new("sticks3_u173", fw, table());
    h.power.vbus.set(false);
    let mut fields = golden_valid().fields();
    fields.target = "sticks3_u173".to_owned();
    fields.version = "2.0.2".to_owned();
    fields.size = 64;
    fields.sha256 = hex;
    let sig = sign_fields(&fields);
    let offer = OtaHarness::offer_from_fields(
        "fwr_batt_b",
        fields,
        &sig,
        "/api/device/firmware/fwr_batt_b",
    );
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let battery = h.power.battery_pct.clone();
    let mut ctl = h.controller("sticks3_u173", true);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(INSTALL_WINDOW_IDLE_MS + 1);

    battery.set(Some(MIN_BATTERY_PCT_STICKS3 - 1));
    ctl.tick(&ctx);
    assert!(!ctl.phase_is_downloading());

    battery.set(Some(MIN_BATTERY_PCT_STICKS3));
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
}

#[test]
fn wifi_rssi_boundary_dbm() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = {
        let (fw, hex) = test_firmware(64, 0x10);
        let mut fields = golden_valid().fields();
        fields.version = "2.0.3".to_owned();
        fields.size = 64;
        fields.sha256 = hex;
        let sig = sign_fields(&fields);
        let offer = OtaHarness::offer_from_fields(
            "fwr_rssi_b",
            fields,
            &sig,
            "/api/device/firmware/fwr_rssi_b",
        );
        (fw, offer, ())
    };
    let mut h = OtaHarness::new(&target, fw, table());
    let base_ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &base_ctx);
    ctl.tick(&base_ctx);
    c.now_ms.set(INSTALL_WINDOW_IDLE_MS + 1);

    let mut weak = base_ctx.clone();
    weak.wifi_rssi_dbm = Some(MIN_WIFI_RSSI_DBM - 1);
    ctl.tick(&weak);
    assert!(!ctl.phase_is_downloading());

    let mut ok = base_ctx;
    ok.wifi_rssi_dbm = Some(MIN_WIFI_RSSI_DBM);
    ctl.tick(&ok);
    assert!(ctl.phase_is_downloading());
}

#[test]
fn rollback_report_skipped_when_ota_rel_empty() {
    let mut h = OtaHarness::new("atoms3lite_u173_bench", Vec::new(), table());
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let mut ctl = h.controller("atoms3lite_u173_bench", false);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
}
