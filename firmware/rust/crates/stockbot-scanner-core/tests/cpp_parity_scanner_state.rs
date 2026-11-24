use stockbot_scanner_core::{
    BacklightLevel, ButtonHoldGesture, Direction, ModeChime, ScannerState,
};

/// Ports `test_scanner_state` from `firmware/test/test_scanner_core/test_main.cpp`.
#[test]
fn cpp_test_scanner_state() {
    let mut st = ScannerState::default();
    assert_eq!(st.active_direction, Direction::StockOut);
    st.backlight = BacklightLevel::Dim20;
    let wake = st.on_mode_button_press(1000);
    assert!(wake.woke_backlight && !wake.toggled_direction);
    assert_eq!(st.backlight, BacklightLevel::Full100);
    let toggle = st.on_mode_button_press(1100);
    assert!(toggle.toggled_direction);
    assert_eq!(st.active_direction, Direction::StockIn);

    st.last_activity_ms = 1000;
    st.inactivity_revert_seconds = 300;
    st.active_direction = Direction::StockIn;
    st.default_direction = Direction::StockOut;
    st.on_scan_or_activity(2000);
    let no_revert = st.tick_inactivity_revert(2000 + 299_999);
    assert!(no_revert.is_none());
    let revert = st.tick_inactivity_revert(2000 + 300_000);
    assert!(matches!(revert, Some(ModeChime::ModeRevert)));
    assert_eq!(st.active_direction, Direction::StockOut);

    st.on_vbus_transition(true, 5000);
    assert!(st.is_scan_suppressed(6000));
    assert!(!st.is_scan_suppressed(7000));

    assert_eq!(
        ScannerState::classify_button_hold(2000),
        ButtonHoldGesture::ShortPress
    );
    assert_eq!(
        ScannerState::classify_button_hold(5000),
        ButtonHoldGesture::WifiUpdate
    );
    assert_eq!(
        ScannerState::classify_button_hold(9990),
        ButtonHoldGesture::WifiUpdate
    );
    assert_eq!(
        ScannerState::classify_button_hold(10_000),
        ButtonHoldGesture::FactoryReset
    );
}
