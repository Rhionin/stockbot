use std::fs;
use std::path::PathBuf;

use serde::Deserialize;
use stockbot_scanner_core::{
    build_batch_scans_request_json, build_heartbeat_request_json, build_pair_complete_request_json,
    build_single_scan_request_json, http_failure_is_retryable, make_record, parse_api_error_code,
    parse_device_scan_batch_response_json, parse_device_scan_response_json,
    parse_heartbeat_response_json, parse_pair_complete_response_json, parse_setup_qr_payload,
    BatchScanResultItem, DeviceHubConfigParsed, Direction, HeartbeatParsed, PairCompleteParsed,
    ParsedDeviceScanResponse, RingScanRecord, SetupQrParsed,
};

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

fn read_json(name: &str) -> String {
    let path = repo_root().join("testdata/device-protocol").join(name);
    fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()))
}

#[derive(Debug, Deserialize)]
struct RequestVector {
    name: String,
    method: String,
    endpoint: String,
    body: String,
    #[serde(rename = "deviceReachable")]
    device_reachable: bool,
    #[serde(rename = "expectedStatus")]
    expected_status: i32,
}

#[derive(Debug, Deserialize)]
struct HubConfigJson {
    label: String,
    #[serde(rename = "defaultDirection")]
    default_direction: String,
    #[serde(rename = "inactivityRevertSeconds")]
    inactivity_revert_seconds: u32,
    #[serde(rename = "commitMode")]
    commit_mode: String,
    #[serde(rename = "configVersion")]
    config_version: u32,
}

#[derive(Debug, Deserialize)]
struct PairParsedJson {
    #[serde(rename = "deviceId")]
    device_id: String,
    #[serde(rename = "deviceToken")]
    device_token: String,
    #[serde(rename = "householdId")]
    household_id: String,
    #[serde(rename = "householdName")]
    household_name: String,
    #[serde(rename = "heartbeatSeconds")]
    heartbeat_seconds: u32,
    config: HubConfigJson,
}

#[derive(Debug, Deserialize)]
struct ScanParsedJson {
    id: String,
    status: String,
    #[serde(rename = "scanStatus")]
    scan_status: String,
    known: bool,
    #[serde(rename = "productNameIsNull")]
    product_name_is_null: bool,
    #[serde(rename = "productName")]
    product_name: String,
    barcode: String,
    direction: String,
    #[serde(rename = "queuedCount")]
    queued_count: i32,
    delta: i32,
    #[serde(rename = "onHand")]
    on_hand: i32,
    backfilled: bool,
    code: String,
    unauthorized: bool,
    #[serde(rename = "parseOk")]
    parse_ok: bool,
}

#[derive(Debug, Deserialize)]
struct HeartbeatParsedJson {
    #[serde(rename = "parseOk")]
    parse_ok: bool,
    config: HubConfigJson,
    #[serde(rename = "rotatedDeviceToken")]
    rotated_device_token: Option<String>,
    #[serde(rename = "hasRotatedDeviceToken")]
    has_rotated_device_token: bool,
}

#[derive(Debug, Deserialize)]
struct SetupParsedJson {
    ok: bool,
    ssid: String,
    pass: String,
    url: String,
    code: String,
}

#[derive(Debug, Deserialize)]
struct ResponseVector {
    name: String,
    kind: String,
    #[serde(rename = "httpStatus")]
    http_status: i32,
    body: String,
    #[serde(rename = "fnOk")]
    fn_ok: bool,
    #[serde(default)]
    parsed: Option<serde_json::Value>,
    #[serde(default)]
    items: Option<Vec<BatchItemJson>>,
}

#[derive(Debug, Deserialize)]
struct BatchItemJson {
    id: String,
    status: String,
}

#[derive(Debug, Deserialize)]
struct SetupVector {
    name: String,
    raw: String,
    #[serde(rename = "fnOk")]
    fn_ok: bool,
    parsed: SetupParsedJson,
}

#[derive(Debug, Deserialize)]
struct ApiErrorVector {
    name: String,
    body: String,
    #[serde(rename = "fnOk")]
    fn_ok: bool,
    code: String,
}

#[derive(Debug, Deserialize)]
struct RetryVector {
    #[serde(rename = "httpStatus")]
    http_status: i32,
    #[serde(rename = "transportOk")]
    transport_ok: bool,
    retryable: bool,
}

fn hub_from_json(c: &HubConfigJson) -> DeviceHubConfigParsed {
    DeviceHubConfigParsed {
        label: c.label.clone(),
        default_direction: c.default_direction.clone(),
        inactivity_revert_seconds: c.inactivity_revert_seconds,
        commit_mode: c.commit_mode.clone(),
        config_version: c.config_version,
    }
}

fn make_rec_from_names(id: &str, barcode: &str, direction: u8, scanned_ms: u32) -> RingScanRecord {
    let dir = if direction == 1 {
        Direction::StockIn
    } else {
        Direction::StockOut
    };
    make_record(id, barcode, dir, scanned_ms)
}

#[test]
fn device_protocol_request_vectors_match_cpp() {
    let raw = read_json("requests.json");
    let vectors: Vec<RequestVector> = serde_json::from_str(&raw).expect("parse requests.json");

    for v in &vectors {
        let got = match v.name.as_str() {
            "pair-basic" => {
                build_pair_complete_request_json("SBP-0123-ABCD-G", "Pantry", "1.0.0", "boot-1")
            }
            "pair-escape-unicode" => build_pair_complete_request_json(
                "SBP-\"\\n",
                "Café \"hub\"",
                "1.0.0\r\n",
                "boot\\id",
            ),
            "pair-max-label" => build_pair_complete_request_json(
                "SBP-0123-ABCD-G",
                "Label \"long\" Café",
                "1.0.0\r\n",
                "boot\\id-long-label-zzzz",
            ),
            "scan-single" => {
                let rec = make_rec_from_names("scan-1", "4011", 0, 1000);
                build_single_scan_request_json(&rec, "boot\\n", 5500, false)
            }
            "scan-single-stock-in" => {
                let rec = make_rec_from_names("scan-in", "4011", 1, 2000);
                build_single_scan_request_json(&rec, "boot-1", 100, false)
            }
            "scan-single-time-approx" => {
                let rec = make_rec_from_names("scan-approx", "4011", 0, 0);
                build_single_scan_request_json(&rec, "boot-1", 0, true)
            }
            "scan-single-escaped" => {
                let rec = make_rec_from_names("id\"\\n", "12\"34", 0, 500);
                build_single_scan_request_json(&rec, "boot\\n", 42, false)
            }
            "scan-single-max-fields" => {
                let rec = make_rec_from_names(
                    "012345678901234567890123456789012345",
                    "123456789012345",
                    1,
                    4294967295,
                );
                build_single_scan_request_json(
                    &rec,
                    "boot-max-id-field-123456789012345678",
                    999999999,
                    false,
                )
            }
            "scan-batch-three" => {
                let rec = make_rec_from_names("scan-1", "4011", 0, 1000);
                let rec2 = make_rec_from_names("scan-2", "7890123456789", 1, 2000);
                let rec3 = make_rec_from_names("scan-3", "123", 0, 0);
                build_batch_scans_request_json(&[rec, rec2, rec3], "boot-1", 5000)
            }
            "heartbeat-full" => {
                build_heartbeat_request_json("1.0.0-esp32", "boot-1", 60, 3, 80, true, true)
            }
            "heartbeat-no-battery" => {
                build_heartbeat_request_json("1.0.0", "boot-2", 0, 0, -1, false, false)
            }
            "heartbeat-escape" => {
                build_heartbeat_request_json("ver\"\\n", "boot\r", 1, 500, 0, false, true)
            }
            other => panic!("unknown request vector {other}"),
        };
        assert_eq!(got, v.body, "request body {}", v.name);
        assert_eq!(v.method, "POST", "method {}", v.name);
        assert!(v.endpoint.starts_with("/api/"), "endpoint {}", v.endpoint);
        if v.device_reachable {
            assert!(
                (200..500).contains(&v.expected_status),
                "deviceReachable implies expectedStatus < 500 for {}",
                v.name
            );
        } else {
            assert!(
                v.expected_status == 400,
                "deviceReachable:false expects HTTP 400 for {}",
                v.name
            );
        }
    }
}

#[test]
fn device_protocol_response_vectors_match_cpp() {
    let raw = read_json("responses.json");
    let vectors: Vec<ResponseVector> = serde_json::from_str(&raw).expect("parse responses.json");

    for v in &vectors {
        match v.kind.as_str() {
            "pair_complete" => {
                let mut parsed = PairCompleteParsed {
                    config: DeviceHubConfigParsed::new(),
                    heartbeat_seconds: 60,
                    ..Default::default()
                };
                let fn_ok = parse_pair_complete_response_json(v.http_status, &v.body, &mut parsed);
                assert_eq!(fn_ok, v.fn_ok, "pair fnOk {}", v.name);
                let expect: PairParsedJson =
                    serde_json::from_value(v.parsed.clone().unwrap()).unwrap();
                assert_eq!(parsed.device_id, expect.device_id, "{}", v.name);
                assert_eq!(parsed.device_token, expect.device_token, "{}", v.name);
                assert_eq!(parsed.household_id, expect.household_id, "{}", v.name);
                assert_eq!(parsed.household_name, expect.household_name, "{}", v.name);
                assert_eq!(
                    parsed.heartbeat_seconds, expect.heartbeat_seconds,
                    "{}",
                    v.name
                );
                let cfg = hub_from_json(&expect.config);
                assert_eq!(parsed.config, cfg, "{}", v.name);
            }
            "device_scan" => {
                let mut parsed = ParsedDeviceScanResponse::default();
                let fn_ok = parse_device_scan_response_json(v.http_status, &v.body, &mut parsed);
                assert_eq!(fn_ok, v.fn_ok, "scan fnOk {}", v.name);
                let expect: ScanParsedJson =
                    serde_json::from_value(v.parsed.clone().unwrap()).unwrap();
                assert_eq!(parsed.id, expect.id, "{}", v.name);
                assert_eq!(parsed.status, expect.status, "{}", v.name);
                assert_eq!(parsed.scan_status, expect.scan_status, "{}", v.name);
                assert_eq!(parsed.known, expect.known, "{}", v.name);
                assert_eq!(
                    parsed.product_name_is_null, expect.product_name_is_null,
                    "{}",
                    v.name
                );
                assert_eq!(parsed.product_name, expect.product_name, "{}", v.name);
                assert_eq!(parsed.barcode, expect.barcode, "{}", v.name);
                assert_eq!(parsed.direction, expect.direction, "{}", v.name);
                assert_eq!(parsed.queued_count, expect.queued_count, "{}", v.name);
                assert_eq!(parsed.delta, expect.delta, "{}", v.name);
                assert_eq!(parsed.on_hand, expect.on_hand, "{}", v.name);
                assert_eq!(parsed.backfilled, expect.backfilled, "{}", v.name);
                assert_eq!(parsed.code, expect.code, "{}", v.name);
                assert_eq!(parsed.unauthorized, expect.unauthorized, "{}", v.name);
                assert_eq!(parsed.parse_ok, expect.parse_ok, "{}", v.name);
            }
            "scan_batch" => {
                let mut items: Vec<BatchScanResultItem> = Vec::new();
                let fn_ok =
                    parse_device_scan_batch_response_json(v.http_status, &v.body, &mut items);
                assert_eq!(fn_ok, v.fn_ok, "batch fnOk {}", v.name);
                let expect = v.items.as_ref().expect("batch items");
                assert_eq!(items.len(), expect.len(), "{}", v.name);
                for (got, want) in items.iter().zip(expect.iter()) {
                    assert_eq!(got.id, want.id);
                    assert_eq!(got.status, want.status);
                }
            }
            "heartbeat" => {
                let mut parsed = HeartbeatParsed::default();
                let fn_ok = parse_heartbeat_response_json(v.http_status, &v.body, &mut parsed);
                assert_eq!(fn_ok, v.fn_ok, "heartbeat fnOk {}", v.name);
                let expect: HeartbeatParsedJson =
                    serde_json::from_value(v.parsed.clone().unwrap()).unwrap();
                assert_eq!(parsed.parse_ok, expect.parse_ok, "{}", v.name);
                assert_eq!(parsed.config, hub_from_json(&expect.config), "{}", v.name);
                if expect.has_rotated_device_token {
                    assert_eq!(
                        parsed.rotated_device_token, expect.rotated_device_token,
                        "{}",
                        v.name
                    );
                } else {
                    assert!(parsed.rotated_device_token.is_none(), "{}", v.name);
                }
            }
            other => panic!("unknown response kind {other}"),
        }
    }
}

#[test]
fn device_protocol_setup_qr_vectors_match_cpp() {
    let raw = read_json("setup_qr.json");
    let vectors: Vec<SetupVector> = serde_json::from_str(&raw).expect("parse setup_qr.json");
    for v in &vectors {
        let mut parsed = SetupQrParsed::default();
        let fn_ok = parse_setup_qr_payload(&v.raw, &mut parsed);
        assert_eq!(fn_ok, v.fn_ok, "setup fnOk {}", v.name);
        assert_eq!(parsed.ok, v.parsed.ok, "{}", v.name);
        assert_eq!(parsed.ssid, v.parsed.ssid, "{}", v.name);
        assert_eq!(parsed.pass, v.parsed.pass, "{}", v.name);
        assert_eq!(parsed.url, v.parsed.url, "{}", v.name);
        assert_eq!(parsed.code, v.parsed.code, "{}", v.name);
    }
}

#[test]
fn device_protocol_api_error_vectors_match_cpp() {
    let raw = read_json("api_error.json");
    let vectors: Vec<ApiErrorVector> = serde_json::from_str(&raw).expect("parse api_error.json");
    for v in &vectors {
        let mut code = String::new();
        let fn_ok = parse_api_error_code(&v.body, &mut code);
        assert_eq!(fn_ok, v.fn_ok, "api_error fnOk {}", v.name);
        assert_eq!(code, v.code, "{}", v.name);
    }
}

#[test]
fn device_protocol_http_retry_vectors_match_cpp() {
    let raw = read_json("http_retry.json");
    let vectors: Vec<RetryVector> = serde_json::from_str(&raw).expect("parse http_retry.json");
    for v in &vectors {
        let got = http_failure_is_retryable(v.http_status, v.transport_ok);
        assert_eq!(
            got, v.retryable,
            "retry status={} transport_ok={}",
            v.http_status, v.transport_ok
        );
    }
}
