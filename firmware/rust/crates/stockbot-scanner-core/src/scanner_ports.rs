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
    /// Clears OTA-only overlay; default no-op for non-OTA displays.
    fn clear_ota_overlay(&mut self) {}
}

/// Monotonic clock for controllers (heartbeat, OTA deadlines).
pub trait ClockHal {
    fn now_ms(&self) -> u64;
}

/// One streaming firmware GET (§6 step 5); resume uses `Range` and expects `206` when offset &gt; 0.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FirmwareOpenResult {
    pub transport_ok: bool,
    pub http_status: i32,
}

pub trait FirmwareHttpHal {
    fn open_firmware(&mut self, url: &str, range_start: u64) -> FirmwareOpenResult;
    /// Read next bytes from the open response (`Ok(0)` = end of body for this connection).
    fn read_firmware_chunk(&mut self, buf: &mut [u8]) -> Result<usize, &'static str>;
    fn close_firmware(&mut self);
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
    /// Namespace `stockbot` string keys (firmware-ota.md §9).
    fn stockbot_get_str(&self, key: &str) -> Option<String>;
    fn stockbot_set_str(&mut self, key: &str, value: &str);
    fn stockbot_remove(&mut self, key: &str);
    fn stockbot_get_u32(&self, key: &str) -> Option<u32>;
    fn stockbot_set_u32(&mut self, key: &str, value: u32);
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
    ) -> HttpCallResult;
}

pub trait WifiStationHal {
    fn begin(&mut self);
    fn connect(&mut self, ssid: &str, pass: &str);
    fn is_connected(&self) -> bool;
    fn tick(&mut self);
    fn disconnect_station(&mut self);
}
