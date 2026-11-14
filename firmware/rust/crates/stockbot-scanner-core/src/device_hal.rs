//! OTA flash slot HAL (`esp_ota_*` parity). Shared scanner peripherals live in [`crate::scanner_ports`].

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum OtaSlot {
    #[default]
    Slot0,
    Slot1,
}

/// Frozen NVS keys in namespace `stockbot` (firmware-ota.md §9).
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

/// OTA flash partition operations.
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
