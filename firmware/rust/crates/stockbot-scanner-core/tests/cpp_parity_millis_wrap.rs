//! Locks uint64_t wrapping time math against `firmware/test/test_millis_wrap_parity.cpp`
//! (same subtraction as `button_hold_tracker.cpp` / `scanner_state.cpp` after millis widening).

use std::path::PathBuf;
use std::process::Command;

use stockbot_scanner_core::{
    ButtonHoldTracker, Direction, ModeButtonHoldPhase, ModeButtonReleaseAction, ModeChime,
    ScannerState,
};

const BEFORE_WRAP: u64 = 4_294_967_000;
const AFTER_WRAP: u64 = 100;
/// From C++ probe: `(uint64_t)100 - (uint64_t)4294967000`.
const HELD_DELTA: u64 = 18_446_744_069_414_584_716;

fn repo_firmware_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../..")
        .join("firmware")
        .canonicalize()
        .expect("firmware dir")
}

fn millis_wrap_binary() -> PathBuf {
    let firmware = repo_firmware_dir();
    let cxx = std::env::var("CXX").unwrap_or_else(|_| "g++".to_owned());
    let out_bin = PathBuf::from("/tmp/stockbot_millis_wrap_parity_rusttest");
    let out = Command::new(&cxx)
        .args([
            "-std=c++17",
            "-Wall",
            "-Wextra",
            "-Werror",
            "-Isrc/core",
            "src/core/button_hold_tracker.cpp",
            "src/core/scanner_state.cpp",
            "test/test_millis_wrap_parity.cpp",
            "-o",
            out_bin.to_str().expect("out path"),
        ])
        .current_dir(&firmware)
        .output()
        .expect("compile millis wrap parity");
    assert!(
        out.status.success(),
        "millis wrap compile failed: {}",
        String::from_utf8_lossy(&out.stderr)
    );
    out_bin
}

#[test]
fn cpp_millis_wrap_binary_stdout_held_delta() {
    let bin = millis_wrap_binary();
    let out = Command::new(&bin)
        .output()
        .expect("run millis wrap parity binary");
    assert!(out.status.success());
    let stdout = String::from_utf8_lossy(&out.stdout);
    assert!(
        stdout
            .lines()
            .any(|l| l == "held_delta=18446744069414584716"),
        "stdout missing held_delta line: {stdout}"
    );
}

#[test]
fn cpp_millis_wrap_held_delta_matches_uint64_subtraction() {
    let held = AFTER_WRAP.wrapping_sub(BEFORE_WRAP);
    assert_eq!(held, HELD_DELTA);
    assert!(held >= 10_000);
}

#[test]
fn cpp_millis_wrap_button_hold_erase_and_factory_reset() {
    let mut tracker = ButtonHoldTracker::new();
    tracker.on_press(BEFORE_WRAP);
    let phase = tracker.on_hold_progress(AFTER_WRAP);
    assert_eq!(phase, Some(ModeButtonHoldPhase::EraseWarning));
    assert_eq!(
        tracker.on_release(AFTER_WRAP),
        ModeButtonReleaseAction::FactoryReset
    );
}

#[test]
fn cpp_millis_wrap_inactivity_revert_fires() {
    let mut st = ScannerState {
        active_direction: Direction::StockIn,
        default_direction: Direction::StockOut,
        inactivity_revert_seconds: 300,
        last_activity_ms: BEFORE_WRAP,
        ..Default::default()
    };
    assert_eq!(
        st.tick_inactivity_revert(AFTER_WRAP),
        Some(ModeChime::ModeRevert)
    );
}

#[test]
fn cpp_millis_wrap_dock_not_suppressed_after_wrap() {
    let mut st = ScannerState::default();
    st.on_vbus_transition(true, BEFORE_WRAP);
    assert!(!st.is_scan_suppressed(AFTER_WRAP));
}

#[test]
fn cpp_dock_suppress_boundary_1499_and_1500() {
    let mut st = ScannerState::default();
    st.on_vbus_transition(true, 1000);
    assert!(st.is_scan_suppressed(2499));
    assert!(!st.is_scan_suppressed(2500));
}
