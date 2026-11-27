//! #748: the Symcode dongle's configuration descriptor to its keyboard endpoint, and its
//! interrupt transfers to scan lines.

use stockbot_scanner_core::usb_hid::{
    boot_report_from, find_interrupt_in_endpoint, InterruptInEndpoint, KEYBOARD_INTERFACE,
};
use stockbot_scanner_core::HidKeycodeDecoder;

fn config_header() -> Vec<u8> {
    vec![9, 0x02, 0, 0, 2, 1, 0, 0xA0, 50]
}

fn interface(number: u8, alternate: u8) -> Vec<u8> {
    vec![9, 0x04, number, alternate, 1, 0x03, 0x01, 0x01, 0]
}

fn hid_class_descriptor() -> Vec<u8> {
    vec![9, 0x21, 0x11, 0x01, 0, 1, 0x22, 63, 0]
}

fn endpoint(address: u8, attributes: u8, max_packet: u16) -> Vec<u8> {
    let [lo, hi] = max_packet.to_le_bytes();
    vec![7, 0x05, address, attributes, lo, hi, 10]
}

fn config(parts: &[Vec<u8>]) -> Vec<u8> {
    let mut out = config_header();
    for part in parts {
        out.extend_from_slice(part);
    }
    let [lo, hi] = (out.len() as u16).to_le_bytes();
    out[2] = lo;
    out[3] = hi;
    out
}

const INTERRUPT: u8 = 0x03;
const BULK: u8 = 0x02;

fn found(config: &[u8]) -> Option<InterruptInEndpoint> {
    find_interrupt_in_endpoint(config, KEYBOARD_INTERFACE)
}

#[test]
fn finds_the_keyboard_endpoint_of_a_two_interface_dongle() {
    let desc = config(&[
        interface(0, 0),
        hid_class_descriptor(),
        endpoint(0x81, INTERRUPT, 8),
        interface(1, 0),
        hid_class_descriptor(),
        endpoint(0x82, INTERRUPT, 16),
    ]);
    assert_eq!(
        found(&desc),
        Some(InterruptInEndpoint {
            address: 0x81,
            packet_size: 8
        })
    );
}

#[test]
fn skips_endpoints_that_belong_to_other_interfaces() {
    let desc = config(&[
        interface(1, 0),
        endpoint(0x82, INTERRUPT, 16),
        interface(0, 0),
        endpoint(0x81, INTERRUPT, 8),
    ]);
    assert_eq!(found(&desc).map(|e| e.address), Some(0x81));
    assert_eq!(
        find_interrupt_in_endpoint(&desc, 1).map(|e| e.address),
        Some(0x82)
    );
}

#[test]
fn skips_out_and_bulk_endpoints() {
    let desc = config(&[
        interface(0, 0),
        endpoint(0x01, INTERRUPT, 8),
        endpoint(0x83, BULK, 64),
        endpoint(0x84, INTERRUPT, 8),
    ]);
    assert_eq!(found(&desc).map(|e| e.address), Some(0x84));
}

#[test]
fn skips_alternate_settings_of_interface_0() {
    let desc = config(&[
        interface(0, 1),
        endpoint(0x85, INTERRUPT, 8),
        interface(0, 0),
        endpoint(0x81, INTERRUPT, 8),
    ]);
    assert_eq!(found(&desc).map(|e| e.address), Some(0x81));
}

#[test]
fn packet_size_is_clamped_to_8_through_64() {
    for (raw, want) in [
        (0u16, 8u16),
        (4, 8),
        (8, 8),
        (32, 32),
        (64, 64),
        (512, 64),
        // High-bandwidth bits (11-12) are not bytes.
        (0x1808, 8),
        (0x0840, 64),
    ] {
        let desc = config(&[interface(0, 0), endpoint(0x81, INTERRUPT, raw)]);
        assert_eq!(
            found(&desc).map(|e| e.packet_size),
            Some(want),
            "wMaxPacketSize {raw:#06x}"
        );
    }
}

#[test]
fn no_keyboard_endpoint_means_none() {
    assert_eq!(found(&config(&[interface(0, 0)])), None);
    assert_eq!(
        found(&config(&[interface(1, 0), endpoint(0x81, INTERRUPT, 8)])),
        None
    );
    assert_eq!(found(&[]), None);
}

/// A zero-length descriptor would loop forever if walked; the parser stops instead of guessing,
/// even though a valid endpoint follows.
#[test]
fn zero_length_descriptor_stops_the_walk() {
    let mut desc = config(&[interface(0, 0), endpoint(0x81, INTERRUPT, 8)]);
    let interface_end = 9 + 9;
    desc.insert(interface_end, 0x05);
    desc.insert(interface_end, 0);
    assert_eq!(found(&desc), None);
    assert_eq!(
        found(&config(&[interface(0, 0), endpoint(0x81, INTERRUPT, 8)])).map(|e| e.address),
        Some(0x81)
    );
}

#[test]
fn descriptor_running_past_the_end_is_ignored() {
    let whole = config(&[interface(0, 0), endpoint(0x81, INTERRUPT, 8)]);
    let truncated = &whole[..whole.len() - 1];
    assert_eq!(found(truncated), None);
    assert!(found(&whole).is_some());
}

#[test]
fn boot_report_takes_the_first_8_bytes_and_zero_fills() {
    assert_eq!(boot_report_from(&[]), None);
    assert_eq!(
        boot_report_from(&[0x02, 0, 0x04]),
        Some([0x02, 0, 0x04, 0, 0, 0, 0, 0])
    );
    let long: Vec<u8> = (1..=16).collect();
    assert_eq!(boot_report_from(&long), Some([1, 2, 3, 4, 5, 6, 7, 8]));
}

/// The path the driver runs: each interrupt transfer becomes a boot report, and Enter ends the
/// scan.
#[test]
fn interrupt_transfers_decode_to_one_scan_line() {
    let transfers: [&[u8]; 6] = [
        &[0, 0, 0x1F, 0, 0, 0, 0, 0],
        &[0, 0, 0, 0, 0, 0, 0, 0],
        &[0, 0, 0x20, 0, 0, 0, 0, 0],
        &[0, 0, 0, 0, 0, 0, 0, 0],
        &[0, 0, 0x28],
        &[0, 0, 0, 0, 0, 0, 0, 0],
    ];
    let mut decoder = HidKeycodeDecoder::new();
    let mut lines = Vec::new();
    for data in transfers {
        let report = boot_report_from(data).expect("non-empty transfer");
        lines.extend(decoder.consume_report(&report));
    }
    assert_eq!(lines, vec!["23".to_owned()]);
}
