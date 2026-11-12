//! HAL and client port traits for [`crate::scanner_controller::ScannerController`].

use crate::display_model::{AudioCue, DisplayBands};
use crate::nvs_config::NvsConfig;
use crate::protocol_json::{HeartbeatParsed, PairCompleteParsed, ParsedDeviceScanResponse};
use crate::ring_buffer::RingScanRecord;
use crate::scanner_state::BacklightLevel;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ScanLine {
    pub barcode: String,
    pub timestamp_ms: u64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct HttpCallResult {
    pub transport_ok: bool,
    pub http_status: i32,
    pub body: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct PowerStatus {
    pub vbus_present: bool,
    pub battery_pct: Option<i32>,
    pub charging: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CaptivePortalSubmission {
    pub ready: bool,
    pub wifi_ssid: String,
    pub wifi_pass: String,
    pub server_url: String,
    pub pairing_code: String,
}

pub trait ScannerHal {
    fn begin(&mut self);
    fn poll(&mut self, now_ms: u64, out: &mut Vec<ScanLine>);
    fn enable_1d_only(&mut self, enabled: bool);
    fn set_continuous_mode(&mut self, enabled: bool);
}

pub trait DisplayHal {
    fn begin(&mut self);
    fn render(&mut self, bands: &DisplayBands);
    fn set_backlight(&mut self, level: BacklightLevel);
}

pub trait FeedbackHal {
    fn begin(&mut self);
    fn play(&mut self, cue: AudioCue);
    fn flash_led_for_scan(&mut self, stock_in: bool);
}

pub trait PowerHal {
    fn begin(&mut self);
    fn read(&self) -> PowerStatus;
    fn set_grove_power(&mut self, enabled: bool);
    fn enter_deep_sleep_wake_on_button(&mut self);
}

pub trait NvsStore {
    fn load(&mut self, out: &mut NvsConfig) -> bool;
    fn save(&mut self, cfg: &NvsConfig) -> bool;
    fn clear_device_token(&mut self);
    fn factory_reset(&mut self);
}

pub trait CaptivePortalHal {
    fn start(&mut self, ap_suffix: &str);
    fn stop(&mut self);
    fn loop_portal(&mut self);
    fn take_submission(&mut self, out: &mut CaptivePortalSubmission) -> bool;
}

pub trait DeviceClient {
    fn set_server_url(&mut self, url: &str);
    fn set_device_token(&mut self, token: &str);
    fn complete_pairing(
        &mut self,
        code: &str,
        label: &str,
        hub_version: &str,
        boot_id: &str,
        out: &mut PairCompleteParsed,
    ) -> HttpCallResult;
    fn send_scan(
        &mut self,
        record: &RingScanRecord,
        boot_id: &str,
        age_ms: u32,
        time_approx: bool,
        out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult;
    fn flush_batch(
        &mut self,
        batch: &[RingScanRecord],
        boot_id: &str,
        now_ms: u64,
        last_out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult;
    fn send_heartbeat(
        &mut self,
        hub_version: &str,
        boot_id: &str,
        uptime_s: u32,
        queued: usize,
        battery_pct: i32,
        charging: bool,
        out: &mut HeartbeatParsed,
    ) -> HttpCallResult;
}

pub trait WifiStationHal {
    fn begin(&mut self);
    fn connect(&mut self, ssid: &str, pass: &str);
    fn is_connected(&self) -> bool;
    fn tick(&mut self);
    fn disconnect_station(&mut self);
}
