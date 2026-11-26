//! `ScannerHal` for the Symcode MJ-2030-B 2.4 GHz dongle on the AtomS3 Lite's USB-C port (#748).
//!
//! The dongle is a USB keyboard. `stockbot_usb_host` runs the port in host mode and hands over
//! each 8-byte boot report; the core `HidKeycodeDecoder` turns them into lines, and Enter ends a
//! scan. The scanner has no symbology or trigger control, so 1D filtering is the core barcode
//! classifier's job and the dongle always reads.
//!
//! Host mode takes the port away from the serial console, so the serial stand-in only works until
//! `begin` starts the USB host.

use stockbot_scanner_core::{HidKeycodeDecoder, ScanLine, ScannerHal};
use stockbot_usb_host::{KeyboardEvent, UsbKeyboard};

use crate::bench::SerialScanner;

pub struct SymcodeScanner {
    serial: SerialScanner,
    usb: Option<UsbKeyboard>,
    decoder: HidKeycodeDecoder,
}

impl SymcodeScanner {
    pub fn new(serial: SerialScanner) -> Self {
        Self {
            serial,
            usb: None,
            decoder: HidKeycodeDecoder::new(),
        }
    }
}

impl ScannerHal for SymcodeScanner {
    fn begin(&mut self) {
        self.serial.begin();
        if self.usb.is_some() {
            return;
        }
        log::info!("scanner: switching the USB port to the scanner; the serial console stops here");
        match UsbKeyboard::start() {
            Ok(usb) => self.usb = Some(usb),
            Err(err) => log::error!("scanner: USB scanner unavailable: {err}"),
        }
    }

    fn poll(&mut self, now_ms: u64, out: &mut Vec<ScanLine>) {
        self.serial.poll(now_ms, out);
        let Some(usb) = &self.usb else {
            return;
        };
        while let Some(event) = usb.next_event() {
            match event {
                KeyboardEvent::Report(report) => {
                    if let Some(barcode) = self.decoder.consume_report(&report) {
                        out.push(ScanLine {
                            barcode,
                            timestamp_ms: now_ms,
                        });
                    }
                }
                KeyboardEvent::Connected => {
                    self.decoder = HidKeycodeDecoder::new();
                    log::info!("scanner: USB scanner connected");
                }
                KeyboardEvent::Removed => {
                    self.decoder = HidKeycodeDecoder::new();
                    log::info!("scanner: USB scanner unplugged");
                }
                KeyboardEvent::Rejected(reason) => log::warn!("scanner: {reason}"),
            }
        }
    }

    fn enable_1d_only(&mut self, enabled: bool) {
        self.serial.enable_1d_only(enabled);
    }

    fn set_continuous_mode(&mut self, enabled: bool) {
        self.serial.set_continuous_mode(enabled);
    }
}
