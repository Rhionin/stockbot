//! Locks C++/Rust parity: oversized integers and parse failures (`-`, `-x` → token `-`) keep defaults.

use stockbot_scanner_core::{
    parse_pair_complete_response_json, DeviceHubConfigParsed, PairCompleteParsed,
};

#[test]
fn pair_heartbeat_seconds_i64_overflow_keeps_default() {
    let body =
        r#"{"deviceId":"dev-1","token":"sbd_t","heartbeatSeconds":999999999999999999999999}"#;
    let mut parsed = PairCompleteParsed {
        config: DeviceHubConfigParsed::new(),
        heartbeat_seconds: 60,
        device_token: String::new(),
        ..Default::default()
    };
    assert!(parse_pair_complete_response_json(201, body, &mut parsed));
    assert_eq!(parsed.heartbeat_seconds, 60);
}

#[test]
fn pair_heartbeat_seconds_bare_minus_keeps_default() {
    let body = r#"{"deviceId":"dev-1","token":"sbd_t","heartbeatSeconds":-}"#;
    let mut parsed = PairCompleteParsed {
        config: DeviceHubConfigParsed::new(),
        heartbeat_seconds: 60,
        ..Default::default()
    };
    assert!(parse_pair_complete_response_json(201, body, &mut parsed));
    assert_eq!(parsed.heartbeat_seconds, 60);
}

#[test]
fn pair_heartbeat_seconds_minus_x_keeps_default() {
    let body = r#"{"deviceId":"dev-1","token":"sbd_t","heartbeatSeconds":-x}"#;
    let mut parsed = PairCompleteParsed {
        config: DeviceHubConfigParsed::new(),
        heartbeat_seconds: 60,
        ..Default::default()
    };
    assert!(parse_pair_complete_response_json(201, body, &mut parsed));
    assert_eq!(parsed.heartbeat_seconds, 60);
}

#[test]
fn pair_heartbeat_seconds_oversized_digit_run_keeps_default() {
    let body =
        r#"{"deviceId":"dev-1","token":"sbd_t","heartbeatSeconds":123456789012345678901234567890}"#;
    let mut parsed = PairCompleteParsed {
        config: DeviceHubConfigParsed::new(),
        heartbeat_seconds: 60,
        ..Default::default()
    };
    assert!(parse_pair_complete_response_json(201, body, &mut parsed));
    assert_eq!(parsed.heartbeat_seconds, 60);
}
