//! Device API client on `EspHttpConnection`: one new connection per request.

use std::time::Duration;

use esp_idf_svc::http::client::{Configuration, EspHttpConnection};
use esp_idf_svc::http::Method;
use esp_idf_svc::sys::EspError;
use stockbot_scanner_core::{
    build_batch_scans_request_json, build_heartbeat_request_json, build_pair_complete_request_json,
    build_single_scan_request_json, parse_device_scan_response_json, parse_heartbeat_response_json,
    parse_pair_complete_response_json, transport_for_url, DeviceClient, HeartbeatParsed,
    HttpCallResult, PairCompleteParsed, ParsedDeviceScanResponse, RingScanRecord, Transport,
};

const PAIR_COMPLETE_PATH: &str = "/api/devices/pair/complete";
const SCANS_PATH: &str = "/api/device/scans";
const HEARTBEAT_PATH: &str = "/api/device/heartbeat";
const ROTATE_TOKEN_PATH: &str = "/api/device/token/rotate";
const REQUEST_TIMEOUT: Duration = Duration::from_secs(10);
const MAX_RESPONSE_BYTES: usize = 16 * 1024;

#[derive(Default)]
pub struct EspDeviceClient {
    server_url: String,
    device_token: String,
}

impl EspDeviceClient {
    pub fn new() -> Self {
        Self::default()
    }

    /// Posts a JSON body. Pairing is the only call without the bearer token.
    fn post_json(&self, path: &str, bearer: bool, body: &str) -> HttpCallResult {
        let url = format!("{}{}", self.server_url.trim_end_matches('/'), path);
        let Some(transport) = transport_for_url(&url) else {
            log::warn!("http: server URL must start with https:// or http://; request skipped");
            return HttpCallResult::default();
        };
        let configuration = Configuration {
            timeout: Some(REQUEST_TIMEOUT),
            crt_bundle_attach: match transport {
                Transport::VerifiedTls => Some(esp_idf_svc::sys::esp_crt_bundle_attach),
                Transport::PlainHttp => None,
            },
            ..Default::default()
        };
        let authorization = format!("Bearer {}", self.device_token);
        match exchange(&configuration, &url, body, bearer.then_some(&authorization)) {
            Ok(result) => result,
            Err(err) => {
                log::warn!("http: request to {path} failed: {err}");
                HttpCallResult::default()
            }
        }
    }
}

fn exchange(
    configuration: &Configuration,
    url: &str,
    body: &str,
    authorization: Option<&String>,
) -> Result<HttpCallResult, EspError> {
    let mut connection = EspHttpConnection::new(configuration)?;
    let content_length = body.len().to_string();
    let mut headers = vec![
        ("Content-Type", "application/json"),
        ("Content-Length", content_length.as_str()),
    ];
    if let Some(value) = authorization {
        headers.push(("Authorization", value.as_str()));
    }
    connection.initiate_request(Method::Post, url, &headers)?;
    connection.write_all(body.as_bytes())?;
    connection.initiate_response()?;

    let http_status = i32::from(connection.status());
    let retry_after = connection.header("Retry-After").map(str::to_owned);
    let mut bytes = Vec::new();
    let mut chunk = [0u8; 512];
    while bytes.len() < MAX_RESPONSE_BYTES {
        let read = connection.read(&mut chunk)?;
        if read == 0 {
            break;
        }
        bytes.extend_from_slice(&chunk[..read]);
    }
    bytes.truncate(MAX_RESPONSE_BYTES);
    Ok(HttpCallResult {
        transport_ok: true,
        http_status,
        body: String::from_utf8_lossy(&bytes).into_owned(),
        retry_after,
    })
}

impl DeviceClient for EspDeviceClient {
    fn set_server_url(&mut self, url: &str) {
        self.server_url = url.to_owned();
    }

    fn set_device_token(&mut self, token: &str) {
        self.device_token = token.to_owned();
    }

    fn complete_pairing(
        &mut self,
        code: &str,
        label: &str,
        hub_version: &str,
        boot_id: &str,
        out: &mut PairCompleteParsed,
    ) -> HttpCallResult {
        let body = build_pair_complete_request_json(code, label, hub_version, boot_id);
        let result = self.post_json(PAIR_COMPLETE_PATH, false, &body);
        *out = PairCompleteParsed::default();
        if result.transport_ok {
            parse_pair_complete_response_json(result.http_status, &result.body, out);
        }
        result
    }

    fn send_scan(
        &mut self,
        record: &RingScanRecord,
        boot_id: &str,
        age_ms: u32,
        time_approx: bool,
        out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        let body = build_single_scan_request_json(record, boot_id, u64::from(age_ms), time_approx);
        let result = self.post_json(SCANS_PATH, true, &body);
        *out = ParsedDeviceScanResponse::default();
        if result.transport_ok {
            parse_device_scan_response_json(result.http_status, &result.body, out);
        }
        result
    }

    fn flush_batch(
        &mut self,
        batch: &[RingScanRecord],
        boot_id: &str,
        now_ms: u64,
        last_out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        let body = build_batch_scans_request_json(batch, boot_id, now_ms);
        let result = self.post_json(SCANS_PATH, true, &body);
        // The controller reads the per-scan results straight from the response body.
        *last_out = ParsedDeviceScanResponse::default();
        result
    }

    #[allow(clippy::too_many_arguments)]
    fn send_heartbeat(
        &mut self,
        hub_version: &str,
        boot_id: &str,
        uptime_s: u32,
        queued: usize,
        battery_pct: i32,
        charging: bool,
        out: &mut HeartbeatParsed,
    ) -> HttpCallResult {
        // No time source yet (SNTP is not wired), so the clock is reported as not synced.
        let body = build_heartbeat_request_json(
            hub_version,
            boot_id,
            uptime_s,
            queued,
            battery_pct,
            charging,
            false,
        );
        let result = self.post_json(HEARTBEAT_PATH, true, &body);
        *out = HeartbeatParsed::default();
        if result.transport_ok {
            parse_heartbeat_response_json(result.http_status, &result.body, out);
        }
        result
    }

    fn rotate_token(&mut self) -> HttpCallResult {
        // The server reads no body; `exchange` sends `Content-Length: 0` for the empty string.
        self.post_json(ROTATE_TOKEN_PATH, true, "")
    }
}
