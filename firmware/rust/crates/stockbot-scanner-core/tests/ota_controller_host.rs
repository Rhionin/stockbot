//! Firmware host tests for `OtaController` (firmware-ota.md §11).

#![allow(clippy::field_reassign_with_default)]

#[path = "common/mod.rs"]
mod common;

use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use stockbot_scanner_core::{
    canonical_bytes, decode_public_key_b64, ota_nvs_keys, verify_pre_download, DeviceOtaError,
    Fields, NvsStore, OtaTickContext, TrustedKeyTable, MAX_RANGE_RETRIES,
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

fn pattern_firmware(len: usize) -> (Vec<u8>, String) {
    let fw: Vec<u8> = (0..len as u32).map(|i| ((i * 7) % 251) as u8).collect();
    let hex = common::ota_fakes::sha256_hex(&fw);
    (fw, hex)
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

fn count_ranged_opens(c: &common::ota_fakes::OtaHarnessShared) -> usize {
    c.http_ctl
        .open_ranges
        .borrow()
        .iter()
        .filter(|&&r| r > 0)
        .count()
}

fn assert_ranged_resume_non_206_refused(
    c: &common::ota_fakes::OtaHarnessShared,
    ctl: &mut stockbot_scanner_core::OtaController<'_>,
    ctx: &stockbot_scanner_core::OtaTickContext,
    expected_partial: u64,
    fw: &[u8],
) {
    let opens_at_partial = c.http_ctl.open_calls.get();
    let ranged_at_partial = count_ranged_opens(c);
    let aborts_at_partial = c.slots_ctl.abort_inactive_count.get();
    for _ in 0..4 {
        if !ctl.phase_is_downloading() {
            break;
        }
        ctl.tick(ctx);
    }
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::HttpStatus.as_str()));
    assert!(
        c.http_ctl.open_calls.get() > opens_at_partial,
        "ranged resume open expected"
    );
    assert_eq!(c.http_ctl.last_range_start.get(), expected_partial);
    assert_eq!(c.slots_ctl.peak_written.get(), expected_partial);
    assert_eq!(
        &*c.slots_ctl.written_mirror.borrow(),
        &fw[..expected_partial as usize]
    );
    assert!(!c.slots_ctl.marked_valid.get());
    assert!(!c.slots_ctl.boot_switched.get());
    assert!(!c.slots_ctl.rebooted.get());
    assert_eq!(
        c.slots_ctl.abort_inactive_count.get(),
        aborts_at_partial + 1,
        "fail_post_offer must abort the inactive OTA write (esp_ota_abort)"
    );
    assert_eq!(
        count_ranged_opens(c),
        ranged_at_partial + 1,
        "exactly one ranged resume attempt before fail"
    );
    let opens_at_fail = c.http_ctl.open_calls.get();
    for _ in 0..8 {
        ctl.tick(ctx);
        let later = ctl.heartbeat_fields();
        assert_ne!(later.ota_state, "failed");
        assert!(later.ota_error.is_none());
    }
    assert_eq!(
        c.http_ctl.open_calls.get(),
        opens_at_fail,
        "no further HTTP opens after refusal"
    );
    assert_eq!(
        count_ranged_opens(c),
        ranged_at_partial + 1,
        "no further ranged resume attempts after refusal"
    );
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
    assert_ranged_resume_non_206_refused(&c, &mut ctl, &ctx, 400, &fw);
}

#[test]
fn resume_non_206_2xx_204_refused_without_appending() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_r204", &target, 1000, 0x74);
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
    c.http_ctl.resume_http_status.set(Some(204));
    assert_ranged_resume_non_206_refused(&c, &mut ctl, &ctx, 400, &fw);
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
    let bands = c.display_last.borrow();
    let bands = bands.as_ref().expect("OTA overlay rendered");
    assert_eq!(bands.band1, "UPDATING");
    assert_eq!(bands.band2_line1, "Updating,");
    assert_eq!(bands.band2_line2, "do not unplug");
    assert_eq!(bands.band3, "25%");
}

#[test]
fn mid_download_flash_failure_clears_ota_overlay() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_flash_ov", &target, 4096, 0xB1);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(ctl.phase_is_downloading());
    assert!(c.display_last.borrow().is_some());
    c.slots_ctl.reject_write.set(true);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "failed");
    assert!(c.display_last.borrow().is_none());
    assert!(c.display_clear_count.get() >= 1);
}

#[test]
fn mid_download_http_status_failure_clears_ota_overlay() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_http_ov", &target, 4096, 0xB2);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(1024));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(c.display_last.borrow().is_some());
    ctl.tick(&ctx);
    c.http_ctl.fail_status.set(Some(500));
    c.http_ctl.fail_count_remaining.set(1);
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::HttpStatus.as_str()));
    assert!(c.display_last.borrow().is_none());
    assert!(c.display_clear_count.get() >= 1);
}

#[test]
fn pre_download_bad_signature_clears_ota_overlay_from_offered() {
    let target = golden_valid().fields.target.clone();
    let (fw, mut offer, _) = sized_offer("fwr_pre_ov", &target, 512, 0xC1);
    offer.sig_b64 = base64::engine::general_purpose::STANDARD.encode([0u8; 64]);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "failed");
    assert!(c.display_last.borrow().is_none());
    assert!(c.display_clear_count.get() >= 1);
}

#[test]
fn verify_bad_sha_clears_ota_overlay() {
    let target = golden_valid().fields.target.clone();
    let (fw, mut offer, _) = sized_offer("fwr_sha_ov", &target, 256, 0xC2);
    offer.fields.sha256 = "0".repeat(64);
    let (_c, sig) = sign_fields(&offer.fields);
    offer.sig_b64 = sig;
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
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::BadSha256.as_str()));
    assert!(c.display_last.borrow().is_none());
    assert!(c.display_clear_count.get() >= 1);
}

#[test]
fn rolled_back_one_shot_preserved_across_post_download_failure() {
    let target = golden_valid().fields.target.clone();
    let (fw, mut offer, _) = sized_offer("fwr_rb_keep", &target, 128, 0xE1);
    offer.fields.sha256 = "0".repeat(64);
    let (_c, sig) = sign_fields(&offer.fields);
    offer.sig_b64 = sig;
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.nvs.set(ota_nvs_keys::OTA_REL, "fwr_rb");
    h.slots_ctl
        .last_invalid
        .set(Some(stockbot_scanner_core::OtaSlot::Slot1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.tick(&ctx);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "rolled_back");
    assert_eq!(c.slots_ctl.abort_inactive_count.get(), 1);
}

#[test]
fn resume_416_when_two_retries_used_ends_attempt() {
    let target = golden_valid().fields.target.clone();
    let (fw, _) = pattern_firmware(2000);
    let offer = offer_for_firmware("fwr_416_exhaust", &target, &fw, "9.1.7");
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(400));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    for target_written in [400_u64, 800_u64] {
        while ctl.phase_is_downloading() && c.slots_ctl.written.get() < target_written {
            ctl.tick(&ctx);
        }
        assert!(ctl.phase_is_downloading());
        ctl.tick(&ctx);
        while ctl.phase_is_downloading() && c.slots_ctl.written.get() < target_written {
            ctl.tick(&ctx);
        }
    }
    assert_eq!(ctl.download_retry_count(), 2);
    c.http_ctl.resume_http_status.set(Some(416));
    ctl.tick(&ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
}

#[test]
fn err_download_arm_calls_shared_retry_exhaustion() {
    let src = include_str!("../src/ota_controller.rs");
    let read_match = src
        .split("match self.http.read_firmware_chunk(&mut buf) {")
        .nth(1)
        .expect("read_firmware_chunk match")
        .split("fn open_download_stream")
        .next()
        .expect("open_download_stream");
    let err_arm = read_match
        .split("Err(_) => {")
        .nth(1)
        .expect("Err arm")
        .split("\n            }")
        .next()
        .expect("Err arm body");
    assert!(
        err_arm.contains("if self.download_retry_exhausted()"),
        "read-Err path must call download_retry_exhausted(), not a lone retries > MAX check"
    );
    assert!(
        !err_arm.contains("if self.download_retries > MAX_RANGE_RETRIES"),
        "read-Err path must not bypass shared exhaustion helper"
    );
}

#[test]
fn read_errors_exhaust_on_fourth_get_without_fifth() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_read4";
    let (fw, offer, _) = sized_offer(release_id, &target, 64, 0x7B);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.fail_read_remaining.set(3);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    let peak_retries = drive_until_not_downloading(&mut ctl, &ctx);
    assert_eq!(c.http_ctl.open_calls.get(), 4);
    assert_eq!(peak_retries, MAX_RANGE_RETRIES);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
}

#[test]
fn three_consecutive_read_errors_exhaust_retries() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_read_err";
    let (fw, offer, _) = sized_offer(release_id, &target, 4096, 0xEE);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.fail_read_remaining.set(4);
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    let peak_retries = drive_until_not_downloading(&mut ctl, &ctx);
    assert_eq!(c.http_ctl.open_calls.get(), 4);
    assert_eq!(peak_retries, MAX_RANGE_RETRIES);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
}

#[test]
fn download_first_tick_overlay_shows_zero_percent() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_pct0", &target, 4096, 0x01);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    let bands = c.display_last.borrow();
    let bands = bands.as_ref().expect("overlay rendered");
    assert_eq!(bands.band3, "0%");
}

#[test]
fn scan_abort_clears_ota_overlay_after_partial_download() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_scan_ov", &target, 8192, 0x88);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(c.display_last.borrow().is_some());
    let clears_before = c.display_clear_count.get();
    let mut abort_ctx = ctx.clone();
    abort_ctx.user_scan_this_tick = true;
    ctl.tick(&abort_ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert!(c.display_last.borrow().is_none());
    assert!(c.display_clear_count.get() > clears_before);
}

#[test]
fn button_abort_clears_ota_overlay_after_partial_download() {
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_btn_ov", &target, 8192, 0x99);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    ctl.tick(&ctx);
    assert!(c.display_last.borrow().is_some());
    let clears_before = c.display_clear_count.get();
    let mut abort_ctx = ctx.clone();
    abort_ctx.user_button_this_tick = true;
    ctl.tick(&abort_ctx);
    assert_eq!(ctl.heartbeat_fields().ota_state, "waiting");
    assert!(c.display_last.borrow().is_none());
    assert!(c.display_clear_count.get() > clears_before);
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

fn offer_for_firmware(
    release_id: &str,
    target: &str,
    fw: &[u8],
    version: &str,
) -> stockbot_scanner_core::UpdateOffer {
    let mut fields = golden_valid().fields();
    fields.target = target.to_owned();
    fields.version = version.to_owned();
    fields.size = fw.len() as i64;
    fields.sha256 = common::ota_fakes::sha256_hex(fw);
    let (_, sig) = sign_fields(&fields);
    OtaHarness::offer_from_fields(
        release_id,
        fields,
        &sig,
        &format!("/api/device/firmware/{release_id}"),
    )
}

#[test]
fn resume_416_restarts_from_byte_zero() {
    let target = golden_valid().fields.target.clone();
    let (fw, _) = pattern_firmware(1000);
    let offer = offer_for_firmware("fwr_416", &target, &fw, "9.1.6");
    let mut h = OtaHarness::new(&target, fw.clone(), allowlist());
    h.http_ctl.bytes_per_connection.set(Some(400));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    let opens_before_416 = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    drive_download_to_partial_written(&c, &mut ctl, &ctx, 400);
    assert_eq!(&*c.slots_ctl.written_mirror.borrow(), &fw[..400]);
    assert_eq!(c.slots_ctl.peak_written.get(), 400);

    ctl.tick(&ctx);
    assert!(
        ctl.phase_is_downloading(),
        "bytes_per_connection cap closes body; download resumes on next tick"
    );
    c.http_ctl.resume_http_status.set(Some(416));
    let retries_before_416 = ctl.download_retry_count();
    assert_eq!(
        retries_before_416, 1,
        "setup connection drop consumed one retry"
    );
    ctl.tick(&ctx);
    assert_eq!(
        ctl.download_retry_count(),
        retries_before_416,
        "416 restart must not consume an extra range retry"
    );
    assert_eq!(
        MAX_RANGE_RETRIES - ctl.download_retry_count(),
        2,
        "two range retries remain after setup drop and 416 restart"
    );
    assert_eq!(c.slots_ctl.written.get(), 0);
    assert!(c.slots_ctl.written_mirror.borrow().is_empty());
    assert!(!c.slots_ctl.marked_valid.get());
    assert!(!c.slots_ctl.boot_switched.get());
    assert!(!c.slots_ctl.rebooted.get());

    let ranges_after_416 = c.http_ctl.open_ranges.borrow().clone();
    assert!(
        ranges_after_416.iter().any(|&r| r == 400),
        "resume attempt used Range"
    );
    let _ = opens_before_416;

    ctl.tick(&ctx);
    let ranges = c.http_ctl.open_ranges.borrow();
    assert_eq!(
        *ranges.last().expect("reopen after 416"),
        0,
        "416 restart opens from byte 0 without Range"
    );
    assert_eq!(
        ctl.download_retry_count(),
        1,
        "successful restart chunk must not reset retry counter"
    );

    c.http_ctl.bytes_per_connection.set(None);
    c.http_ctl.resume_http_status.set(None);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    assert_eq!(ctl.heartbeat_fields().ota_state, "verifying");
    assert!(!c.slots_ctl.rebooted.get());
    ctl.tick(&ctx);
    assert!(c.slots_ctl.rebooted.get());
    assert_eq!(*c.slots_ctl.written_mirror.borrow(), fw);
    assert!(!c.slots_ctl.marked_valid.get());
}

#[test]
fn download_retries_increment_on_dropped_connection_not_reset_per_chunk() {
    const MAX_RANGE_RETRIES: u32 = 3;
    let target = golden_valid().fields.target.clone();
    let (fw, offer, _) = sized_offer("fwr_retry_cnt", &target, 1000, 0x71);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(100));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    while c.slots_ctl.written.get() < 100 {
        assert_eq!(ctl.download_retry_count(), 0);
        ctl.tick(&ctx);
    }
    assert_eq!(c.slots_ctl.written.get(), 100);
    ctl.tick(&ctx);
    assert!(
        ctl.download_retry_count() >= 1,
        "dropped connection should bump retries, not reset after chunk"
    );
    assert!(
        ctl.download_retry_count() < MAX_RANGE_RETRIES,
        "single drop must not exhaust retries"
    );
    let _ = c;
}

#[test]
fn failure_after_rollback_reported_ends_bad_sha_attempt() {
    let target = golden_valid().fields.target.clone();
    let (fw, mut offer, _) = sized_offer("fwr_post_rb", &target, 512, 0x22);
    offer.fields.sha256 = "0".repeat(64);
    let (_canon, sig) = sign_fields(&offer.fields);
    offer.sig_b64 = sig;
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
    assert_eq!(hb.ota_error, Some(DeviceOtaError::BadSha256.as_str()));
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
    ctl.tick(&ctx);
    while ctl.phase_is_downloading() {
        ctl.tick(&ctx);
    }
    let mut fail_state = "";
    for _ in 0..64 {
        let hb = ctl.heartbeat_fields();
        if hb.ota_state == "failed" {
            fail_state = hb.ota_state;
            break;
        }
        ctl.tick(&ctx);
    }
    assert_eq!(fail_state, "failed");
    drop(ctl);
    assert!(h.nvs.stockbot_get_str(ota_nvs_keys::OTA_REL).is_none());
    let _ = c;
}

fn drive_until_not_downloading(
    ctl: &mut stockbot_scanner_core::OtaController<'_>,
    ctx: &OtaTickContext,
) -> u32 {
    let mut peak_retries = 0;
    for _ in 0..128 {
        if !ctl.phase_is_downloading() {
            return peak_retries;
        }
        peak_retries = peak_retries.max(ctl.download_retry_count());
        ctl.tick(ctx);
    }
    panic!("download did not end");
}

#[test]
fn three_eof_drops_abort_at_max_range_retries() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_eof3";
    let (fw, offer, _) = sized_offer(release_id, &target, 32, 0x51);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let running_before = c.slots_ctl.running.get();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    let peak_retries = drive_until_not_downloading(&mut ctl, &ctx);
    assert_eq!(c.http_ctl.open_calls.get(), 4);
    assert_eq!(
        peak_retries, MAX_RANGE_RETRIES,
        "terminal after four GETs at MAX retries, not MAX+1"
    );
    assert_eq!(c.slots_ctl.abort_inactive_count.get(), 1);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
    assert_eq!(hb.ota_release_id.as_deref(), Some(release_id));
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
    assert!(!c.slots_ctl.boot_switched.get());
    assert_eq!(c.slots_ctl.running.get(), running_before);
    let opens = c.http_ctl.open_calls.get();
    ctl.on_heartbeat_update(Some(offer), &ctx);
    ctl.tick(&ctx);
    assert_eq!(
        c.http_ctl.open_calls.get(),
        opens,
        "no fifth GET after backoff"
    );
}

#[test]
fn three_failed_resumes_end_in_download_failed_idle() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_retry";
    let (fw, offer, _) = sized_offer(release_id, &target, 32, 0x01);
    let mut h = OtaHarness::new(&target, fw, allowlist());
    h.http_ctl.bytes_per_connection.set(Some(1));
    let ctx = open_window_ctx(&h.ctx);
    let c = h.controls();
    let mut ctl = h.controller(&target, false);
    ctl.on_heartbeat_update(Some(offer.clone()), &ctx);
    ctl.tick(&ctx);
    c.now_ms.set(200_000);
    ctl.tick(&ctx);
    let peak_retries = drive_until_not_downloading(&mut ctl, &ctx);
    assert_eq!(c.http_ctl.open_calls.get(), 4);
    assert_eq!(peak_retries, MAX_RANGE_RETRIES);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
    assert_eq!(ctl.heartbeat_fields().ota_state, "idle");
}

#[test]
fn transport_open_failures_terminal_without_firmware_body() {
    let target = golden_valid().fields.target.clone();
    let release_id = "fwr_transport_exhaust";
    let (fw, offer, _) = sized_offer(release_id, &target, 512, 0x7A);
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
    let peak_retries = drive_until_not_downloading(&mut ctl, &ctx);
    let hb = ctl.heartbeat_fields();
    assert_eq!(hb.ota_state, "failed");
    assert_eq!(hb.ota_error, Some(DeviceOtaError::DownloadFailed.as_str()));
    assert_eq!(
        c.http_ctl.open_calls.get(),
        MAX_RANGE_RETRIES,
        "transport failures count as GET attempts but never open a body"
    );
    let _ = peak_retries;
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
