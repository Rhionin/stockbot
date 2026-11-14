//! Device HAL traits for [`crate::ota_controller::OtaController`] (separate from `scanner_ports`).

use crate::display_model::DisplayBands;
use crate::scanner_ports::{PowerStatus, ScanLine};

/// Monotonic clock for OTA scheduling and deadlines.
pub trait ClockHal {
    fn now_ms(&self) -> u64;
}

/// Scanner input while OTA may be preempted by user activity.
pub trait ScannerHal {
    fn poll(&mut self, now_ms: u64, out: &mut Vec<ScanLine>);
}

/// Display output during OTA.
pub trait DisplayHal {
    fn render(&mut self, bands: &DisplayBands);
    fn clear_ota_display(&mut self) {}
}

/// Audio / LED feedback during OTA guards.
pub trait FeedbackHal {
    fn play_ota_blocked(&mut self);
}

/// Power telemetry for StickS3 install-window checks.
pub trait PowerHal {
    fn read(&self) -> PowerStatus;
}

/// NVS namespace `stockbot` — frozen keys from firmware-ota.md §9 plus `boot_cnt`.
pub trait OtaNvsHal {
    fn get_str(&self, key: &str) -> Option<String>;
    fn set_str(&mut self, key: &str, value: &str);
    fn get_u32(&self, key: &str) -> Option<u32>;
    fn set_u32(&mut self, key: &str, value: u32);
    fn remove(&mut self, key: &str);
}

pub mod ota_nvs_keys {
    pub const WIFI_SSID: &str = "wifi_ssid";
    pub const WIFI_PASS: &str = "wifi_pass";
    pub const SERVER_URL: &str = "server_url";
    pub const DEVICE_TOKEN: &str = "device_token";
    pub const LABEL: &str = "label";
    pub const DEFAULT_DIR: &str = "default_dir";
    pub const COMMIT_MODE: &str = "commit_mode";
    pub const OTA_REL: &str = "ota_rel";
    pub const OTA_ERR: &str = "ota_err";
    pub const OTA_SUPP: &str = "ota_supp";
    pub const INACT_REV: &str = "inact_rev";
    pub const CFG_VER: &str = "cfg_ver";
    pub const BOOT_CNT: &str = "boot_cnt";
}

/// Result of a firmware binary HTTP fetch (GET with optional Range).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FirmwareHttpResult {
    pub transport_ok: bool,
    pub http_status: i32,
    pub body: Vec<u8>,
}

/// Authenticated firmware download client.
pub trait HttpHal {
    fn fetch_firmware(&mut self, url: &str, range_start: u64) -> FirmwareHttpResult;
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum OtaSlot {
    #[default]
    Slot0,
    Slot1,
}

/// OTA flash partition operations (`esp_ota_*` parity).
pub trait OtaSlotsHal {
    fn running_slot(&self) -> OtaSlot;
    fn inactive_slot(&self) -> OtaSlot;
    fn begin_inactive_write(&mut self) -> Result<(), &'static str>;
    fn write_inactive(&mut self, chunk: &[u8]) -> Result<(), &'static str>;
    fn end_inactive_write(&mut self) -> Result<(), &'static str>;
    fn set_boot_inactive(&mut self) -> Result<(), &'static str>;
    fn mark_running_valid(&mut self) -> Result<(), &'static str>;
    fn mark_running_invalid_and_reboot(&mut self) -> Result<(), &'static str>;
    fn last_invalid_partition(&self) -> Option<OtaSlot>;
    fn reboot(&mut self);
    fn bytes_written_inactive(&self) -> u64;
    fn abort_inactive_write(&mut self);
}
