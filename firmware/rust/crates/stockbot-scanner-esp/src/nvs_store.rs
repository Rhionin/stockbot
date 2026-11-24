//! Device configuration in the default NVS partition, namespace `stockbot`.

use esp_idf_svc::nvs::{EspDefaultNvsPartition, EspNvs, NvsDefault};
use esp_idf_svc::sys::EspError;
use stockbot_scanner_core::{ota_nvs_keys as keys, CommitMode, Direction, NvsConfig, NvsStore};

const NAMESPACE: &str = "stockbot";
/// Large enough for a device token or Wi-Fi password with room to spare.
const READ_BUF_LEN: usize = 512;

const DIR_STOCK_IN: &str = "stock_in";
const DIR_STOCK_OUT: &str = "stock_out";
const MODE_IMMEDIATE: &str = "immediate";
const MODE_QUEUE: &str = "queue";

/// Every key the firmware owns in the namespace, including the OTA keys.
const ALL_KEYS: [&str; 13] = [
    keys::WIFI_SSID,
    keys::WIFI_PASS,
    keys::SERVER_URL,
    keys::DEVICE_TOKEN,
    keys::LABEL,
    keys::DEFAULT_DIR,
    keys::COMMIT_MODE,
    keys::INACT_REV,
    keys::CFG_VER,
    keys::BOOT_CNT,
    keys::OTA_REL,
    keys::OTA_ERR,
    keys::OTA_SUPP,
];

pub struct EspNvsStore {
    nvs: EspNvs<NvsDefault>,
}

impl EspNvsStore {
    pub fn open(partition: EspDefaultNvsPartition) -> Result<Self, EspError> {
        Ok(Self {
            nvs: EspNvs::new(partition, NAMESPACE, true)?,
        })
    }

    fn read_str(&self, key: &str) -> Option<String> {
        let mut buf = [0u8; READ_BUF_LEN];
        match self.nvs.get_str(key, &mut buf) {
            Ok(value) => value.map(str::to_owned),
            Err(err) => {
                log::warn!("nvs: reading {key} failed: {err}");
                None
            }
        }
    }

    fn read_u32(&self, key: &str) -> Option<u32> {
        match self.nvs.get_u32(key) {
            Ok(value) => value,
            Err(err) => {
                log::warn!("nvs: reading {key} failed: {err}");
                None
            }
        }
    }

    fn write_str(&mut self, key: &str, value: &str) -> bool {
        match self.nvs.set_str(key, value) {
            Ok(()) => true,
            Err(err) => {
                log::warn!("nvs: writing {key} failed: {err}");
                false
            }
        }
    }

    fn write_u32(&mut self, key: &str, value: u32) -> bool {
        match self.nvs.set_u32(key, value) {
            Ok(()) => true,
            Err(err) => {
                log::warn!("nvs: writing {key} failed: {err}");
                false
            }
        }
    }

    fn remove_key(&mut self, key: &str) {
        if let Err(err) = self.nvs.remove(key) {
            log::warn!("nvs: removing {key} failed: {err}");
        }
    }
}

impl NvsStore for EspNvsStore {
    fn load(&mut self, out: &mut NvsConfig) -> bool {
        let defaults = NvsConfig::default();
        out.wifi_ssid = self.read_str(keys::WIFI_SSID).unwrap_or(defaults.wifi_ssid);
        out.wifi_pass = self.read_str(keys::WIFI_PASS).unwrap_or(defaults.wifi_pass);
        out.server_url = self
            .read_str(keys::SERVER_URL)
            .unwrap_or(defaults.server_url);
        out.device_token = self
            .read_str(keys::DEVICE_TOKEN)
            .unwrap_or(defaults.device_token);
        out.label = self.read_str(keys::LABEL).unwrap_or(defaults.label);
        out.default_direction = match self.read_str(keys::DEFAULT_DIR).as_deref() {
            Some(DIR_STOCK_IN) => Direction::StockIn,
            Some(_) => Direction::StockOut,
            None => defaults.default_direction,
        };
        out.commit_mode = match self.read_str(keys::COMMIT_MODE).as_deref() {
            Some(MODE_IMMEDIATE) => CommitMode::Immediate,
            Some(_) => CommitMode::Queue,
            None => defaults.commit_mode,
        };
        out.inactivity_revert_s = self
            .read_u32(keys::INACT_REV)
            .unwrap_or(defaults.inactivity_revert_s);
        out.config_version = self
            .read_u32(keys::CFG_VER)
            .unwrap_or(defaults.config_version);
        out.boot_count = self.read_u32(keys::BOOT_CNT).unwrap_or(defaults.boot_count);
        true
    }

    fn save(&mut self, cfg: &NvsConfig) -> bool {
        let direction = match cfg.default_direction {
            Direction::StockIn => DIR_STOCK_IN,
            Direction::StockOut => DIR_STOCK_OUT,
        };
        let commit_mode = match cfg.commit_mode {
            CommitMode::Immediate => MODE_IMMEDIATE,
            CommitMode::Queue => MODE_QUEUE,
        };
        let mut ok = true;
        ok &= self.write_str(keys::WIFI_SSID, &cfg.wifi_ssid);
        ok &= self.write_str(keys::WIFI_PASS, &cfg.wifi_pass);
        ok &= self.write_str(keys::SERVER_URL, &cfg.server_url);
        ok &= self.write_str(keys::DEVICE_TOKEN, &cfg.device_token);
        ok &= self.write_str(keys::LABEL, &cfg.label);
        ok &= self.write_str(keys::DEFAULT_DIR, direction);
        ok &= self.write_str(keys::COMMIT_MODE, commit_mode);
        ok &= self.write_u32(keys::INACT_REV, cfg.inactivity_revert_s);
        ok &= self.write_u32(keys::CFG_VER, cfg.config_version);
        ok &= self.write_u32(keys::BOOT_CNT, cfg.boot_count);
        ok
    }

    fn clear_device_token(&mut self) {
        self.remove_key(keys::DEVICE_TOKEN);
    }

    fn factory_reset(&mut self) {
        for key in ALL_KEYS {
            self.remove_key(key);
        }
    }

    fn stockbot_get_str(&self, key: &str) -> Option<String> {
        self.read_str(key)
    }

    fn stockbot_set_str(&mut self, key: &str, value: &str) {
        self.write_str(key, value);
    }

    fn stockbot_remove(&mut self, key: &str) {
        self.remove_key(key);
    }

    fn stockbot_get_u32(&self, key: &str) -> Option<u32> {
        self.read_u32(key)
    }

    fn stockbot_set_u32(&mut self, key: &str, value: u32) {
        self.write_u32(key, value);
    }
}
