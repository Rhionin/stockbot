//! USB keyboard facts the ESP USB host driver needs, kept here so the host tests cover them:
//! finding a keyboard's interrupt IN endpoint in its configuration descriptor, and turning one
//! interrupt transfer into the 8-byte boot report that [`crate::HidKeycodeDecoder`] reads.

/// The scanner dongle's keyboard is interface 0; the driver claims it at alternate setting 0.
pub const KEYBOARD_INTERFACE: u8 = 0;
pub const MIN_PACKET_SIZE: u16 = 8;
pub const MAX_PACKET_SIZE: u16 = 64;

const DESCRIPTOR_TYPE_INTERFACE: u8 = 0x04;
const DESCRIPTOR_TYPE_ENDPOINT: u8 = 0x05;
const ENDPOINT_DIRECTION_IN: u8 = 0x80;
const TRANSFER_TYPE_MASK: u8 = 0x03;
const TRANSFER_TYPE_INTERRUPT: u8 = 0x03;
/// Bits 11-12 of `wMaxPacketSize` count extra high-speed transactions, not bytes.
const PACKET_SIZE_MASK: u16 = 0x07FF;
const INTERFACE_DESCRIPTOR_MIN_LEN: usize = 4;
const ENDPOINT_DESCRIPTOR_MIN_LEN: usize = 6;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct InterruptInEndpoint {
    pub address: u8,
    /// `wMaxPacketSize` clamped to [`MIN_PACKET_SIZE`]..=[`MAX_PACKET_SIZE`]; the transfer buffer
    /// is this size.
    pub packet_size: u16,
}

/// First interrupt IN endpoint of `interface` (alternate setting 0) in a full configuration
/// descriptor (`wTotalLength` bytes). Stops at the first malformed descriptor (zero length or
/// running past the end) rather than guessing.
pub fn find_interrupt_in_endpoint(config: &[u8], interface: u8) -> Option<InterruptInEndpoint> {
    let mut at = usize::from(*config.first()?);
    let mut in_interface = false;
    while at + 2 <= config.len() {
        let len = usize::from(config[at]);
        if len < 2 || at + len > config.len() {
            return None;
        }
        let desc = &config[at..at + len];
        match desc[1] {
            DESCRIPTOR_TYPE_INTERFACE if len >= INTERFACE_DESCRIPTOR_MIN_LEN => {
                in_interface = desc[2] == interface && desc[3] == 0;
            }
            DESCRIPTOR_TYPE_ENDPOINT if in_interface && len >= ENDPOINT_DESCRIPTOR_MIN_LEN => {
                let address = desc[2];
                let attributes = desc[3];
                if address & ENDPOINT_DIRECTION_IN != 0
                    && attributes & TRANSFER_TYPE_MASK == TRANSFER_TYPE_INTERRUPT
                {
                    let raw = u16::from_le_bytes([desc[4], desc[5]]) & PACKET_SIZE_MASK;
                    return Some(InterruptInEndpoint {
                        address,
                        packet_size: raw.clamp(MIN_PACKET_SIZE, MAX_PACKET_SIZE),
                    });
                }
            }
            _ => {}
        }
        at += len;
    }
    None
}

/// The boot keyboard report in one interrupt transfer: the first 8 bytes, zero-filled when the
/// transfer was shorter. An empty transfer carries no report.
pub fn boot_report_from(data: &[u8]) -> Option<[u8; 8]> {
    if data.is_empty() {
        return None;
    }
    let mut report = [0u8; 8];
    let n = data.len().min(report.len());
    report[..n].copy_from_slice(&data[..n]);
    Some(report)
}
