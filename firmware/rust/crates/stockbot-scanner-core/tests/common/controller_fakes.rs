use std::cell::Cell;
use std::collections::VecDeque;
use std::rc::Rc;

use serde_json::Value;
use stockbot_scanner_core::{
    build_batch_scans_request_json, build_heartbeat_request_json, AudioCue, BacklightLevel,
    CaptivePortalHal, CaptivePortalSubmission, DeviceClient, DisplayBands, DisplayHal, FeedbackHal,
    HeartbeatParsed, HttpCallResult, NvsConfig, NvsStore, PairCompleteParsed,
    ParsedDeviceScanResponse, PowerHal, PowerStatus, RingScanRecord, ScanLine, ScannerHal,
    WifiStationHal,
};

#[derive(Default)]
pub struct MockScanner {
    pub one_d_calls: u32,
    pub one_d_enabled: bool,
    pub continuous: bool,
}

impl ScannerHal for MockScanner {
    fn begin(&mut self) {}

    fn poll(&mut self, _now_ms: u64, _out: &mut Vec<ScanLine>) {}

    fn enable_1d_only(&mut self, enabled: bool) {
        self.one_d_calls += 1;
        self.one_d_enabled = enabled;
    }

    fn set_continuous_mode(&mut self, enabled: bool) {
        self.continuous = enabled;
    }
}

pub struct MockDisplay {
    pub last: DisplayBands,
    /// Shared counter so tests can read render count while `ScannerController` holds `&mut MockDisplay`.
    pub render_calls: Rc<Cell<u32>>,
}

impl Default for MockDisplay {
    fn default() -> Self {
        Self {
            last: DisplayBands::default(),
            render_calls: Rc::new(Cell::new(0)),
        }
    }
}

impl DisplayHal for MockDisplay {
    fn begin(&mut self) {}

    fn render(&mut self, bands: &DisplayBands) {
        self.last = bands.clone();
        self.render_calls
            .set(self.render_calls.get().wrapping_add(1));
    }

    fn set_backlight(&mut self, _level: BacklightLevel) {}
}

#[derive(Default)]
pub struct MockFeedback {
    pub cues: Vec<AudioCue>,
}

impl FeedbackHal for MockFeedback {
    fn begin(&mut self) {}

    fn play(&mut self, cue: AudioCue) {
        self.cues.push(cue);
    }

    fn flash_led_for_scan(&mut self, _stock_in: bool) {}
}

#[derive(Default)]
pub struct MockPower {
    pub status: PowerStatus,
}

impl PowerHal for MockPower {
    fn begin(&mut self) {}

    fn read(&self) -> PowerStatus {
        self.status.clone()
    }

    fn set_grove_power(&mut self, _enabled: bool) {}

    fn enter_deep_sleep_wake_on_button(&mut self) {}
}

#[derive(Default)]
pub struct MockNvs {
    pub cfg: NvsConfig,
    pub factory_reset_calls: u32,
}

impl NvsStore for MockNvs {
    fn load(&mut self, out: &mut NvsConfig) -> bool {
        *out = self.cfg.clone();
        true
    }

    fn save(&mut self, cfg: &NvsConfig) -> bool {
        self.cfg = cfg.clone();
        true
    }

    fn clear_device_token(&mut self) {
        self.cfg.device_token.clear();
    }

    fn factory_reset(&mut self) {
        self.factory_reset_calls += 1;
        self.cfg = NvsConfig::default();
    }
}

#[derive(Default)]
pub struct MockPortal {
    pub running: bool,
    pub pending: CaptivePortalSubmission,
}

impl CaptivePortalHal for MockPortal {
    fn start(&mut self, _ap_suffix: &str) {
        self.running = true;
    }

    fn stop(&mut self) {
        self.running = false;
    }

    fn loop_portal(&mut self) {}

    fn take_submission(&mut self, out: &mut CaptivePortalSubmission) -> bool {
        if !self.pending.ready {
            return false;
        }
        *out = self.pending.clone();
        self.pending.ready = false;
        true
    }
}

#[derive(Default)]
pub struct MockWifi {
    pub last_ssid: String,
    pub connect_calls: u32,
    pub connected: bool,
}

impl WifiStationHal for MockWifi {
    fn begin(&mut self) {}

    fn connect(&mut self, ssid: &str, _pass: &str) {
        self.last_ssid = ssid.to_owned();
        self.connect_calls += 1;
        self.connected = !ssid.is_empty();
    }

    fn is_connected(&self) -> bool {
        self.connected
    }

    fn tick(&mut self) {}

    fn disconnect_station(&mut self) {
        self.connected = false;
        self.last_ssid.clear();
    }
}

#[derive(Debug, Clone)]
#[allow(dead_code)]
pub struct HttpRequestRecord {
    pub kind: String,
    pub at_ms: u64,
    pub http_status: i32,
    pub transport_ok: bool,
}

#[derive(Debug, Clone)]
pub struct MockHttpScriptEntry {
    pub result: HttpCallResult,
    pub pair_body: PairCompleteParsed,
    pub scan_body: ParsedDeviceScanResponse,
    pub hb_body: HeartbeatParsed,
}

impl Default for MockHttpScriptEntry {
    fn default() -> Self {
        Self {
            result: HttpCallResult {
                transport_ok: false,
                http_status: 0,
                body: String::new(),
            },
            pair_body: PairCompleteParsed::default(),
            scan_body: ParsedDeviceScanResponse::default(),
            hb_body: HeartbeatParsed::default(),
        }
    }
}

#[derive(Default)]
pub struct MockClient {
    pub token: String,
    pub next_pair: HttpCallResult,
    pub pair_body: PairCompleteParsed,
    pub next_scan: HttpCallResult,
    pub scan_body: ParsedDeviceScanResponse,
    pub next_hb: HttpCallResult,
    pub hb_body: HeartbeatParsed,
    pub pair_queue: VecDeque<MockHttpScriptEntry>,
    pub scan_queue: VecDeque<MockHttpScriptEntry>,
    pub hb_queue: VecDeque<MockHttpScriptEntry>,
    pub now_ms: u64,
    pub http_log: Vec<HttpRequestRecord>,
    pub last_send_barcode: String,
    pub last_send_scan_id: String,
    pub last_send_age_ms: u32,
    pub last_send_time_approx: bool,
    pub send_order: Vec<String>,
    pub last_flush_batch: Vec<RingScanRecord>,
    pub last_flush_boot: String,
    pub last_flush_now_ms: u64,
    pub last_flush_body: String,
    pub last_heartbeat_body: String,
    pub send_heartbeat_calls: u32,
}

impl MockClient {
    fn log_http(&mut self, kind: &str, res: &HttpCallResult) {
        self.http_log.push(HttpRequestRecord {
            kind: kind.to_owned(),
            at_ms: self.now_ms,
            http_status: res.http_status,
            transport_ok: res.transport_ok,
        });
    }

    fn pop_pair(&mut self) -> MockHttpScriptEntry {
        if let Some(e) = self.pair_queue.pop_front() {
            return e;
        }
        MockHttpScriptEntry {
            result: self.next_pair.clone(),
            pair_body: self.pair_body.clone(),
            ..Default::default()
        }
    }

    fn pop_scan(&mut self) -> MockHttpScriptEntry {
        if let Some(e) = self.scan_queue.pop_front() {
            return e;
        }
        MockHttpScriptEntry {
            result: self.next_scan.clone(),
            scan_body: self.scan_body.clone(),
            ..Default::default()
        }
    }

    fn pop_hb(&mut self) -> MockHttpScriptEntry {
        if let Some(e) = self.hb_queue.pop_front() {
            return e;
        }
        MockHttpScriptEntry {
            result: self.next_hb.clone(),
            hb_body: self.hb_body.clone(),
            ..Default::default()
        }
    }

    #[allow(dead_code)]
    pub fn push_http_queue(&mut self, step: &Value) {
        for key in ["pair", "scan", "flush", "heartbeat"] {
            let Some(arr) = step.get(key).and_then(|v| v.as_array()) else {
                continue;
            };
            for item in arr {
                let entry = entry_from_value(item);
                match key {
                    "pair" => self.pair_queue.push_back(entry),
                    "scan" | "flush" => self.scan_queue.push_back(entry),
                    "heartbeat" => self.hb_queue.push_back(entry),
                    _ => {}
                }
            }
        }
    }
}

#[allow(clippy::field_reassign_with_default)]
fn entry_from_value(v: &Value) -> MockHttpScriptEntry {
    let transport_ok = v
        .get("transport_ok")
        .and_then(|x| x.as_bool())
        .unwrap_or(false);
    let http_status = v.get("http_status").and_then(|x| x.as_i64()).unwrap_or(0) as i32;
    let body = v
        .get("body")
        .and_then(|x| x.as_str())
        .unwrap_or("")
        .to_owned();
    let mut scan_body = ParsedDeviceScanResponse::default();
    scan_body.parse_ok = true;
    if let Some(b) = v.get("unauthorized").and_then(|x| x.as_bool()) {
        scan_body.unauthorized = b;
    }
    if let Some(s) = v.get("status").and_then(|x| x.as_str()) {
        scan_body.status = s.to_owned();
    }
    if let Some(s) = v.get("scan_status").and_then(|x| x.as_str()) {
        scan_body.scan_status = s.to_owned();
    }
    if let Some(b) = v.get("known").and_then(|x| x.as_bool()) {
        scan_body.known = b;
    }
    if let Some(s) = v.get("product_name").and_then(|x| x.as_str()) {
        scan_body.product_name = s.to_owned();
    }
    if let Some(n) = v.get("on_hand").and_then(|x| x.as_i64()) {
        scan_body.on_hand = n as i32;
    }
    let mut pair_body = PairCompleteParsed::default();
    pair_body.device_token = v
        .get("device_token")
        .and_then(|x| x.as_str())
        .unwrap_or("sbd_scenario")
        .to_owned();
    let mut hb_body = HeartbeatParsed::default();
    hb_body.parse_ok = true;
    if let Some(n) = v.get("config_version").and_then(|x| x.as_i64()) {
        hb_body.config.config_version = n as u32;
    }
    if let Some(s) = v.get("default_direction").and_then(|x| x.as_str()) {
        hb_body.config.default_direction = s.to_owned();
    }
    MockHttpScriptEntry {
        result: HttpCallResult {
            transport_ok,
            http_status,
            body,
        },
        pair_body,
        scan_body,
        hb_body,
    }
}

impl DeviceClient for MockClient {
    fn set_server_url(&mut self, _url: &str) {}

    fn set_device_token(&mut self, token: &str) {
        self.token = token.to_owned();
    }

    fn complete_pairing(
        &mut self,
        _code: &str,
        _label: &str,
        _hub_version: &str,
        _boot_id: &str,
        out: &mut PairCompleteParsed,
    ) -> HttpCallResult {
        let scripted = self.pop_pair();
        *out = scripted.pair_body;
        self.log_http("pair", &scripted.result);
        scripted.result
    }

    fn send_scan(
        &mut self,
        rec: &RingScanRecord,
        _boot_id: &str,
        age_ms: u32,
        time_approx: bool,
        out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        self.last_send_barcode = stockbot_scanner_core::c_str_from_fixed(&rec.barcode);
        self.last_send_scan_id = stockbot_scanner_core::c_str_from_fixed(&rec.client_scan_id);
        self.last_send_age_ms = age_ms;
        self.last_send_time_approx = time_approx;
        self.send_order
            .push(stockbot_scanner_core::c_str_from_fixed(&rec.barcode));
        let scripted = self.pop_scan();
        *out = scripted.scan_body;
        self.log_http("scan", &scripted.result);
        scripted.result
    }

    fn flush_batch(
        &mut self,
        batch: &[RingScanRecord],
        boot_id: &str,
        now_ms: u64,
        out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        self.last_flush_batch = batch.to_vec();
        self.last_flush_boot = boot_id.to_owned();
        self.last_flush_now_ms = now_ms;
        self.last_flush_body = build_batch_scans_request_json(batch, boot_id, now_ms);
        let scripted = self.pop_scan();
        *out = scripted.scan_body;
        self.log_http("flush", &scripted.result);
        scripted.result
    }

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
        self.send_heartbeat_calls += 1;
        self.last_heartbeat_body = build_heartbeat_request_json(
            hub_version,
            boot_id,
            uptime_s,
            queued,
            battery_pct,
            charging,
            true,
        );
        let scripted = self.pop_hb();
        *out = scripted.hb_body;
        self.log_http("heartbeat", &scripted.result);
        scripted.result
    }
}

/// `ScannerController` holds `&mut` to the client; tests tune responses via [`Rc<RefCell<MockClient>>`].
pub struct SharedMockClient(pub Rc<std::cell::RefCell<MockClient>>);

impl DeviceClient for SharedMockClient {
    fn set_server_url(&mut self, url: &str) {
        self.0.borrow_mut().set_server_url(url);
    }

    fn set_device_token(&mut self, token: &str) {
        self.0.borrow_mut().set_device_token(token);
    }

    fn complete_pairing(
        &mut self,
        code: &str,
        label: &str,
        hub_version: &str,
        boot_id: &str,
        out: &mut PairCompleteParsed,
    ) -> HttpCallResult {
        self.0
            .borrow_mut()
            .complete_pairing(code, label, hub_version, boot_id, out)
    }

    fn send_scan(
        &mut self,
        rec: &RingScanRecord,
        boot_id: &str,
        age_ms: u32,
        time_approx: bool,
        out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        self.0
            .borrow_mut()
            .send_scan(rec, boot_id, age_ms, time_approx, out)
    }

    fn flush_batch(
        &mut self,
        batch: &[RingScanRecord],
        boot_id: &str,
        now_ms: u64,
        out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        self.0.borrow_mut().flush_batch(batch, boot_id, now_ms, out)
    }

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
        self.0.borrow_mut().send_heartbeat(
            hub_version,
            boot_id,
            uptime_s,
            queued,
            battery_pct,
            charging,
            out,
        )
    }
}

pub struct SharedMockNvs(pub Rc<std::cell::RefCell<MockNvs>>);

impl NvsStore for SharedMockNvs {
    fn load(&mut self, out: &mut NvsConfig) -> bool {
        self.0.borrow_mut().load(out)
    }

    fn save(&mut self, cfg: &NvsConfig) -> bool {
        self.0.borrow_mut().save(cfg)
    }

    fn clear_device_token(&mut self) {
        self.0.borrow_mut().clear_device_token();
    }

    fn factory_reset(&mut self) {
        self.0.borrow_mut().factory_reset();
    }
}

pub struct SharedMockPortal(pub Rc<std::cell::RefCell<MockPortal>>);

impl CaptivePortalHal for SharedMockPortal {
    fn start(&mut self, ap_suffix: &str) {
        self.0.borrow_mut().start(ap_suffix);
    }

    fn stop(&mut self) {
        self.0.borrow_mut().stop();
    }

    fn loop_portal(&mut self) {
        self.0.borrow_mut().loop_portal();
    }

    fn take_submission(&mut self, out: &mut CaptivePortalSubmission) -> bool {
        self.0.borrow_mut().take_submission(out)
    }
}

pub struct SharedMockPower(pub Rc<std::cell::RefCell<MockPower>>);

impl PowerHal for SharedMockPower {
    fn begin(&mut self) {
        self.0.borrow_mut().begin();
    }

    fn read(&self) -> PowerStatus {
        self.0.borrow().read()
    }

    fn set_grove_power(&mut self, enabled: bool) {
        self.0.borrow_mut().set_grove_power(enabled);
    }

    fn enter_deep_sleep_wake_on_button(&mut self) {
        self.0.borrow_mut().enter_deep_sleep_wake_on_button();
    }
}

pub struct SharedMockDisplay(pub Rc<std::cell::RefCell<MockDisplay>>);

impl DisplayHal for SharedMockDisplay {
    fn begin(&mut self) {
        self.0.borrow_mut().begin();
    }

    fn render(&mut self, bands: &DisplayBands) {
        self.0.borrow_mut().render(bands);
    }

    fn set_backlight(&mut self, level: BacklightLevel) {
        self.0.borrow_mut().set_backlight(level);
    }
}

/// Same pattern as [`SharedMockClient`] for reading or clearing cues mid-controller session.
pub struct SharedMockFeedback(pub Rc<std::cell::RefCell<MockFeedback>>);

impl FeedbackHal for SharedMockFeedback {
    fn begin(&mut self) {
        self.0.borrow_mut().begin();
    }

    fn play(&mut self, cue: AudioCue) {
        self.0.borrow_mut().play(cue);
    }

    fn flash_led_for_scan(&mut self, stock_in: bool) {
        self.0.borrow_mut().flash_led_for_scan(stock_in);
    }
}

/// Same pattern as [`SharedMockClient`] for toggling `connected` mid-controller session.
pub struct SharedMockWifi(pub Rc<std::cell::RefCell<MockWifi>>);

impl WifiStationHal for SharedMockWifi {
    fn begin(&mut self) {
        self.0.borrow_mut().begin();
    }

    fn connect(&mut self, ssid: &str, pass: &str) {
        self.0.borrow_mut().connect(ssid, pass);
    }

    fn is_connected(&self) -> bool {
        self.0.borrow().is_connected()
    }

    fn tick(&mut self) {
        self.0.borrow_mut().tick();
    }

    fn disconnect_station(&mut self) {
        self.0.borrow_mut().disconnect_station();
    }
}
