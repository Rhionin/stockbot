//! Device HTTP JSON builders and parsers (parity with `firmware/src/core/protocol_json.cpp`).

use crate::barcode::Direction;
use crate::ring_buffer::{c_str_from_fixed, ring_scan_age_ms, RingScanRecord};
use crate::text::trim_ascii;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct DeviceHubConfigParsed {
    pub label: String,
    pub default_direction: String,
    pub inactivity_revert_seconds: u32,
    pub commit_mode: String,
    pub config_version: u32,
}

impl DeviceHubConfigParsed {
    pub fn new() -> Self {
        Self {
            default_direction: "stock_out".to_owned(),
            inactivity_revert_seconds: 300,
            commit_mode: "queue".to_owned(),
            ..Default::default()
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PairCompleteParsed {
    pub device_id: String,
    pub device_token: String,
    pub household_id: String,
    pub household_name: String,
    pub config: DeviceHubConfigParsed,
    pub heartbeat_seconds: u32,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ParsedDeviceScanResponse {
    pub id: String,
    pub status: String,
    pub scan_status: String,
    pub known: bool,
    pub product_name_is_null: bool,
    pub product_name: String,
    pub barcode: String,
    pub direction: String,
    pub queued_count: i32,
    pub delta: i32,
    pub on_hand: i32,
    pub backfilled: bool,
    pub code: String,
    pub unauthorized: bool,
    pub parse_ok: bool,
}

impl Default for ParsedDeviceScanResponse {
    fn default() -> Self {
        Self {
            id: String::new(),
            status: String::new(),
            scan_status: String::new(),
            known: false,
            product_name_is_null: true,
            product_name: String::new(),
            barcode: String::new(),
            direction: String::new(),
            queued_count: 0,
            delta: 0,
            on_hand: 0,
            backfilled: false,
            code: String::new(),
            unauthorized: false,
            parse_ok: false,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HeartbeatParsed {
    pub config: DeviceHubConfigParsed,
    pub rotated_device_token: Option<String>,
    pub parse_ok: bool,
}

impl Default for HeartbeatParsed {
    fn default() -> Self {
        Self {
            config: DeviceHubConfigParsed::new(),
            rotated_device_token: None,
            parse_ok: false,
        }
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SetupQrParsed {
    pub ok: bool,
    pub ssid: String,
    pub pass: String,
    pub url: String,
    pub code: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct BatchScanResultItem {
    pub id: String,
    pub status: String,
}

pub fn http_failure_is_retryable(http_status: i32, transport_ok: bool) -> bool {
    if !transport_ok {
        return true;
    }
    http_status >= 500 || http_status == 429
}

pub fn build_pair_complete_request_json(
    code: &str,
    label: &str,
    hub_version: &str,
    boot_id: &str,
) -> String {
    format!(
        "{{\"code\":\"{}\",\"label\":\"{}\",\"hub\":{{\"kind\":\"esp32_scanner\",\"version\":\"{}\",\"bootId\":\"{}\",\"label\":\"{}\"}}}}",
        json_escape(code),
        json_escape(label),
        json_escape(hub_version),
        json_escape(boot_id),
        json_escape(label),
    )
}

pub fn parse_pair_complete_response_json(
    http_status: i32,
    body: &str,
    out: &mut PairCompleteParsed,
) -> bool {
    *out = PairCompleteParsed {
        config: DeviceHubConfigParsed::new(),
        heartbeat_seconds: 60,
        ..Default::default()
    };
    if http_status == 401 {
        return false;
    }
    if http_status != 201 && http_status != 200 {
        return false;
    }
    if let Some(device_id) = json_string_at_key(body, "deviceId", 0, body.len()) {
        out.device_id = device_id;
    }
    if let Some(token) = json_string_at_key(body, "token", 0, body.len()) {
        out.device_token = token;
    }
    if let Some(household) = json_string_at_key(body, "householdId", 0, body.len()) {
        out.household_id = household;
    }
    if let Some(household_name) = json_string_at_key(body, "householdName", 0, body.len()) {
        out.household_name = household_name;
    }
    if let Some(hb) = json_int_at_key(body, "heartbeatSeconds", 0, body.len()) {
        out.heartbeat_seconds = hb as u32;
    }
    if let Some(cfg_json) = json_object_at_key(body, "config") {
        parse_hub_config_object(&cfg_json, &mut out.config);
    }
    !out.device_token.is_empty()
}

pub fn build_single_scan_request_json(
    record: &RingScanRecord,
    boot_id: &str,
    age_ms: u64,
    time_approx: bool,
) -> String {
    let direction = if record.direction == 1 {
        Direction::StockIn
    } else {
        Direction::StockOut
    };
    let id = c_str_from_fixed(&record.client_scan_id);
    let barcode = c_str_from_fixed(&record.barcode);
    let mut out = format!(
        "{{\"id\":\"{}\",\"barcode\":\"{}\",\"direction\":\"{}\",\"unitCount\":1,\"ageMs\":{},\"bootId\":\"{}\"",
        json_escape(&id),
        json_escape(&barcode),
        direction_to_string(direction),
        age_ms,
        json_escape(boot_id),
    );
    if time_approx {
        out.push_str(",\"timeApprox\":true");
    }
    out.push('}');
    out
}

pub fn build_batch_scans_request_json(
    records: &[RingScanRecord],
    boot_id: &str,
    now_ms: u64,
) -> String {
    let mut parts = Vec::with_capacity(records.len());
    for rec in records {
        let age_ms = u64::from(ring_scan_age_ms(rec, now_ms));
        let time_approx = rec.scanned_ms == 0;
        parts.push(build_single_scan_request_json(
            rec,
            boot_id,
            age_ms,
            time_approx,
        ));
    }
    format!("{{\"scans\":[{}]}}", parts.join(","))
}

pub fn parse_device_scan_response_json(
    http_status: i32,
    body: &str,
    out: &mut ParsedDeviceScanResponse,
) -> bool {
    *out = ParsedDeviceScanResponse::default();
    out.unauthorized = http_status == 401;
    if out.unauthorized {
        return true;
    }
    if !(200..300).contains(&http_status) {
        return false;
    }

    let result_spans = json_result_object_spans(body);
    if let Some((start, end)) = result_spans.first() {
        parse_scan_result_object(body, *start, *end, out);
    } else if let Some((start, end)) = json_object_span_at(body, 0) {
        parse_scan_result_object(body, start, end, out);
    }

    out.parse_ok = !out.status.is_empty() || !out.id.is_empty();
    out.parse_ok
}

pub fn parse_device_scan_batch_response_json(
    http_status: i32,
    body: &str,
    out: &mut Vec<BatchScanResultItem>,
) -> bool {
    out.clear();
    if http_status == 401 {
        return true;
    }
    if !(200..300).contains(&http_status) {
        return false;
    }
    let spans = json_result_object_spans(body);
    if spans.is_empty() {
        let mut single = ParsedDeviceScanResponse::default();
        if !parse_device_scan_response_json(http_status, body, &mut single) {
            return false;
        }
        if !single.id.is_empty() || !single.status.is_empty() {
            out.push(BatchScanResultItem {
                id: single.id,
                status: single.status,
            });
        }
        return true;
    }
    for (start, end) in spans {
        let mut item = BatchScanResultItem {
            id: String::new(),
            status: String::new(),
        };
        if let Some(id) = json_string_at_key(body, "id", start, end) {
            item.id = id;
        }
        if let Some(status) = json_string_at_key(body, "status", start, end) {
            item.status = status;
        }
        out.push(item);
    }
    true
}

pub fn build_heartbeat_request_json(
    hub_version: &str,
    boot_id: &str,
    uptime_s: u32,
    queued: usize,
    battery_pct: i32,
    charging: bool,
    clock_synced: bool,
) -> String {
    let mut out = format!(
        "{{\"version\":\"{}\",\"bootId\":\"{}\",\"uptimeS\":{},\"queued\":{},\"droppedCount\":0,\"clockSynced\":{}",
        json_escape(hub_version),
        json_escape(boot_id),
        uptime_s,
        queued,
        if clock_synced { "true" } else { "false" },
    );
    if battery_pct >= 0 {
        out.push_str(&format!(
            ",\"batteryPct\":{},\"charging\":{}",
            battery_pct,
            if charging { "true" } else { "false" }
        ));
    }
    out.push('}');
    out
}

pub fn parse_heartbeat_response_json(
    http_status: i32,
    body: &str,
    out: &mut HeartbeatParsed,
) -> bool {
    *out = HeartbeatParsed::default();
    if http_status == 401 {
        out.parse_ok = true;
        return true;
    }
    if !(200..300).contains(&http_status) {
        return false;
    }
    if let Some(cfg_json) = json_object_at_key(body, "config") {
        parse_hub_config_object(&cfg_json, &mut out.config);
    }
    if let Some(token) = json_string_at_key(body, "token", 0, body.len()) {
        out.rotated_device_token = Some(token);
    }
    if let Some(rotate) = json_bool_at_key(body, "rotateToken", 0, body.len()) {
        if rotate && out.rotated_device_token.is_none() {
            out.rotated_device_token = Some(String::new());
        }
    }
    out.parse_ok = true;
    true
}

pub fn parse_setup_qr_payload(raw: &str, out: &mut SetupQrParsed) -> bool {
    *out = SetupQrParsed::default();
    let trimmed = trim_ascii(raw);
    if trimmed.is_empty() || !trimmed.starts_with('{') {
        return false;
    }
    if let Some(ssid) = json_string_at_key(&trimmed, "ssid", 0, trimmed.len()) {
        out.ssid = ssid;
    } else if let Some(s) = json_string_at_key(&trimmed, "s", 0, trimmed.len()) {
        out.ssid = s;
    }
    if let Some(pass) = json_string_at_key(&trimmed, "pass", 0, trimmed.len()) {
        out.pass = pass;
    } else if let Some(w) = json_string_at_key(&trimmed, "w", 0, trimmed.len()) {
        out.pass = w;
    }
    if let Some(url) = json_string_at_key(&trimmed, "url", 0, trimmed.len()) {
        out.url = url;
    } else if let Some(u) = json_string_at_key(&trimmed, "u", 0, trimmed.len()) {
        out.url = u;
    }
    if let Some(code) = json_string_at_key(&trimmed, "code", 0, trimmed.len()) {
        out.code = code;
    } else if let Some(p) = json_string_at_key(&trimmed, "p", 0, trimmed.len()) {
        out.code = p;
    }
    if let Some(v) = json_int_at_key(&trimmed, "v", 0, trimmed.len()) {
        out.ok = v == 1 && !out.ssid.is_empty();
    } else {
        out.ok = !out.ssid.is_empty();
    }
    out.ok
}

pub fn parse_api_error_code(body: &str, code_out: &mut String) -> bool {
    code_out.clear();
    if let Some(code) = json_string_at_key(body, "code", 0, body.len()) {
        *code_out = code;
        return true;
    }
    false
}

fn json_escape(in_str: &str) -> String {
    let mut out = String::with_capacity(in_str.len() + 8);
    for ch in in_str.chars() {
        match ch {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            _ => out.push(ch),
        }
    }
    out
}

fn direction_to_string(direction: Direction) -> &'static str {
    if direction == Direction::StockIn {
        "stock_in"
    } else {
        "stock_out"
    }
}

fn json_string_at_key(
    body: &str,
    key: &str,
    search_start: usize,
    search_end: usize,
) -> Option<String> {
    let needle = format!("\"{key}\"");
    let mut pos = body[search_start..search_end].find(&needle)? + search_start;
    if pos >= search_end {
        return None;
    }
    pos = body[pos..search_end].find(':')? + pos;
    pos += 1;
    while pos < search_end {
        let b = body.as_bytes()[pos];
        if !b.is_ascii_whitespace() {
            break;
        }
        pos += 1;
    }
    if pos >= search_end {
        return None;
    }
    if body[pos..search_end].starts_with("null") {
        return Some(String::new());
    }
    if body.as_bytes()[pos] != b'"' {
        return None;
    }
    pos += 1;
    let mut out = Vec::new();
    let bytes = body.as_bytes();
    while pos < search_end && bytes[pos] != b'"' {
        if bytes[pos] == b'\\' && pos + 1 < search_end {
            out.push(bytes[pos + 1]);
            pos += 2;
            continue;
        }
        out.push(bytes[pos]);
        pos += 1;
    }
    Some(String::from_utf8_lossy(&out).into_owned())
}

fn json_int_at_key(body: &str, key: &str, search_start: usize, search_end: usize) -> Option<i64> {
    let needle = format!("\"{key}\"");
    let mut pos = body[search_start..search_end].find(&needle)? + search_start;
    if pos >= search_end {
        return None;
    }
    pos = body[pos..search_end].find(':')? + pos;
    pos += 1;
    while pos < search_end {
        let b = body.as_bytes()[pos];
        if !b.is_ascii_whitespace() {
            break;
        }
        pos += 1;
    }
    let start = pos;
    while pos < search_end {
        let b = body.as_bytes()[pos];
        if !b.is_ascii_digit() && b != b'-' {
            break;
        }
        pos += 1;
    }
    if pos == start {
        return None;
    }
    body[start..pos].parse().ok()
}

fn json_bool_at_key(body: &str, key: &str, search_start: usize, search_end: usize) -> Option<bool> {
    let needle = format!("\"{key}\"");
    let mut pos = body[search_start..search_end].find(&needle)? + search_start;
    if pos >= search_end {
        return None;
    }
    pos = body[pos..search_end].find(':')? + pos;
    pos += 1;
    while pos < search_end {
        let b = body.as_bytes()[pos];
        if !b.is_ascii_whitespace() {
            break;
        }
        pos += 1;
    }
    if pos + 4 <= search_end && body[pos..].starts_with("true") {
        return Some(true);
    }
    if pos + 5 <= search_end && body[pos..].starts_with("false") {
        return Some(false);
    }
    None
}

fn json_object_at_key(body: &str, key: &str) -> Option<String> {
    let needle = format!("\"{key}\"");
    let mut pos = body.find(&needle)?;
    pos = body[pos..].find('{')? + pos;
    let mut depth = 0;
    for (offset, byte) in body.as_bytes().iter().enumerate().skip(pos) {
        match *byte {
            b'{' => depth += 1,
            b'}' => {
                depth -= 1;
                if depth == 0 {
                    return Some(body[pos..=offset].to_owned());
                }
            }
            _ => {}
        }
    }
    None
}

fn parse_hub_config_object(config_json: &str, cfg: &mut DeviceHubConfigParsed) {
    let len = config_json.len();
    if let Some(label) = json_string_at_key(config_json, "label", 0, len) {
        cfg.label = label;
    }
    if let Some(dir) = json_string_at_key(config_json, "defaultDirection", 0, len) {
        cfg.default_direction = dir;
    }
    if let Some(sec) = json_int_at_key(config_json, "inactivityRevertSeconds", 0, len) {
        cfg.inactivity_revert_seconds = sec as u32;
    }
    if let Some(idle) = json_int_at_key(config_json, "idleSeconds", 0, len) {
        if cfg.inactivity_revert_seconds == 300 {
            cfg.inactivity_revert_seconds = idle as u32;
        }
    }
    if let Some(mode) = json_string_at_key(config_json, "commitMode", 0, len) {
        cfg.commit_mode = mode;
    }
    if let Some(ver) = json_int_at_key(config_json, "configVersion", 0, len) {
        cfg.config_version = ver as u32;
    }
}

fn json_object_span_at(body: &str, from: usize) -> Option<(usize, usize)> {
    let obj_start = body[from..].find('{')? + from;
    let mut depth = 0;
    for (offset, byte) in body.as_bytes().iter().enumerate().skip(obj_start) {
        match *byte {
            b'{' => depth += 1,
            b'}' => {
                depth -= 1;
                if depth == 0 {
                    return Some((obj_start, offset + 1));
                }
            }
            _ => {}
        }
    }
    None
}

fn json_result_object_spans(body: &str) -> Vec<(usize, usize)> {
    let mut spans = Vec::new();
    let needle = "\"results\"";
    let results_pos = match body.find(needle) {
        Some(p) => p,
        None => return spans,
    };
    let array_start = match body[results_pos..].find('[') {
        Some(p) => results_pos + p,
        None => return spans,
    };
    let mut pos = array_start + 1;
    while pos < body.len() {
        while pos < body.len() {
            let b = body.as_bytes()[pos];
            if b == b' ' || b == b',' || b == b'\n' || b == b'\r' {
                pos += 1;
            } else {
                break;
            }
        }
        if pos >= body.len() || body.as_bytes()[pos] == b']' {
            break;
        }
        if let Some(span) = json_object_span_at(body, pos) {
            spans.push(span);
            pos = span.1;
        } else {
            break;
        }
    }
    spans
}

fn parse_enriched_scan_fields(
    body: &str,
    out: &mut ParsedDeviceScanResponse,
    obj_start: usize,
    obj_end: usize,
) {
    if let Some(scan_status) = json_string_at_key(body, "scanStatus", obj_start, obj_end) {
        out.scan_status = scan_status;
    }
    if let Some(known) = json_bool_at_key(body, "known", obj_start, obj_end) {
        out.known = known;
    }
    let slice = &body[obj_start..obj_end];
    let null_needle = "\"productName\":null";
    if let Some(p) = slice.find(null_needle) {
        if obj_start + p < obj_end {
            out.product_name_is_null = true;
        }
    } else if let Some(name) = json_string_at_key(body, "productName", obj_start, obj_end) {
        out.product_name_is_null = false;
        out.product_name = name;
    }
    if let Some(barcode) = json_string_at_key(body, "barcode", obj_start, obj_end) {
        out.barcode = barcode;
    }
    if let Some(direction) = json_string_at_key(body, "direction", obj_start, obj_end) {
        out.direction = direction;
    }
    if let Some(queued) = json_int_at_key(body, "queuedCount", obj_start, obj_end) {
        out.queued_count = queued as i32;
    }
    if let Some(delta) = json_int_at_key(body, "delta", obj_start, obj_end) {
        out.delta = delta as i32;
    }
    if let Some(on_hand) = json_int_at_key(body, "onHand", obj_start, obj_end) {
        out.on_hand = on_hand as i32;
    }
    if let Some(backfilled) = json_bool_at_key(body, "backfilled", obj_start, obj_end) {
        out.backfilled = backfilled;
    }
    if let Some(code) = json_string_at_key(body, "code", obj_start, obj_end) {
        out.code = code;
    }
}

fn parse_scan_result_object(
    body: &str,
    obj_start: usize,
    obj_end: usize,
    out: &mut ParsedDeviceScanResponse,
) {
    if let Some(id) = json_string_at_key(body, "id", obj_start, obj_end) {
        out.id = id;
    }
    if let Some(status) = json_string_at_key(body, "status", obj_start, obj_end) {
        out.status = status;
    }
    parse_enriched_scan_fields(body, out, obj_start, obj_end);
}
