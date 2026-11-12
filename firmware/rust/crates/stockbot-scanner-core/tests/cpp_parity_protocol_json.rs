//! Ports selected cases from `firmware/test/test_scanner_core/test_main.cpp` (`test_protocol_json`).

use stockbot_scanner_core::{
    build_batch_scans_request_json, build_heartbeat_request_json, build_pair_complete_request_json,
    build_single_scan_request_json, make_record, parse_device_scan_batch_response_json,
    parse_device_scan_response_json, parse_heartbeat_response_json,
    parse_pair_complete_response_json, parse_setup_qr_payload, Direction, HeartbeatParsed,
    PairCompleteParsed, ParsedDeviceScanResponse,
};

#[test]
fn test_protocol_json_smoke() {
    let pair_req = build_pair_complete_request_json("SBP-0123-ABCD-G", "Pantry", "1.0.0", "boot-1");
    assert!(pair_req.contains("esp32_scanner"));

    let mut parsed = PairCompleteParsed {
        config: stockbot_scanner_core::DeviceHubConfigParsed::new(),
        heartbeat_seconds: 60,
        ..Default::default()
    };
    let pair_resp = r#"{"deviceId":"dev-1","token":"sbd_testtoken","config":{"label":"Room","defaultDirection":"stock_out","inactivityRevertSeconds":300,"commitMode":"queue","configVersion":2}}"#;
    assert!(parse_pair_complete_response_json(
        201,
        pair_resp,
        &mut parsed
    ));
    assert_eq!(parsed.device_token, "sbd_testtoken");
    assert_eq!(parsed.config.config_version, 2);

    let pair_with_name = r#"{"deviceId":"dev-1","token":"sbd_testtoken","householdName":"Pantry","config":{"label":"Room","defaultDirection":"stock_out","inactivityRevertSeconds":300,"commitMode":"queue","configVersion":2}}"#;
    assert!(parse_pair_complete_response_json(
        201,
        pair_with_name,
        &mut parsed
    ));
    assert_eq!(parsed.household_name, "Pantry");

    let rec = make_record("scan-1", "4011", Direction::StockOut, 1000);
    let scan_req = build_single_scan_request_json(&rec, "boot-1", 5500, false);
    assert!(scan_req.contains("\"unitCount\":1"));
    assert!(scan_req.contains("\"ageMs\":5500"));

    let rec2 = make_record("scan-2", "4011", Direction::StockOut, 2000);
    let batch_req = build_batch_scans_request_json(&[rec, rec2], "boot-1", 5000);
    let pos4000 = batch_req.find("\"ageMs\":4000").unwrap();
    let pos3000 = batch_req.find("\"ageMs\":3000").unwrap();
    assert!(pos4000 < pos3000);

    let mut scan_out = ParsedDeviceScanResponse::default();
    let scan_resp = r#"{"id":"scan-1","status":"created","scanStatus":"queued","known":true,"productName":"Milk","barcode":"4011","direction":"stock_out","queuedCount":1,"onHand":6,"delta":0,"backfilled":false}"#;
    assert!(parse_device_scan_response_json(
        200,
        scan_resp,
        &mut scan_out
    ));
    assert_eq!(scan_out.status, "created");
    assert_eq!(scan_out.on_hand, 6);

    let batch_resp = r#"{"results":[{"id":"scan-a","status":"created","onHand":1},{"id":"scan-b","status":"rejected","onHand":0}],"config":{}}"#;
    let mut batch_items = Vec::new();
    assert!(parse_device_scan_batch_response_json(
        200,
        batch_resp,
        &mut batch_items
    ));
    assert_eq!(batch_items.len(), 2);
    assert_eq!(batch_items[0].id, "scan-a");
    assert_eq!(batch_items[1].status, "rejected");

    let bounded = r#"{"results":[{"id":"inner","status":"created","onHand":2}],"onHand":99}"#;
    assert!(parse_device_scan_response_json(200, bounded, &mut scan_out));
    assert_eq!(scan_out.on_hand, 2);

    let mut hb = HeartbeatParsed::default();
    let hb_resp = r#"{"config":{"defaultDirection":"stock_in","inactivityRevertSeconds":600,"commitMode":"immediate","configVersion":9},"rotateToken":false}"#;
    assert!(parse_heartbeat_response_json(200, hb_resp, &mut hb));
    assert_eq!(hb.config.default_direction, "stock_in");

    let hb_req = build_heartbeat_request_json("1.0.0-esp32", "boot-1", 60, 3, 80, true, true);
    assert!(hb_req.contains("\"droppedCount\":0"));
    assert!(hb_req.contains("\"queued\":3"));

    let mut setup = stockbot_scanner_core::SetupQrParsed::default();
    assert!(parse_setup_qr_payload(
        r#"{"v":1,"ssid":"Home","pass":"pw","url":"https://x","code":"SBP-0123-ABCD-G"}"#,
        &mut setup
    ));
    assert_eq!(setup.ssid, "Home");
}
