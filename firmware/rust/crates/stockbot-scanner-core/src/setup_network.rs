//! Captive-portal AP naming (`firmware/src/core/scanner_setup_network.h`).

pub const SCANNER_SETUP_AP_PREFIX: &str = "Scanner-Setup-";

pub fn scanner_setup_ssid(ap_suffix: &str) -> String {
    format!("{SCANNER_SETUP_AP_PREFIX}{ap_suffix}")
}

/// Last four hex digits of the MAC (colons/dashes stripped, uppercase).
pub fn ap_suffix_from_mac_address(mac: &str) -> String {
    let mut hex = String::with_capacity(12);
    for ch in mac.chars() {
        if ch.is_ascii_hexdigit() {
            hex.push(ch.to_ascii_uppercase());
        }
    }
    if hex.len() < 4 {
        return "0000".to_owned();
    }
    hex[hex.len() - 4..].to_owned()
}
