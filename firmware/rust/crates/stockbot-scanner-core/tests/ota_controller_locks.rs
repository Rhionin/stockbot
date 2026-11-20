//! Negative-path and boundary locks for `OtaController` (Architect § review).

#![allow(clippy::field_reassign_with_default)]

#[path = "common/mod.rs"]
mod common;

use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use stockbot_scanner_core::{
    ota_nvs_keys, DeviceOtaError, NvsStore, OtaTickContext, UpdateOffer, CHECKIN_DEADLINE_MS,
    DOWNLOAD_FAILED_BACKOFF_MS, INSTALL_WINDOW_IDLE_MS, MAX_RANGE_RETRIES,
    MIN_BATTERY_PCT_STICKS3, MIN_WIFI_RSSI_DBM,
};

const BACKOFF_ONE_HOUR_MS: u64 = 3_600_000;

use common::ota_fakes::{open_window_ctx, test_firmware, OtaHarness};
use common::vector_fixtures::golden_valid;

fn sized_offer(
    release_id: &str,
    target: &str,
    size: i64,
    byte: u8,
    version: &str,
) -> (Vec<u8>, UpdateOffer) {
    let v = golden_valid();
    let (fw, hex) = test_firmware(size as usize, byte);
    let mut fields = v.fields();
    fields.target = target.to_owned();
    fields.version = version.to_owned();
    fields.size = size;
    fields.sha256 = hex;
    let sig = sign_fields(&fields);
    let offer = OtaHarness::offer_from_fields(
        release_id,
        fields,
        &sig,
        &format!("/api/device/firmware/{release_id}"),
    );
    (fw, offer)
}

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
    let (fw_a, offer_a) = sized_offer("fwr_a", &target, 64, 0x0A, "9.9.0");
    let (_fw_b, offer_b) = sized_offer("fwr_supp", &target, 64, 0x0B, "9.9.9");
    let mut h = OtaHarness::new(&target, fw_a, table());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_supp");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer_a), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    let opens_before = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    ctl.tick(&ctx);
    assert_ne!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_supp")
    );
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert_eq!(c.http_ctl.open_calls.get(), opens_before);
}

#[test]
fn suppression_cleared_on_200_heartbeat_with_different_release_id() {
    let target = golden_valid().fields.target.clone();
    let (_fw, offer) = sized_offer("fwr_new", &target, 64, 0x0C, "9.9.2");
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_old");
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_http(true, 200, Some(&offer));
    assert!(h.nvs.stockbot_get_str(ota_nvs_keys::OTA_SUPP).is_none());
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
fn waiting_state_rejects_suppressed_offer_with_different_release_id() {
    let target = golden_valid().fields.target.clone();
    let (fw_a, offer_a) = sized_offer("fwr_a", &target, 256, 0xAA, "9.9.0");
    let mut h = OtaHarness::new(&target, fw_a, table());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_b");
    let (_fw_b, offer_b) = sized_offer("fwr_b", &target, 128, 0xBB, "9.9.1");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer_a), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
    let opens_before = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
    assert_eq!(c.http_ctl.open_calls.get(), opens_before);
    assert_eq!(c.slots_ctl.written.get(), 0);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
}

#[test]
fn waiting_state_rejects_equal_running_version_offer() {
    let target = golden_valid().fields.target.clone();
    let (fw_a, offer_a) = sized_offer("fwr_a", &target, 256, 0xAA, "9.9.0");
    let mut h = OtaHarness::new(&target, fw_a, table());
    let running = h.ctx.running_version.clone();
    let (_fw_b, offer_b) = sized_offer("fwr_same_wait", &target, 128, 0xBC, &running);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer_a), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
    let opens_before = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
    assert_eq!(c.http_ctl.open_calls.get(), opens_before);
    assert_eq!(c.slots_ctl.written.get(), 0);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a")
    );
}

#[test]
fn offered_state_rejects_suppressed_offer_with_different_release_id() {
    let target = golden_valid().fields.target.clone();
    let (fw_a, offer_a) = sized_offer("fwr_a2", &target, 256, 0xAD, "9.8.0");
    let mut h = OtaHarness::new(&target, fw_a, table());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_b2");
    let (_fw_b, offer_b) = sized_offer("fwr_b2", &target, 128, 0xBE, "9.8.1");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer_a), &ctx);
    let opens_before = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a2")
    );
    assert_eq!(c.http_ctl.open_calls.get(), opens_before);
    assert_eq!(c.slots_ctl.written.get(), 0);
}

#[test]
fn offered_state_rejects_equal_running_version_offer() {
    let target = golden_valid().fields.target.clone();
    let running = golden_valid().fields.version.clone();
    let (fw_a, offer_a) = sized_offer("fwr_a3", &target, 256, 0xAE, "9.7.0");
    let mut h = OtaHarness::new(&target, fw_a, table());
    h.ctx.running_version = running.clone();
    let (_fw_b, offer_b) = sized_offer("fwr_same_off", &target, 128, 0xBF, &running);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer_a), &ctx);
    let opens_before = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_a3")
    );
    assert_eq!(c.http_ctl.open_calls.get(), opens_before);
    assert_eq!(c.slots_ctl.written.get(), 0);
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

fn drive_terminal_download_failed(
    ctl: &mut stockbot_scanner_core::OtaController<'_>,
    ctx: &OtaTickContext,
    c: &common::ota_fakes::OtaHarnessShared,
    offer: &UpdateOffer,
    first_get_ms: u64,
) {
    c.now_ms.set(first_get_ms);
    ctl.on_heartbeat_update(Some(offer.clone()), ctx);
    ctl.tick(ctx);
    c.now_ms.set(INSTALL_WINDOW_IDLE_MS + first_get_ms);
    ctl.tick(ctx);
    for _ in 0..128 {
        if !ctl.phase_is_downloading() {
            break;
        }
        ctl.tick(ctx);
    }
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
}

fn prepare_download_failed_fixture(
    h: &mut OtaHarness,
    release_id: &str,
    version: &str,
) -> (UpdateOffer, u64) {
    let target = golden_valid().fields.target.clone();
    let (fw, offer) = sized_offer(release_id, &target, 32, 0xDA, version);
    h.http.firmware = fw;
    h.http_ctl.bytes_per_connection.set(Some(1));
    (offer, 1_000_u64)
}

#[test]
fn download_failed_backoff_blocks_reoffer_before_60_minutes() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_backoff_a";
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    let (offer, first_get_ms) = prepare_download_failed_fixture(&mut h, release_id, "9.10.0");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    drive_terminal_download_failed(&mut ctl, &ctx, &c, &offer, first_get_ms);
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;
    c.now_ms.set(attempt_start + BACKOFF_ONE_HOUR_MS - 1);
    let opens_before = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert_eq!(c.http_ctl.open_calls.get(), opens_before);
}

#[test]
fn download_failed_backoff_allows_reoffer_after_60_minutes() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_backoff_ok";
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    let (offer, first_get_ms) = prepare_download_failed_fixture(&mut h, release_id, "9.10.1");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    drive_terminal_download_failed(&mut ctl, &ctx, &c, &offer, first_get_ms);
    let opens_after_failure = c.http_ctl.open_calls.get();
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;

    c.now_ms.set(attempt_start + BACKOFF_ONE_HOUR_MS - 1);
    let opens_before_59_59 = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_state,
        "idle",
        "59:59 before expiry: same release still blocked"
    );
    assert_eq!(
        c.http_ctl.open_calls.get(),
        opens_before_59_59,
        "59:59: must not open another GET"
    );

    c.now_ms.set(attempt_start + BACKOFF_ONE_HOUR_MS);
    ctl.tick(&ctx);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_state,
        "waiting",
        "60:00 on the tick: offer adopted after backoff"
    );
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some(release_id)
    );

    for _ in 0..8 {
        if c.http_ctl.open_calls.get() > opens_after_failure {
            break;
        }
        ctl.tick(&ctx);
    }
    assert!(
        c.http_ctl.open_calls.get() > opens_after_failure,
        "fresh attempt must open a new GET after backoff"
    );
    assert_eq!(
        c.http_ctl.last_range_start.get(),
        0,
        "post-backoff download restarts from byte 0, not a resume"
    );
}

#[test]
fn download_failed_backoff_cleared_by_heartbeat_release_b_then_a_starts() {
    let target = golden_valid().fields.target.clone();
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    let (offer_a, first_get_ms) =
        prepare_download_failed_fixture(&mut h, "fwr_backoff_n3a", "9.10.6");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    drive_terminal_download_failed(&mut ctl, &ctx, &c, &offer_a, first_get_ms);
    let (_fw_b, offer_b) = sized_offer("fwr_backoff_n3b", &target, 32, 0xDC, "9.10.7");
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;
    c.now_ms.set(attempt_start + 1_000);
    ctl.on_heartbeat_http(true, 200, Some(&offer_b));
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    let opens_before_a = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer_a.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_backoff_n3a"),
        "release A must be adopted, not B"
    );
    c.now_ms.set(attempt_start + 1_000 + INSTALL_WINDOW_IDLE_MS);
    for _ in 0..8 {
        if c.http_ctl.open_calls.get() > opens_before_a {
            break;
        }
        ctl.tick(&ctx);
    }
    assert!(
        c.http_ctl.open_calls.get() > opens_before_a,
        "release A must start a fresh GET after B cleared backoff context"
    );
    assert_eq!(c.http_ctl.last_range_start.get(), 0);
    assert_eq!(
        ctl.heartbeat_fields().ota_release_id.as_deref(),
        Some("fwr_backoff_n3a")
    );
}

#[test]
fn download_failed_backoff_heartbeat_mid_attempt_keeps_first_get_anchor() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_hb_mid";
    let (fw, offer) = sized_offer(release_id, &target, 4096, 0x21, "9.11.2");
    let mut h = OtaHarness::new(&target, fw, table());
    h.http_ctl.bytes_per_connection.set(Some(512));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    let t0 = 200_000_u64;
    c.now_ms.set(t0);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert_eq!(c.http_ctl.open_calls.get(), 1);
    let mut n = 0_u64;
    while ctl.phase_is_downloading() && n < 2000 {
        c.now_ms.set(t0 + n * 1_000);
        if c.http_ctl.open_calls.get() == 4 {
            ctl.on_heartbeat_http(true, 200, Some(&offer));
        }
        ctl.tick(&ctx);
        n += 1;
    }
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
    let opens_after_fail = c.http_ctl.open_calls.get();
    c.http_ctl.bytes_per_connection.set(None);
    c.now_ms.set(t0 + BACKOFF_ONE_HOUR_MS - 1);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert_eq!(c.http_ctl.open_calls.get(), opens_after_fail);
    c.now_ms.set(t0 + BACKOFF_ONE_HOUR_MS);
    ctl.tick(&ctx);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_state,
        "waiting",
        "60:00 from first GET: offer adopted"
    );
    c.now_ms
        .set(t0 + BACKOFF_ONE_HOUR_MS + INSTALL_WINDOW_IDLE_MS);
    for _ in 0..8 {
        if c.http_ctl.open_calls.get() > opens_after_fail {
            break;
        }
        ctl.tick(&ctx);
    }
    assert!(c.http_ctl.open_calls.get() > opens_after_fail);
    assert_eq!(c.http_ctl.last_range_start.get(), 0);
}

#[test]
fn download_failed_backoff_heartbeat_between_each_get_keeps_anchor() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_hb_each";
    let (fw, offer) = sized_offer(release_id, &target, 512, 0x22, "9.11.3");
    let mut h = OtaHarness::new(&target, fw, table());
    h.http_ctl.fail_transport.set(true);
    h.http_ctl.fail_transport_always.set(true);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    let t0 = 200_000_u64;
    c.now_ms.set(t0);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    let mut k = 0_u64;
    while ctl.phase_is_downloading() && k < 50 {
        c.now_ms.set(t0 + k * 10 * 60 * 1_000);
        ctl.tick(&ctx);
        ctl.on_heartbeat_http(true, 200, Some(&offer));
        k += 1;
    }
    assert_eq!(c.http_ctl.open_calls.get(), 4);
    assert_eq!(
        ctl.heartbeat_fields().ota_error,
        Some(DeviceOtaError::DownloadFailed.as_str())
    );
    let opens_after_fail = c.http_ctl.open_calls.get();
    c.http_ctl.fail_transport.set(false);
    c.http_ctl.fail_transport_always.set(false);
    c.now_ms.set(t0 + BACKOFF_ONE_HOUR_MS - 1);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert_eq!(c.http_ctl.open_calls.get(), opens_after_fail);
    c.now_ms.set(t0 + BACKOFF_ONE_HOUR_MS);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    c.now_ms
        .set(t0 + BACKOFF_ONE_HOUR_MS + INSTALL_WINDOW_IDLE_MS);
    for _ in 0..8 {
        if c.http_ctl.open_calls.get() > opens_after_fail {
            break;
        }
        ctl.tick(&ctx);
    }
    assert!(c.http_ctl.open_calls.get() > opens_after_fail);
    assert_eq!(c.http_ctl.last_range_start.get(), 0);
}

#[test]
fn transport_only_failures_start_backoff_clock_before_open() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_backoff_transport";
    let (fw, offer) = sized_offer(release_id, &target, 32, 0x11, "9.10.8");
    let mut h = OtaHarness::new(&target, fw, table());
    h.http_ctl.fail_transport.set(true);
    h.http_ctl.fail_transport_always.set(true);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let first_get_ms = 5_000_u64;
    let mut ctl = h.controller(&target, false);
    c.now_ms.set(first_get_ms);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(INSTALL_WINDOW_IDLE_MS + first_get_ms);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
    for _ in 0..64 {
        if !ctl.phase_is_downloading() {
            break;
        }
        ctl.tick(&ctx);
    }
    assert!(
        !ctl.phase_is_downloading(),
        "transport failures must leave download phase"
    );
    let hb = ctl.heartbeat_fields();
    assert_eq!(
        hb.ota_state, "failed",
        "four transport failures end with download_failed"
    );
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;
    c.now_ms.set(attempt_start + BACKOFF_ONE_HOUR_MS - 1);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    c.now_ms.set(attempt_start + BACKOFF_ONE_HOUR_MS);
    ctl.tick(&ctx);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
}

#[test]
fn begin_download_resets_first_get_timestamp_after_scan_abort() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_first_get_reset";
    let (fw, offer) = sized_offer(release_id, &target, 32, 0x12, "9.10.9");
    let mut h = OtaHarness::new(&target, fw, table());
    h.http_ctl.bytes_per_connection.set(Some(1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    const EARLY_MS: u64 = 1_000_000;
    const LATE_MS: u64 = 50_000_000;
    c.now_ms.set(EARLY_MS);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(EARLY_MS + INSTALL_WINDOW_IDLE_MS);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    let mut scan_ctx = ctx.clone();
    scan_ctx.user_scan_this_tick = true;
    ctl.tick(&scan_ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    c.now_ms.set(LATE_MS);
    ctl.tick(&ctx);
    c.now_ms.set(LATE_MS + INSTALL_WINDOW_IDLE_MS);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    assert_eq!(ctl.heartbeat_fields().ota_state, "failed");
    let attempt_start = LATE_MS + INSTALL_WINDOW_IDLE_MS;
    c.now_ms.set(attempt_start + BACKOFF_ONE_HOUR_MS - 1);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        ctl.heartbeat_fields().ota_state,
        "idle",
        "backoff keyed off the retried attempt's first GET, not the scan-aborted one"
    );
}

#[test]
fn unread_rolled_back_one_shot_survives_download_failed_terminal() {
    let target = golden_valid().fields.target.clone();
    let (fw_rb, offer_rb) = sized_offer("fwr_j_df_rb", &target, 32, 0x13, "9.11.0");
    let (fw, offer_fail) = sized_offer("fwr_j_df_fail", &target, 32, 0x14, "9.11.1");
    let mut h = OtaHarness::new(&target, fw_rb, table());
    h.http.firmware = fw;
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_j_df_rb");
    h.http_ctl.bytes_per_connection.set(Some(1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.tick(&ctx);
    ctl.on_heartbeat_update(Some(offer_fail), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    let hb = ctl.heartbeat_fields();
    assert_eq!(
        hb.ota_state, "rolled_back",
        "unread rolled_back must outlive a download_failed terminal"
    );
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    let _ = (offer_rb, c);
}

#[test]
fn download_failed_backoff_does_not_block_different_release() {
    let target = golden_valid().fields.target.clone();
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    let (offer_a, first_get_ms) =
        prepare_download_failed_fixture(&mut h, "fwr_backoff_a2", "9.10.2");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    drive_terminal_download_failed(&mut ctl, &ctx, &c, &offer_a, first_get_ms);
    let (_fw_b, offer_b) = sized_offer("fwr_backoff_b2", &target, 32, 0xDB, "9.10.3");
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;
    c.now_ms.set(attempt_start + 1_000);
    ctl.on_heartbeat_update(Some(offer_b), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
}

#[test]
fn download_failed_backoff_cleared_by_no_update_heartbeat() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_backoff_hb";
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    let (offer, first_get_ms) = prepare_download_failed_fixture(&mut h, release_id, "9.10.4");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    drive_terminal_download_failed(&mut ctl, &ctx, &c, &offer, first_get_ms);
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;
    c.now_ms.set(attempt_start + 30_000);
    ctl.on_heartbeat_http(true, 200, None);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
}

#[test]
fn max_range_retries_constant_is_three() {
    assert_eq!(MAX_RANGE_RETRIES, 3);
}

#[test]
fn download_failed_backoff_is_sixty_minutes() {
    assert_eq!(DOWNLOAD_FAILED_BACKOFF_MS, 3_600_000);
}

/// firmware-ota.md §11 case 9: reboot / new controller clears RAM download-failed backoff.
#[test]
fn download_failed_backoff_ram_cleared_on_new_controller() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_backoff_reboot";
    let mut h = OtaHarness::new(&target, Vec::new(), table());
    let (offer, first_get_ms) = prepare_download_failed_fixture(&mut h, release_id, "9.10.5");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    {
        let mut ctl = h.controller(&target, false);
        drive_terminal_download_failed(&mut ctl, &ctx, &c, &offer, first_get_ms);
    }
    let attempt_start = INSTALL_WINDOW_IDLE_MS + first_get_ms;
    c.now_ms.set(attempt_start + 1_000);
    let mut fresh = h.controller(&target, false);
    fresh.on_heartbeat_update(Some(offer), &ctx);
    fresh.tick(&ctx);
    assert_eq!(
        fresh.heartbeat_fields().ota_state,
        "waiting",
        "RAM backoff does not survive a new controller (reboot)"
    );
}
