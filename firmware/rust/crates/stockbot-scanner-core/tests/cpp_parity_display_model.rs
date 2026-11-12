//! Ports display_model unit tests from `firmware/test/test_scanner_core/test_main.cpp`.
#![allow(clippy::field_reassign_with_default)]
use stockbot_scanner_core::{
    build_display_model, format_band2_barcode_keep_end, format_revert_countdown, rgb565_from_hex,
    wrap_product_name_two_lines, AudioCue, CommitMode, Direction, DisplayModelInput,
    ScanResultView, ScanStatusKind, UiScreen, BAND2_CHARS_PER_LINE, ELLIPSIS, RING_CAPACITY,
};

/// `test_display_model`
#[test]
fn cpp_test_display_model() {
    let mut in_val = DisplayModelInput::default();
    in_val.screen = UiScreen::S2Idle;
    in_val.commit_mode = CommitMode::Queue;
    let idle = build_display_model(&in_val);
    assert_eq!(idle.band3, "Queues for review");

    in_val.commit_mode = CommitMode::Immediate;
    let idle = build_display_model(&in_val);
    assert_eq!(idle.band3, "Updates stock now");

    in_val.screen = UiScreen::S3ScanResult;
    in_val.commit_mode = CommitMode::Queue;
    in_val.last_scan = ScanResultView {
        known: true,
        status: ScanStatusKind::Queued,
        product_name: "Kirkland Organic Diced Tomatoes 14.5 oz".into(),
        direction: Direction::StockOut,
        on_hand: 6,
        queued_count: 1,
        ..ScanResultView::default()
    };
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "Queued -1 - 6 on hand");

    in_val.last_scan.on_hand = 0;
    in_val.last_scan.queued_count = 1;
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "Queued -1 - 0 on hand - Check app");

    in_val.last_scan.direction = Direction::StockIn;
    in_val.last_scan.on_hand = 4;
    in_val.last_scan.queued_count = 2;
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "Queued +2 - 4 on hand");

    in_val.commit_mode = CommitMode::Immediate;
    in_val.last_scan.direction = Direction::StockIn;
    in_val.last_scan.on_hand = 5;
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "+1 - 5 on hand");

    in_val.last_scan.direction = Direction::StockOut;
    in_val.last_scan.on_hand = 3;
    in_val.last_scan.backfilled = false;
    in_val.last_scan.delta = -1;
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "-1 - 3 left");
    assert_eq!(q.audio, AudioCue::MediumBeep);

    in_val.last_scan.on_hand = 0;
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "-1 - 0 left (Out of stock)");
    assert_eq!(q.audio, AudioCue::DoubleBeep);

    in_val.last_scan.backfilled = true;
    let q = build_display_model(&in_val);
    assert_eq!(q.band3, "0 on hand - Added & used 1");
    assert_eq!(q.audio, AudioCue::SoftDoublePulse);
    assert!(!q.band3.contains("backfill"));

    in_val.screen = UiScreen::S4UnknownBarcode;
    in_val.last_scan.barcode = "999888777".into();
    let unk = build_display_model(&in_val);
    assert_eq!(unk.band1, "NEW BARCODE");
    assert_eq!(unk.band2_line1, "Unknown item");
    assert_eq!(unk.band3, "In Scan Queue to name");

    in_val.screen = UiScreen::S5OfflineBuffered;
    in_val.offline_queue_count = 7;
    in_val.last_scan.barcode = "4011".into();
    let off = build_display_model(&in_val);
    assert_eq!(off.band3, "7 waiting to send");

    in_val.screen = UiScreen::S7HardError;
    in_val.device_revoked = true;
    let err = build_display_model(&in_val);
    assert_eq!(err.band1, "SCANNER ALERT");
    assert_eq!(err.band2_line1, "Disconnected");
    assert_eq!(err.band2_line2, "from household");
    assert_eq!(err.band3, "Needs a new setup sheet");

    in_val.screen = UiScreen::S0Unprovisioned;
    in_val.pairing_ap_suffix = "ABCD".into();
    let s0 = build_display_model(&in_val);
    assert_eq!(s0.band3, "Then follow the steps on your phone");
    assert_eq!(s0.band2_line2, "Scanner-Setup-ABCD");
    for field in [&s0.band1, &s0.band2_line1, &s0.band2_line2, &s0.band3] {
        assert!(!field.contains("QR"), "S0 strings must not mention QR");
    }

    in_val.screen = UiScreen::S1Pairing;
    let s1 = build_display_model(&in_val);
    assert_eq!(s1.band2_line1, "Scan the pairing");
    assert_eq!(s1.band2_line2, "barcode to connect");

    in_val.screen = UiScreen::SConnected;
    in_val.household_name = "Test Household".into();
    let connected = build_display_model(&in_val);
    assert_eq!(connected.band1, "CONNECTED");
    assert_eq!(connected.band3, "Ready to scan");
    assert_eq!(connected.band2_line1, "Test Household");

    in_val.screen = UiScreen::S2Idle;
    in_val.default_direction = Direction::StockIn;
    in_val.active_direction = Direction::StockOut;
    in_val.show_revert_countdown = true;
    in_val.inactivity_revert_seconds = 180;
    in_val.last_activity_ms = 1000;
    in_val.now_ms = 1000 + 60_000;
    let countdown = build_display_model(&in_val);
    assert_eq!(countdown.band3, "Back to Stock in: 2:00");

    in_val.screen = UiScreen::SPairingExpired;
    let expired = build_display_model(&in_val);
    assert_eq!(expired.band1, "PAIRING CODE EXPIRED");
    assert_eq!(expired.band2_line1, "Print a new");
    assert_eq!(expired.band2_line2, "setup sheet");

    in_val.screen = UiScreen::SWifiButtonHold;
    let hold = build_display_model(&in_val);
    assert_eq!(hold.band1, "HOLDING");
    assert_eq!(hold.band3, "Keep holding to erase everything");

    in_val.screen = UiScreen::SWifiButtonHoldErase;
    let erase_hold = build_display_model(&in_val);
    assert_eq!(erase_hold.band1, "LET GO TO ERASE");
    assert_eq!(erase_hold.band3, "Then use a setup sheet");

    in_val.screen = UiScreen::S7HardError;
    in_val.device_revoked = false;
    in_val.buffer_full = true;
    let full = build_display_model(&in_val);
    let capacity_line = format!("{RING_CAPACITY} scans waiting");
    assert_eq!(full.band1, "MEMORY FULL");
    assert_eq!(full.band2_line1, capacity_line);
    assert_eq!(full.band2_line2, "Scans won't save");
    assert_eq!(full.audio, AudioCue::Silent);

    in_val.screen = UiScreen::S7OfflineBufferRefused;
    in_val.last_scan.barcode = "4011".into();
    let refused = build_display_model(&in_val);
    assert_eq!(refused.band1, "NOT SAVED");
    assert_eq!(refused.band2_line2, "4011");

    in_val.last_scan.barcode = "4000000000011".into();
    let refused = build_display_model(&in_val);
    assert_eq!(refused.band2_line2, "4000000000011");

    in_val.last_scan.barcode = "012345678905".into();
    let refused = build_display_model(&in_val);
    assert_eq!(refused.band2_line2, "012345678905");
}

/// `test_display_band2_text`
#[test]
fn cpp_test_display_band2_text() {
    assert_eq!(BAND2_CHARS_PER_LINE, 18);
    assert_eq!(ELLIPSIS, "...");

    let bc18 = "012345678901234567";
    assert_eq!(bc18.len(), 18);
    assert_eq!(format_band2_barcode_keep_end(bc18), bc18);

    let bc19 = "0123456789012345678";
    assert_eq!(format_band2_barcode_keep_end(bc19), "...456789012345678");

    let mut in_val = DisplayModelInput::default();
    in_val.screen = UiScreen::S7OfflineBufferRefused;
    in_val.last_scan.barcode = bc18.into();
    assert_eq!(build_display_model(&in_val).band2_line2, bc18);
    in_val.last_scan.barcode = bc19.into();
    assert_eq!(
        build_display_model(&in_val).band2_line2,
        "...456789012345678"
    );

    in_val.screen = UiScreen::SConnected;
    in_val.household_name = "Test Household".into();
    let one_line = build_display_model(&in_val);
    assert_eq!(one_line.band2_line1, "Test Household");
    assert!(one_line.band2_line2.is_empty());

    in_val.household_name = "North Test Household".into();
    let wrapped = build_display_model(&in_val);
    assert_eq!(wrapped.band2_line1, "North Test");
    assert_eq!(wrapped.band2_line2, "Household");

    in_val.household_name = "North Test Housexx".into();
    assert_eq!(in_val.household_name.len(), 18);
    let eighteen = build_display_model(&in_val);
    assert_eq!(eighteen.band2_line1, "North Test Housexx");
    assert!(eighteen.band2_line2.is_empty());

    let overflow_name: String = "A".repeat(18) + &"B".repeat(19);
    in_val.household_name = overflow_name.clone();
    let overflow = build_display_model(&in_val);
    assert_eq!(overflow.band2_line1, "A".repeat(18));
    assert_eq!(overflow.band2_line2, format!("{}{}", "B".repeat(15), "..."));

    in_val.household_name = "Château du Supermarché du Nord".into();
    let mb = build_display_model(&in_val);
    assert_eq!(mb.band2_line1, "Château du");
    assert_eq!(mb.band2_line2, "Supermarché du ...");

    let product_wrap = wrap_product_name_two_lines(
        "Kirkland Organic Diced Tomatoes Extra Long Product Name Here That Keeps Going",
        BAND2_CHARS_PER_LINE,
    );
    assert_eq!(product_wrap.0, "Kirkland Organic");
    assert_eq!(product_wrap.1, "Diced Tomatoes ...");
}

/// `test_pairing_failure_display_copy_and_colors`
#[test]
fn cpp_test_pairing_failure_display_copy_and_colors() {
    let mut many = DisplayModelInput::default();
    many.screen = UiScreen::SPairingTooManyTries;
    let too_many = build_display_model(&many);
    assert_eq!(too_many.band1, "TOO MANY TRIES");
    assert_eq!(too_many.band1_bg_rgb565, rgb565_from_hex("#F59E0B"));
    assert_eq!(too_many.band1_fg_rgb565, rgb565_from_hex("#111827"));

    let mut rejected = DisplayModelInput::default();
    rejected.screen = UiScreen::SPairingCodeNotAccepted;
    let not_accepted = build_display_model(&rejected);
    assert_eq!(not_accepted.band1, "CODE NOT ACCEPTED");
    assert_eq!(not_accepted.band1_bg_rgb565, rgb565_from_hex("#DC2626"));
}

/// Spot-check `format_revert_countdown` default-timer branch.
#[test]
fn cpp_test_revert_countdown_initial() {
    let s = format_revert_countdown(300, 0, 1000, Direction::StockIn);
    assert_eq!(s, "Back to Stock in: 5:00");
}
