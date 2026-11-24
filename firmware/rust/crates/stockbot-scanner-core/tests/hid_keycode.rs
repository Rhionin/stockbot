use stockbot_scanner_core::HidKeycodeDecoder;

/// HID keyboard reports decode to ASCII lines terminated by Enter.
#[test]
fn hid_keycode() {
    let mut decoder = HidKeycodeDecoder::new();
    let report = [0u8, 0, 0x1e, 0x27, 0, 0, 0, 0];
    decoder.consume_report(&report);
    let enter = [0u8, 0, 0x28, 0, 0, 0, 0, 0];
    let line = decoder.consume_report(&enter);
    assert!(matches!(line.as_deref(), Some("10")));
}

/// HID usage decoding ignores `shift` for usages `0x1e`–`0x27` (digits); Left Shift still sets `shift` true.
#[test]
fn hid_shifted_digit_one_unchanged() {
    let mut decoder = HidKeycodeDecoder::new();
    let shift_one = [0x02u8, 0, 0x1e, 0, 0, 0, 0, 0];
    decoder.consume_report(&shift_one);
    assert_eq!(decoder.line_buffer(), "1");
}
