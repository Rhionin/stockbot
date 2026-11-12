use stockbot_scanner_core::{ButtonHoldTracker, ModeButtonHoldPhase, ModeButtonReleaseAction};

/// Ports `test_button_hold_tracker` from `firmware/test/test_scanner_core/test_main.cpp`.
#[test]
fn cpp_test_button_hold_tracker() {
    let mut tracker = ButtonHoldTracker::new();
    tracker.on_press(0);
    assert_eq!(
        tracker.on_release(4999),
        ModeButtonReleaseAction::ShortPress
    );

    tracker.on_press(0);
    assert_eq!(tracker.on_release(5000), ModeButtonReleaseAction::WifiSetup);

    tracker.on_press(0);
    assert_eq!(tracker.on_release(9999), ModeButtonReleaseAction::WifiSetup);

    tracker.on_press(0);
    assert_eq!(
        tracker.on_hold_progress(10_000),
        Some(ModeButtonHoldPhase::EraseWarning)
    );
    assert_eq!(
        tracker.on_release(10_000),
        ModeButtonReleaseAction::FactoryReset
    );

    tracker.on_press(0);
    tracker.on_hold_progress(10_000);
    tracker.on_hold_progress(30_000);
    assert!(tracker.is_down());
    assert_eq!(
        tracker.on_release(30_000),
        ModeButtonReleaseAction::FactoryReset
    );

    tracker.on_press(0);
    tracker.on_hold_progress(5500);
    assert_eq!(tracker.on_release(5500), ModeButtonReleaseAction::WifiSetup);

    tracker.on_press(0);
    assert!(tracker.on_hold_progress(4999).is_none());
    let at5000 = tracker.on_hold_progress(5000);
    assert!(matches!(at5000, Some(ModeButtonHoldPhase::WifiSetupHold)));
    assert!(tracker.on_hold_progress(5001).is_none());

    tracker.on_press(0);
    let at9999 = tracker.on_hold_progress(9999);
    assert!(matches!(at9999, Some(ModeButtonHoldPhase::WifiSetupHold)));
    assert!(tracker.on_hold_progress(9999).is_none());

    tracker.on_press(0);
    let at10000 = tracker.on_hold_progress(10_000);
    assert!(matches!(at10000, Some(ModeButtonHoldPhase::EraseWarning)));
}
