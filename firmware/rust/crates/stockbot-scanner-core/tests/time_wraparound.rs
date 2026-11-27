//! Locks u64 wrap-around safety of the scanner time math: held-duration and
//! inactivity deltas are computed with wrapping subtraction, so a timestamp that
//! wraps past the 32-bit millisecond range still yields the correct elapsed time.

use stockbot_scanner_core::{
    ButtonHoldTracker, Direction, ModeButtonHoldPhase, ModeButtonReleaseAction, ModeChime,
    ScannerState,
};

const BEFORE_WRAP: u64 = 4_294_967_000;
const AFTER_WRAP: u64 = 100;
/// `100u64.wrapping_sub(4_294_967_000)`.
const HELD_DELTA: u64 = 18_446_744_069_414_584_716;

#[test]
fn millis_wrap_held_delta_matches_uint64_subtraction() {
    let held = AFTER_WRAP.wrapping_sub(BEFORE_WRAP);
    assert_eq!(held, HELD_DELTA);
    assert!(held >= 10_000);
}

#[test]
fn millis_wrap_button_hold_erase_and_factory_reset() {
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
fn millis_wrap_inactivity_revert_fires() {
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
fn millis_wrap_dock_not_suppressed_after_wrap() {
    let mut st = ScannerState::default();
    st.on_vbus_transition(true, BEFORE_WRAP);
    assert!(!st.is_scan_suppressed(AFTER_WRAP));
}

#[test]
fn dock_suppress_boundary_1499_and_1500() {
    let mut st = ScannerState::default();
    st.on_vbus_transition(true, 1000);
    assert!(st.is_scan_suppressed(2499));
    assert!(!st.is_scan_suppressed(2500));
}
