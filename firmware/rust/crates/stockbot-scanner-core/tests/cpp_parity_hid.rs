use stockbot_scanner_core::HidKeycodeDecoder;

/// Ports `test_hid_keycode` from `firmware/test/test_scanner_core/test_main.cpp`.
#[test]
fn cpp_test_hid_keycode() {
    let mut decoder = HidKeycodeDecoder::new();
    let report = [0u8, 0, 0x1e, 0x27, 0, 0, 0, 0];
    decoder.consume_report(&report);
    let enter = [0u8, 0, 0x28, 0, 0, 0, 0, 0];
    let line = decoder.consume_report(&enter);
    assert!(matches!(line.as_deref(), Some("10")));
}
