//! Wi-Fi on `EspWifi`: the station that joins the home network and the setup network the captive
//! portal runs on. Both share one radio. Nothing here blocks the controller loop.

use std::cell::RefCell;
use std::net::Ipv4Addr;
use std::rc::Rc;
use std::time::{Duration, Instant};

use esp_idf_svc::eventloop::EspSystemEventLoop;
use esp_idf_svc::hal::modem::Modem;
use esp_idf_svc::ipv4::{self, Mask, RouterConfiguration, Subnet};
use esp_idf_svc::netif::{EspNetif, NetifConfiguration, NetifStack};
use esp_idf_svc::sys::EspError;
use esp_idf_svc::wifi::{
    AccessPointConfiguration, AuthMethod, ClientConfiguration, Configuration, EspWifi, WifiDriver,
};
use stockbot_scanner_core::{WifiStationHal, PORTAL_IPV4};

/// Shortest gap between two connection attempts while offline.
const RECONNECT_BACKOFF: Duration = Duration::from_secs(5);
/// A phone or two at a time is all setup needs; the limit keeps the DHCP pool small.
const SETUP_NETWORK_MAX_CLIENTS: u16 = 4;

/// The one radio, shared by the station and the captive portal on the controller thread.
pub type SharedRadio = Rc<RefCell<Radio>>;

pub struct Radio {
    wifi: EspWifi<'static>,
    access_point_on: bool,
}

impl Radio {
    /// Builds the driver without persisting its own settings; credentials live in our NVS keys.
    /// The setup network's gateway is the portal address, and DHCP hands that out as the DNS
    /// server too, so every lookup reaches the portal's DNS answerer.
    pub fn new(modem: Modem, sysloop: EspSystemEventLoop) -> Result<SharedRadio, EspError> {
        let portal_ip = Ipv4Addr::from(PORTAL_IPV4);
        let ap_netif = EspNetif::new_with_conf(&NetifConfiguration {
            ip_configuration: Some(ipv4::Configuration::Router(RouterConfiguration {
                subnet: Subnet {
                    gateway: portal_ip,
                    mask: Mask(24),
                },
                dhcp_enabled: true,
                dns: Some(portal_ip),
                secondary_dns: None,
            })),
            ..NetifConfiguration::wifi_default_router()
        })?;
        let wifi = EspWifi::wrap_all(
            WifiDriver::new(modem, sysloop, None)?,
            EspNetif::new(NetifStack::Sta)?,
            ap_netif,
        )?;
        Ok(Rc::new(RefCell::new(Self {
            wifi,
            access_point_on: false,
        })))
    }

    /// Switches the radio to an open setup network. The station stays off until it stops.
    pub fn start_access_point(&mut self, ssid: &str) -> Result<(), EspError> {
        let configuration = Configuration::AccessPoint(AccessPointConfiguration {
            ssid: match ssid.try_into() {
                Ok(ssid) => ssid,
                Err(_) => {
                    log::warn!("wifi: the setup network name is longer than 32 bytes");
                    return Err(EspError::from_infallible::<
                        { esp_idf_svc::sys::ESP_ERR_INVALID_ARG },
                    >());
                }
            },
            auth_method: AuthMethod::None,
            max_connections: SETUP_NETWORK_MAX_CLIENTS,
            ..Default::default()
        });
        if self.wifi.is_started()? {
            self.wifi.stop()?;
        }
        self.wifi.set_configuration(&configuration)?;
        self.wifi.start()?;
        self.access_point_on = true;
        Ok(())
    }

    /// Turns the setup network off; the station starts the radio again on its next attempt.
    pub fn stop_access_point(&mut self) -> Result<(), EspError> {
        if !self.access_point_on {
            return Ok(());
        }
        self.access_point_on = false;
        if self.wifi.is_started()? {
            self.wifi.stop()?;
        }
        Ok(())
    }
}

pub struct EspWifiStation {
    radio: SharedRadio,
    ssid: String,
    pass: String,
    last_attempt: Option<Instant>,
}

impl EspWifiStation {
    pub fn new(radio: SharedRadio) -> Self {
        Self {
            radio,
            ssid: String::new(),
            pass: String::new(),
            last_attempt: None,
        }
    }

    /// Station MAC as `AA:BB:CC:DD:EE:FF`, or an empty string when unreadable.
    pub fn mac_string(&self) -> String {
        match self.radio.borrow().wifi.sta_netif().get_mac() {
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

    fn access_point_on(&self) -> bool {
        self.radio.borrow().access_point_on
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
        let mut radio = self.radio.borrow_mut();
        if radio.access_point_on {
            return Ok(());
        }
        radio.wifi.set_configuration(&configuration)?;
        if !radio.wifi.is_started()? {
            radio.wifi.start()?;
        }
        radio.wifi.connect()
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
        let radio = self.radio.borrow();
        !radio.access_point_on && radio.wifi.is_up().unwrap_or(false)
    }

    fn tick(&mut self) {
        if self.ssid.is_empty() || self.access_point_on() || self.is_connected() {
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
        let mut radio = self.radio.borrow_mut();
        if radio.access_point_on || !radio.wifi.is_started().unwrap_or(false) {
            return;
        }
        if let Err(err) = radio.wifi.disconnect() {
            log::debug!("wifi: disconnect: {err}");
        }
    }
}
