//! Wi-Fi station on `EspWifi`. Nothing here blocks the controller loop.

use std::time::{Duration, Instant};

use esp_idf_svc::eventloop::EspSystemEventLoop;
use esp_idf_svc::hal::modem::Modem;
use esp_idf_svc::sys::EspError;
use esp_idf_svc::wifi::{AuthMethod, ClientConfiguration, Configuration, EspWifi};
use stockbot_scanner_core::WifiStationHal;

/// Shortest gap between two connection attempts while offline.
const RECONNECT_BACKOFF: Duration = Duration::from_secs(5);

pub struct EspWifiStation {
    wifi: EspWifi<'static>,
    ssid: String,
    pass: String,
    started: bool,
    last_attempt: Option<Instant>,
}

impl EspWifiStation {
    /// Builds the driver without persisting its own settings; credentials live in our NVS keys.
    pub fn new(modem: Modem, sysloop: EspSystemEventLoop) -> Result<Self, EspError> {
        Ok(Self {
            wifi: EspWifi::new(modem, sysloop, None)?,
            ssid: String::new(),
            pass: String::new(),
            started: false,
            last_attempt: None,
        })
    }

    /// Station MAC as `AA:BB:CC:DD:EE:FF`, or an empty string when unreadable.
    pub fn mac_string(&self) -> String {
        match self.wifi.sta_netif().get_mac() {
            Ok(mac) => mac
                .iter()
                .map(|byte| format!("{byte:02X}"))
                .collect::<Vec<_>>()
                .join(":"),
            Err(err) => {
                log::warn!("wifi: reading the station MAC failed: {err}");
                String::new()
            }
        }
    }

    fn attempt(&mut self) {
        self.last_attempt = Some(Instant::now());
        if let Err(err) = self.try_connect() {
            log::warn!("wifi: connect attempt failed: {err}");
        }
    }

    fn try_connect(&mut self) -> Result<(), EspError> {
        let configuration = Configuration::Client(ClientConfiguration {
            ssid: match self.ssid.as_str().try_into() {
                Ok(ssid) => ssid,
                Err(_) => {
                    log::warn!("wifi: the network name is longer than 32 bytes");
                    return Ok(());
                }
            },
            password: match self.pass.as_str().try_into() {
                Ok(pass) => pass,
                Err(_) => {
                    log::warn!("wifi: the password is longer than 64 bytes");
                    return Ok(());
                }
            },
            auth_method: if self.pass.is_empty() {
                AuthMethod::None
            } else {
                AuthMethod::WPA2Personal
            },
            ..Default::default()
        });
        self.wifi.set_configuration(&configuration)?;
        if !self.started {
            self.wifi.start()?;
            self.started = true;
        }
        self.wifi.connect()
    }
}

impl WifiStationHal for EspWifiStation {
    fn begin(&mut self) {
        log::info!("wifi: station ready");
    }

    fn connect(&mut self, ssid: &str, pass: &str) {
        self.ssid = ssid.to_owned();
        self.pass = pass.to_owned();
        if self.ssid.is_empty() {
            return;
        }
        log::info!("wifi: connecting to {ssid}");
        self.attempt();
    }

    fn is_connected(&self) -> bool {
        self.wifi.is_up().unwrap_or(false)
    }

    fn tick(&mut self) {
        if self.ssid.is_empty() || self.is_connected() {
            return;
        }
        let due = self
            .last_attempt
            .is_none_or(|at| at.elapsed() >= RECONNECT_BACKOFF);
        if due {
            self.attempt();
        }
    }

    fn disconnect_station(&mut self) {
        self.ssid.clear();
        self.pass.clear();
        self.last_attempt = None;
        if self.started {
            if let Err(err) = self.wifi.disconnect() {
                log::debug!("wifi: disconnect: {err}");
            }
        }
    }
}
