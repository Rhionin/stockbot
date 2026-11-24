#![allow(clippy::field_reassign_with_default)]

use std::fs;
use std::path::PathBuf;

use serde::Deserialize;
use stockbot_scanner_core::{
    apply_display_band_colors, build_display_model, display_utf8_char_count,
    format_band2_barcode_keep_end, format_revert_countdown, render_display_bands, rgb565_from_hex,
    wrap_product_name_two_lines, CommitMode, Direction, DisplayBands, DisplayModelInput,
    ScanResultView, ScanStatusKind, UiScreen, BAND2_CHARS_PER_LINE, BAND3_CHARS_PER_LINE,
};

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

#[derive(Debug, Deserialize)]
struct LastScanJson {
    status: String,
    known: bool,
    #[serde(rename = "productName")]
    product_name: String,
    barcode: String,
    direction: String,
    #[serde(rename = "queuedCount")]
    queued_count: i32,
    #[serde(rename = "onHand")]
    on_hand: i32,
    delta: i32,
    backfilled: bool,
}

#[derive(Debug, Deserialize)]
struct InputJson {
    screen: String,
    #[serde(rename = "commitMode")]
    commit_mode: String,
    #[serde(rename = "activeDirection")]
    active_direction: String,
    #[serde(rename = "defaultDirection")]
    default_direction: String,
    #[serde(rename = "inactivityRevertSeconds")]
    inactivity_revert_seconds: u32,
    #[serde(rename = "lastActivityMs")]
    last_activity_ms: u64,
    #[serde(rename = "nowMs")]
    now_ms: u64,
    #[serde(rename = "offlineQueueCount")]
    offline_queue_count: usize,
    #[serde(rename = "bufferFull")]
    buffer_full: bool,
    #[serde(rename = "deviceRevoked")]
    device_revoked: bool,
    #[serde(rename = "pairingApSuffix")]
    pairing_ap_suffix: String,
    #[serde(rename = "connectedLabel")]
    connected_label: String,
    #[serde(rename = "householdName")]
    household_name: String,
    #[serde(rename = "showRevertCountdown")]
    show_revert_countdown: bool,
    #[serde(rename = "vbusPresent")]
    vbus_present: bool,
    #[serde(rename = "batteryPct")]
    battery_pct: Option<i32>,
    #[serde(rename = "lastScan")]
    last_scan: LastScanJson,
}

#[derive(Debug, Deserialize)]
struct BandsJsonRaw {
    band1: String,
    #[serde(rename = "band1Right")]
    band1_right: String,
    #[serde(rename = "band1Bg")]
    band1_bg: String,
    #[serde(rename = "band1BgRgb565")]
    band1_bg_rgb565: u16,
    #[serde(rename = "band1FgRgb565")]
    band1_fg_rgb565: u16,
    #[serde(rename = "band2Line1")]
    band2_line1: String,
    #[serde(rename = "band2Line2")]
    band2_line2: String,
    band3: String,
    #[serde(rename = "band3FgRgb565")]
    band3_fg_rgb565: u16,
    audio: String,
}

#[derive(Debug, Deserialize)]
struct ScreenVector {
    name: String,
    input: InputJson,
    bands: BandsJsonRaw,
}

#[derive(Debug, Deserialize)]
struct HelperVector {
    name: String,
    kind: String,
    a: String,
    n: u32,
    u0: u64,
    u1: u64,
    direction: String,
    output: serde_json::Value,
}

fn parse_direction(s: &str) -> Direction {
    match s {
        "stock_in" => Direction::StockIn,
        _ => Direction::StockOut,
    }
}

fn parse_commit_mode(s: &str) -> CommitMode {
    if s == "immediate" {
        CommitMode::Immediate
    } else {
        CommitMode::Queue
    }
}

fn parse_scan_status(s: &str) -> ScanStatusKind {
    match s {
        "queued" => ScanStatusKind::Queued,
        "committed" => ScanStatusKind::Committed,
        "flagged" => ScanStatusKind::Flagged,
        _ => ScanStatusKind::None,
    }
}

fn parse_ui_screen(s: &str) -> UiScreen {
    match s {
        "S0_Unprovisioned" => UiScreen::S0Unprovisioned,
        "S1_Pairing" => UiScreen::S1Pairing,
        "S2_Idle" => UiScreen::S2Idle,
        "S3_ScanResult" => UiScreen::S3ScanResult,
        "S4_UnknownBarcode" => UiScreen::S4UnknownBarcode,
        "S5_OfflineBuffered" => UiScreen::S5OfflineBuffered,
        "S6_DockReminder" => UiScreen::S6DockReminder,
        "S7_HardError" => UiScreen::S7HardError,
        "S7_OfflineBufferRefused" => UiScreen::S7OfflineBufferRefused,
        "S_Connected" => UiScreen::SConnected,
        "S_PairingExpired" => UiScreen::SPairingExpired,
        "S_PairingTooManyTries" => UiScreen::SPairingTooManyTries,
        "S_PairingCodeNotAccepted" => UiScreen::SPairingCodeNotAccepted,
        "S_PairingCantConnect" => UiScreen::SPairingCantConnect,
        "S_WifiButtonHold" => UiScreen::SWifiButtonHold,
        "S_WifiButtonHoldErase" => UiScreen::SWifiButtonHoldErase,
        "S_WifiSetup" => UiScreen::SWifiSetup,
        other => panic!("unknown UiScreen {other}"),
    }
}

fn input_from_json(j: &InputJson) -> DisplayModelInput {
    let scan = &j.last_scan;
    DisplayModelInput {
        screen: parse_ui_screen(&j.screen),
        commit_mode: parse_commit_mode(&j.commit_mode),
        active_direction: parse_direction(&j.active_direction),
        default_direction: parse_direction(&j.default_direction),
        inactivity_revert_seconds: j.inactivity_revert_seconds,
        last_activity_ms: j.last_activity_ms,
        now_ms: j.now_ms,
        offline_queue_count: j.offline_queue_count,
        buffer_full: j.buffer_full,
        device_revoked: j.device_revoked,
        pairing_ap_suffix: j.pairing_ap_suffix.clone(),
        connected_label: j.connected_label.clone(),
        household_name: j.household_name.clone(),
        last_scan: ScanResultView {
            status: parse_scan_status(&scan.status),
            known: scan.known,
            product_name: scan.product_name.clone(),
            barcode: scan.barcode.clone(),
            direction: parse_direction(&scan.direction),
            queued_count: scan.queued_count,
            on_hand: scan.on_hand,
            delta: scan.delta,
            backfilled: scan.backfilled,
        },
        show_revert_countdown: j.show_revert_countdown,
        vbus_present: j.vbus_present,
        battery_pct: j.battery_pct,
    }
}

fn assert_bands_match(name: &str, want: &BandsJsonRaw, got: &DisplayBands) {
    assert_eq!(got.band1, want.band1, "{name} band1");
    assert_eq!(got.band1_right, want.band1_right, "{name} band1_right");
    assert_eq!(got.band1_bg, want.band1_bg, "{name} band1_bg");
    assert_eq!(
        got.band1_bg_rgb565, want.band1_bg_rgb565,
        "{name} band1_bg_rgb565"
    );
    assert_eq!(
        got.band1_fg_rgb565, want.band1_fg_rgb565,
        "{name} band1_fg_rgb565"
    );
    assert_eq!(got.band2_line1, want.band2_line1, "{name} band2_line1");
    assert_eq!(got.band2_line2, want.band2_line2, "{name} band2_line2");
    assert_eq!(got.band3, want.band3, "{name} band3");
    assert_eq!(
        got.band3_fg_rgb565, want.band3_fg_rgb565,
        "{name} band3_fg_rgb565"
    );
    assert_eq!(format!("{:?}", got.audio), want.audio, "{name} audio");
}

#[test]
fn display_model_screens_json_matches_cpp_goldens() {
    let path = repo_root().join("testdata/display-model/screens.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    let vectors: Vec<ScreenVector> = serde_json::from_str(&raw).expect("parse screens.json");
    assert!(!vectors.is_empty(), "screens.json must not be empty");

    for v in &vectors {
        let input = input_from_json(&v.input);
        let got = render_display_bands(&input);
        assert_bands_match(&v.name, &v.bands, &got);
    }
}

#[test]
fn display_model_helpers_json_matches_cpp_goldens() {
    let path = repo_root().join("testdata/display-model/helpers.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    let vectors: Vec<HelperVector> = serde_json::from_str(&raw).expect("parse helpers.json");

    for v in &vectors {
        match v.kind.as_str() {
            "formatBand2BarcodeKeepEnd" => {
                let got = format_band2_barcode_keep_end(&v.a);
                let want = v.output.as_str().expect("string output");
                assert_eq!(got, want, "{}", v.name);
            }
            "wrapProductNameTwoLines" => {
                let (l1, l2) = wrap_product_name_two_lines(&v.a, BAND2_CHARS_PER_LINE);
                let want_l1 = v.output["line1"].as_str().unwrap();
                let want_l2 = v.output["line2"].as_str().unwrap();
                assert_eq!(l1, want_l1, "{} line1", v.name);
                assert_eq!(l2, want_l2, "{} line2", v.name);
            }
            "formatRevertCountdown" => {
                let got = format_revert_countdown(v.n, v.u0, v.u1, parse_direction(&v.direction));
                let want = v.output.as_str().expect("string output");
                assert_eq!(got, want, "{}", v.name);
            }
            "rgb565FromHex" => {
                let got = rgb565_from_hex(&v.a);
                let want = v.output.as_u64().expect("number output") as u16;
                assert_eq!(got, want, "{}", v.name);
            }
            "applyDisplayBandColors" => {
                let mut bands = DisplayBands::default();
                bands.band3 = v.a.clone();
                apply_display_band_colors(&mut bands);
                let want = v.output.as_u64().expect("number output") as u16;
                assert_eq!(bands.band3_fg_rgb565, want, "{}", v.name);
            }
            other => panic!("unknown helper kind {other}"),
        }
    }
}

#[test]
fn display_band_limits_like_cpp_stress() {
    let path = repo_root().join("testdata/display-model/screens.json");
    let raw = fs::read_to_string(&path).unwrap();
    let vectors: Vec<ScreenVector> = serde_json::from_str(&raw).unwrap();

    for v in &vectors {
        let input = input_from_json(&v.input);
        let bands = build_display_model(&input);
        assert!(
            display_utf8_char_count(&bands.band2_line1) <= BAND2_CHARS_PER_LINE,
            "{} band2 line1 overflow",
            v.name
        );
        assert!(
            display_utf8_char_count(&bands.band2_line2) <= BAND2_CHARS_PER_LINE,
            "{} band2 line2 overflow",
            v.name
        );
        assert!(
            display_utf8_char_count(&bands.band3) <= BAND3_CHARS_PER_LINE,
            "{} band3 overflow",
            v.name
        );
    }
}
