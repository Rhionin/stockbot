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

#[derive(Default)]
pub struct MockDisplay {
    pub last: DisplayBands,
    pub render_calls: u32,
}

impl DisplayHal for MockDisplay {
    fn begin(&mut self) {}

    fn render(&mut self, bands: &DisplayBands) {
        self.last = bands.clone();
        self.render_calls += 1;
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

#[derive(Default)]
pub struct MockClient {
    pub token: String,
    pub next_pair: HttpCallResult,
    pub pair_body: PairCompleteParsed,
    pub next_scan: HttpCallResult,
    pub scan_body: ParsedDeviceScanResponse,
    pub next_hb: HttpCallResult,
    pub hb_body: HeartbeatParsed,
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
        *out = self.pair_body.clone();
        self.next_pair.clone()
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
        *out = self.scan_body.clone();
        self.next_scan.clone()
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
        *out = self.scan_body.clone();
        self.next_scan.clone()
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
        *out = self.hb_body.clone();
        self.next_hb.clone()
    }
}
