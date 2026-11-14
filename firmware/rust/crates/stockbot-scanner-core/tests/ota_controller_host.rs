//! Firmware host tests for `OtaController` (firmware-ota.md §11).

#![allow(clippy::field_reassign_with_default)]

#[path = "common/mod.rs"]
mod common;

use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use stockbot_scanner_core::{
    canonical_bytes, decode_public_key_b64, ota_nvs_keys, verify_pre_download, DeviceOtaError,
    Fields, NvsStore, TrustedKeyTable,
};

use common::ota_fakes::{open_window_ctx, test_firmware, OtaHarness};
use common::vector_fixtures::golden_valid;

fn allowlist() -> TrustedKeyTable {
    let v = golden_valid();
    let pk = decode_public_key_b64(&v.public_key_b64).unwrap();
    let mut t = TrustedKeyTable::new();
    t.insert(v.fields.key_id.clone(), pk);
    t
}

fn sign_fields(fields: &Fields) -> (Vec<u8>, String) {
    let canonical = canonical_bytes(fields).unwrap();
    let sk = SigningKey::from_bytes(&[0x3b; 32]);
    let sig = base64::engine::general_purpose::STANDARD.encode(sk.sign(&canonical).to_bytes());
    (canonical, sig)
}

fn sized_offer(
    release_id: &str,
    target: &str,
    size: i64,
    byte: u8,
) -> (Vec<u8>, stockbot_scanner_core::UpdateOffer, String) {
    let v = golden_valid();
    let (fw, hex) = test_firmware(size as usize, byte);
    let mut fields = v.fields();
    fields.target = target.to_owned();
    fields.size = size;
    fields.sha256 = hex.clone();
    let (_canonical, sig) = sign_fields(&fields);
    let offer = OtaHarness::offer_from_fields(
        release_id,
        fields,
        &sig,
        &format!("/api/device/firmware/{release_id}"),
    );
    (fw, offer, hex)
}

#[test]
fn happy_path_download_mark_valid_succeeded() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_happy", &target, 512, 0xCD);
    let table = allowlist();
    verify_pre_download(
        &canonical_bytes(&offer.fields).unwrap(),
        &offer.sig_b64,
        &offer.fields.key_id,
        &target,
        &table,
    )
    .unwrap();
    let mut h = OtaHarness::new(&target, fw, table);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    assert_eq!(ctl.heartbeat_fields().ota_state, "verifying");
    ctl.tick(&ctx);
    assert!(c.slots_ctl.rebooted.get());
    ctl.enter_pending_verify_after_boot("fwr_happy");
    assert!(ctl.sleep_inhibited());
    ctl.on_heartbeat_http(true, 200, Some(&offer));
    let done = ctl.heartbeat_fields();
    assert_eq!(done.ota_state, "succeeded");
    assert!(c.slots_ctl.marked_valid.get());
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
}

#[test]
fn pre_download_refusals_fetch_zero_bytes() {
    let table = allowlist();
    let v = golden_valid();
    let base_fields = v.fields();
    let cases: Vec<(&str, Fields, &str)> = vec![
        (
            "wrong_target",
            {
                let mut f = base_fields.clone();
                f.target = "sticks3_u173".to_owned();
                f
            },
            DeviceOtaError::WrongTarget.as_str(),
        ),
        (
            "unknown_key",
            {
                let mut f = base_fields.clone();
                f.key_id = "fw-2099-9".to_owned();
                f
            },
            DeviceOtaError::UnknownKey.as_str(),
        ),
        (
            "bad_signature",
            base_fields.clone(),
            DeviceOtaError::BadSignature.as_str(),
        ),
        (
            "too_large",
            {
                let mut f = base_fields.clone();
                f.size = 4_000_000;
                f
            },
            DeviceOtaError::TooLarge.as_str(),
        ),
    ];
    for (name, fields, want_err) in cases {
        let (fw, _) = test_firmware(64, 1);
        let mut h = OtaHarness::new(&v.fields.target, fw, table.clone());
        let sig = if name == "bad_signature" {
            "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==".to_owned()
        } else if name == "too_large" {
            v.sig_b64.clone()
        } else {
            sign_fields(&fields).1
        };
        let offer =
            OtaHarness::offer_from_fields("fwr_pre", fields, &sig, "/api/device/firmware/fwr_pre");
        let ctx = open_window_ctx(&h.ctx);
        let c = h.controls();
        let mut ctl = h.controller(&v.fields.target, false);
        ctl.on_heartbeat_update(Some(offer), &ctx);
        ctl.tick(&ctx);
        let hb = ctl.heartbeat_fields();
        assert_eq!(hb.ota_state, "failed", "{name}");
        assert_eq!(hb.ota_error, Some(want_err), "{name}");
        assert_eq!(c.http_ctl.open_calls.get(), 0, "{name}");
        assert_eq!(c.slots_ctl.written.get(), 0, "{name}");
    }
}

#[test]
fn bad_size_and_bad_sha256_after_download() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_sz", &target, 256, 0x11);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    c.slots_ctl.force_written.set(Some(150));
    ctl.tick(&ctx);
    let fail = ctl.heartbeat_fields();
    assert_eq!(fail.ota_state, "failed");
    assert_eq!(fail.ota_error, Some(DeviceOtaError::BadSize.as_str()));
    assert!(!c.slots_ctl.marked_valid.get());
    assert!(!c.slots_ctl.boot_switched.get());
    assert!(!c.slots_ctl.rebooted.get());

    let (fw2, offer2, _) = sized_offer("fwr_hash", &target, 256, 0x22);
    let mut h2 = OtaHarness::new(&target, vec![0xFF; 256], allowlist());
    let c2 = h2.controls();
    let mut ctl2 = h2.controller(&target, false);
    ctl2.on_heartbeat_update(Some(offer2), &ctx);
    ctl2.tick(&ctx);
    c2.now_ms.set(200_000);
    ctl2.tick(&ctx);
    while ctl2.phase_is_downloading() {
        ctl2.tick(&ctx);
    }
    ctl2.tick(&ctx);
    let _ = fw2;
    let hb = ctl2.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::BadSha256.as_str()));
    assert!(!c2.slots_ctl.marked_valid.get());
    assert!(!c2.slots_ctl.boot_switched.get());
    assert!(!c2.slots_ctl.rebooted.get());
}

fn drive_download_to_partial_written(
    c: &common::ota_fakes::OtaHarnessShared,
    ctl: &mut stockbot_scanner_core::OtaController<'_>,
    ctx: &stockbot_scanner_core::OtaTickContext,
    target_bytes: u64,
) {
    while ctl.phase_is_downloading() && c.slots_ctl.written.get() < target_bytes {
        ctl.tick(ctx);
    }
    assert_eq!(c.slots_ctl.written.get(), target_bytes);
    assert!(ctl.phase_is_downloading());
}

fn assert_resume_http_rejection(
    c: &common::ota_fakes::OtaHarnessShared,
    ctl: &mut stockbot_scanner_core::OtaController<'_>,
    ctx: &stockbot_scanner_core::OtaTickContext,
    expected_partial: u64,
    fw: &[u8],
) {
    let opens_before = c.http_ctl.open_calls.get();
    ctl.tick(ctx);
    while ctl.phase_is_downloading() {
        ctl.tick(ctx);
    }
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::HttpStatus.as_str()));
    assert!(
        c.http_ctl.open_calls.get() > opens_before,
        "resume open expected"
    );
    assert_eq!(c.slots_ctl.peak_written.get(), expected_partial);
    assert_eq!(
        &*c.slots_ctl.written_mirror.borrow(),
        &fw[..expected_partial as usize]
    );
    assert!(!c.slots_ctl.marked_valid.get());
    assert!(!c.slots_ctl.boot_switched.get());
    assert!(!c.slots_ctl.rebooted.get());
}

#[test]
fn resume_requires_206_rejects_http_200_without_appending() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_r200", &target, 1000, 0x71);
    let mut h = OtaHarness::new(&target, fw.clone(), allowlist());
    h.http_ctl.bytes_per_connection.set(Some(400));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    drive_download_to_partial_written(&c, &mut ctl, &ctx, 400);
    c.http_ctl.resume_http_status.set(Some(200));
    assert_resume_http_rejection(&c, &mut ctl, &ctx, 400, &fw);
}

#[test]
fn resume_at_40_percent_after_transport_failure() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_resume", &target, 1000, 0x5A);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(400));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    for _ in 0..200 {
        if c.slots_ctl.rebooted.get() {
            break;
        }
        ctl.tick(&ctx);
        if !ctl.phase_is_downloading() {
            let _ = ctl.heartbeat_fields();
        }
    }
    assert!(
        c.slots_ctl.rebooted.get(),
        "opens={:?}",
        c.http_ctl.open_ranges.borrow()
    );
    let ranges = c.http_ctl.open_ranges.borrow().clone();
    assert!(
        ranges.iter().any(|&r| r == 400),
        "must resume mid-stream, got {ranges:?}"
    );
}

#[test]
fn updating_overlay_display_copy_while_downloading() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_disp", &target, 4096, 0xD0);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
    assert!(c.slots_ctl.written.get() >= 1024);
    let bands = h.display.last.as_ref().expect("OTA overlay rendered");
    assert_eq!(bands.band1, "UPDATING");
    assert_eq!(bands.band2_line1, "Updating,");
    assert_eq!(bands.band2_line2, "do not unplug");
    assert_eq!(bands.band3, "25%");
}

#[test]
fn download_one_bounded_chunk_per_tick() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_big", &target, 64 * 1024, 0x11);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    let w = c.slots_ctl.written.get();
    assert!(
        w > 0 && w < 64 * 1024,
        "first downloading tick should write one chunk, got {w}"
    );
}

#[test]
fn scan_aborts_download_after_partial_write() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_scan_mid", &target, 16 * 1024, 0x77);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(c.slots_ctl.written.get() > 0);
    let partial = c.slots_ctl.written.get();
    let mut abort_ctx = ctx.clone();
    abort_ctx.user_scan_this_tick = true;
    ctl.tick(&abort_ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert_eq!(c.slots_ctl.peak_written.get(), partial);
}

#[test]
fn button_aborts_download_after_partial_write() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_btn_mid", &target, 8 * 1024, 0x55);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(c.slots_ctl.written.get() > 0);
    let mut abort_ctx = ctx.clone();
    abort_ctx.user_button_this_tick = true;
    ctl.tick(&abort_ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
}

#[test]
fn resume_416_restarts_from_byte_zero() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_416", &target, 1000, 0x73);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(400));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    drive_download_to_partial_written(&c, &mut ctl, &ctx, 400);
    c.http_ctl.resume_http_status.set(Some(416));
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
        if c.http_ctl.open_ranges.borrow().len() >= 2 {
            break;
        }
    }
    assert_eq!(c.slots_ctl.written.get(), 0);
    c.http_ctl.resume_http_status.set(None);
    for _ in 0..120 {
        if c.slots_ctl.rebooted.get() {
            break;
        }
        ctl.tick(&ctx);
    }
    assert!(c.slots_ctl.rebooted.get());
    let ranges = c.http_ctl.open_ranges.borrow();
    assert!(ranges.contains(&400));
    assert!(ranges.contains(&0));
}

#[test]
fn failure_after_rollback_reported_ends_bad_sha_attempt() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_post_rb", &target, 512, 0x22);
    let mut bad = offer.clone();
    bad.fields.sha256 = "0".repeat(64);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_rb");
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "rolled_back");
    ctl.on_heartbeat_update(Some(bad), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    let mut saw_failed = false;
    for _ in 0..800 {
        if c.slots_ctl.rebooted.get() {
            break;
        }
        ctl.tick(&ctx);
        let hb = ctl.heartbeat_fields();
        if hb.ota_state == "failed" {
            saw_failed = true;
            break;
        }
        if hb.ota_state == "verifying" {
            ctl.tick(&ctx);
        }
    }
    assert!(saw_failed);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert!(!ctl.phase_is_downloading());
    let _ = c;
}

#[test]
fn bad_signature_after_rollback_reported_pre_download() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_sig_rb", &target, 64, 0x22);
    let mut bad = offer;
    bad.sig_b64 = base64::engine::general_purpose::STANDARD.encode([0u8; 64]);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_rb");
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let mut ctl = h.controller(&target, false);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "rolled_back");
    ctl.on_heartbeat_update(Some(bad), &ctx);
    for _ in 0..3 {
        ctl.tick(&ctx);
    }
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::BadSignature.as_str()));
}

#[test]
fn pending_rollback_report_not_overwritten_by_pre_download_failure() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_pending", &target, 64, 0x22);
    let mut bad = offer;
    bad.sig_b64 = base64::engine::general_purpose::STANDARD.encode([0u8; 64]);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_rb");
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(bad), &ctx);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "rolled_back");
}

#[test]
fn rolled_back_release_suppressed_rest_of_boot() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_rb", &target, 64, 0x44);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_rb");
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.tick(&ctx);
    let _ = ctl.heartbeat_fields();
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert_eq!(c.http_ctl.open_calls.get(), 0);
}

#[test]
fn boot_switch_failure_clears_ota_rel() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_bs", &target, 128, 0x66);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.slots_ctl.set_boot_ok.set(false);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    for _ in 0..500 {
        if !ctl.phase_is_downloading() {
            let hb = ctl.heartbeat_fields();
            if hb.ota_state == "failed" || hb.ota_state == "verifying" {
                break;
            }
        }
        ctl.tick(&ctx);
    }
    drop(ctl);
    assert!(h.nvs.stockbot_get_str(ota_nvs_keys::OTA_REL).is_none());
    let _ = c;
}

#[test]
fn three_failed_resumes_return_to_waiting() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_retry", &target, 800, 0x01);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.fail_transport.set(true);
    h.http_ctl.fail_transport_always.set(true);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    for _ in 0..3 {
        ctl.tick(&ctx);
    }
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert_eq!(c.http_ctl.open_calls.get(), 3);
}

#[test]
fn power_cut_before_boot_switch_running_slot_unchanged() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_pwr", &target, 128, 0x02);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.slots_ctl.set_boot_ok.set(false);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let running_before = c.slots_ctl.running.get();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert!(!c.slots_ctl.boot_switched.get());
    assert_eq!(c.slots_ctl.running.get(), running_before);
}

#[test]
fn checkin_timeout_marks_invalid() {
    let mut h = OtaHarness::new("atoms3lite_u173_bench", Vec::new(), allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller("atoms3lite_u173_bench", false);
    ctl.enter_pending_verify_after_boot("fwr_timeout");
    c.now_ms.set(600_001);
    ctl.tick(&ctx);
    assert!(c.slots_ctl.marked_invalid.get());
}

#[test]
fn heartbeat_401_does_not_mark_valid() {
    let mut h = OtaHarness::new("atoms3lite_u173_bench", Vec::new(), allowlist());
    let c = h.controls();
    let mut ctl = h.controller("atoms3lite_u173_bench", false);
    ctl.enter_pending_verify_after_boot("fwr_401");
    ctl.on_heartbeat_http(true, 401, None);
    let state = ctl.heartbeat_fields().ota_state;
    assert!(!c.slots_ctl.marked_valid.get());
    assert_eq!(state, "pending_verify");
}

#[test]
fn sleep_inhibited_while_pending_verify() {
    let mut h = OtaHarness::new("atoms3lite_u173_bench", Vec::new(), allowlist());
    let mut ctl = h.controller("atoms3lite_u173_bench", false);
    assert!(!ctl.sleep_inhibited());
    ctl.enter_pending_verify_after_boot("fwr_sleep");
    assert!(ctl.sleep_inhibited());
}

#[test]
fn offer_withdrawn_while_waiting_returns_idle() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_wd", &target, 64, 0x08);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    ctl.on_heartbeat_update(None, &ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
}

#[test]
fn rollback_suppression_blocks_same_release() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_supp", &target, 64, 0x09);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.nvs.set(ota_nvs_keys::OTA_SUPP, "fwr_supp");
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert!(!ctl.phase_is_downloading());
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert!(!ctl.phase_is_downloading());
    assert_eq!(c.http_ctl.open_calls.get(), 0);
    assert_eq!(c.slots_ctl.written.get(), 0);
}

#[test]
fn scan_aborts_download_back_to_waiting() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_scan", &target, 4096, 0x77);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
    let mut abort_ctx = ctx.clone();
    abort_ctx.user_scan_this_tick = true;
    ctl.tick(&abort_ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert_eq!(c.slots_ctl.written.get(), 0);
}

#[test]
fn no_window_nonempty_ring() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_ring", &target, 64, 0x03);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    let mut blocked = ctx.clone();
    blocked.ring_empty = false;
    c.now_ms.set(200_000);
    ctl.tick(&blocked);
    assert!(!ctl.phase_is_downloading());
}

#[test]
fn no_window_wifi_rssi_below_min() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_rssi", &target, 64, 0x05);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    let mut weak = ctx.clone();
    weak.wifi_rssi_dbm = Some(-85);
    c.now_ms.set(200_000);
    ctl.tick(&weak);
    assert!(!ctl.phase_is_downloading());
}

#[test]
fn sticks3_low_battery_off_dock_no_download() {
    let (fw, offer, _) = sized_offer("fwr_batt", "sticks3_u173", 64, 0x04);
    let mut h = OtaHarness::new("sticks3_u173", fw, allowlist());
    h.power.vbus.set(false);
    h.power.battery_pct.set(Some(40));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller("sticks3_u173", true);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    assert!(!ctl.phase_is_downloading());
}

#[test]
fn rollback_report_once_then_idle() {
    let mut h = OtaHarness::new("atoms3lite_u173_bench", Vec::new(), allowlist());
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_rb");
    h.nvs.set(ota_nvs_keys::OTA_ERR, "boot_failed");
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let mut ctl = h.controller("atoms3lite_u173_bench", false);
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "rolled_back");
    assert!(hb.rollback);
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    drop(ctl);
    assert_eq!(
        stockbot_scanner_core::NvsStore::stockbot_get_str(&h.nvs, ota_nvs_keys::OTA_SUPP)
            .as_deref(),
        Some("fwr_rb")
    );
}
