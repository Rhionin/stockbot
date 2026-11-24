//! Device NVS configuration (`firmware/src/net/nvs_config.h`).

use crate::barcode::Direction;
use crate::scanner_state::CommitMode;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct NvsConfig {
    pub wifi_ssid: String,
    pub wifi_pass: String,
    pub server_url: String,
    pub device_token: String,
    pub label: String,
    pub default_direction: Direction,
    pub inactivity_revert_s: u32,
    pub commit_mode: CommitMode,
    pub config_version: u32,
    pub boot_count: u32,
}

impl Default for NvsConfig {
    fn default() -> Self {
        Self {
            wifi_ssid: String::new(),
            wifi_pass: String::new(),
            server_url: "https://stockbot.rhionin.com".to_owned(),
            device_token: String::new(),
            label: "Storage Room Scanner".to_owned(),
            default_direction: Direction::StockOut,
            inactivity_revert_s: 300,
            commit_mode: CommitMode::Queue,
            config_version: 0,
            boot_count: 0,
        }
    }
}
