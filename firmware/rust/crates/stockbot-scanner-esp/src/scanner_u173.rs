//! `ScannerHal` for a U173 barcode reader on the Grove port, over I2C (#743, #747).
//!
//! On the StickS3 the reader sits on I2C port 0 (SDA GPIO9, SCL GPIO10, 100 kHz; M5Unified
//! `_pin_table_i2c_ex_in`) and runs from the 5 V Grove output that the M5PM1 driver switches on.
//! On the AtomS3 Lite it sits on I2C port 0 (SDA GPIO2, SCL GPIO1) behind the A162 hub, sharing
//! the bus with the U120 screen, and runs from USB 5 V. Register facts and the unit's behaviour
//! are in `stockbot_scanner_core::u173`.
//!
//! On the StickS3 the controller calls `scanner.begin()` before `power.begin()`, so on a cold boot
//! the first `begin` finds the reader unpowered and fails. That is expected: `poll` retries every
//! [`RETRY_INTERVAL_MS`], which also finds a reader plugged in later.
//!
//! The unit has no symbology control, so `enable_1d_only` only logs; ignoring QR codes is done by
//! the core barcode classifier. The reader decodes only while `set_continuous_mode(true)` is in
//! force (docked or trigger button held). The USB serial stand-in stays alongside for bench input.

use esp_idf_svc::hal::delay::FreeRtos;
use stockbot_scanner_core::i2c::I2cBus;
use stockbot_scanner_core::u173;
use stockbot_scanner_core::{ScanLine, ScannerHal};

use crate::bench::SerialScanner;

const SETTLE_MS: u32 = 10;
const RETRY_INTERVAL_MS: u64 = 5_000;
const ERROR_LOG_INTERVAL_MS: u64 = 30_000;

pub struct ScannerU173<B: I2cBus> {
    bus: B,
    serial: SerialScanner,
    present: bool,
    /// What the controller last asked for; applied again whenever the reader (re)starts.
    want_decoding: bool,
    last_attempt_ms: Option<u64>,
    last_error_log_ms: Option<u64>,
    logged_no_1d_control: bool,
}

impl<B: I2cBus> ScannerU173<B> {
    pub fn new(bus: B, serial: SerialScanner) -> Self {
        Self {
            bus,
            serial,
            present: false,
            want_decoding: false,
            last_attempt_ms: None,
            last_error_log_ms: None,
            logged_no_1d_control: false,
        }
    }

    /// Logs at most once per [`ERROR_LOG_INTERVAL_MS`].
    fn warn_rate_limited(&mut self, now_ms: u64, what: &str) {
        let due = self
            .last_error_log_ms
            .is_none_or(|at| now_ms.saturating_sub(at) >= ERROR_LOG_INTERVAL_MS);
        if due {
            self.last_error_log_ms = Some(now_ms);
            log::warn!("scanner: {what}");
        }
    }

    /// Wakes the reader and puts it in manual trigger mode; marks it present on success.
    fn try_begin(&mut self) {
        FreeRtos::delay_ms(SETTLE_MS);
        match u173::begin(&mut self.bus) {
            Ok(version) => {
                log::info!("scanner: U173 ready (firmware {version})");
                self.present = true;
                if self.want_decoding && u173::set_decoding(&mut self.bus, true).is_err() {
                    self.present = false;
                    log::error!("scanner: U173 did not accept the trigger after setup");
                }
            }
            Err(_) => {
                self.present = false;
                log::error!("scanner: U173 not found on the Grove I2C bus; will retry");
            }
        }
    }
}

impl<B: I2cBus> ScannerHal for ScannerU173<B> {
    fn begin(&mut self) {
        self.serial.begin();
        self.try_begin();
    }

    fn poll(&mut self, now_ms: u64, out: &mut Vec<ScanLine>) {
        self.serial.poll(now_ms, out);
        if !self.present {
            let due = self
                .last_attempt_ms
                .is_none_or(|at| now_ms.saturating_sub(at) >= RETRY_INTERVAL_MS);
            if !due {
                return;
            }
            self.last_attempt_ms = Some(now_ms);
            self.try_begin();
            if !self.present {
                return;
            }
        }
        match u173::poll(&mut self.bus) {
            Ok(Some(barcode)) => out.push(ScanLine {
                barcode,
                timestamp_ms: now_ms,
            }),
            Ok(None) => {}
            Err(_) => {
                self.present = false;
                self.last_attempt_ms = Some(now_ms);
                self.warn_rate_limited(now_ms, "U173 read failed; will retry setup");
            }
        }
    }

    fn enable_1d_only(&mut self, enabled: bool) {
        self.serial.enable_1d_only(enabled);
        if !self.logged_no_1d_control {
            self.logged_no_1d_control = true;
            log::info!("scanner: the U173 has no symbology control; 1D filtering is in software");
        }
    }

    fn set_continuous_mode(&mut self, enabled: bool) {
        self.want_decoding = enabled;
        self.serial.set_continuous_mode(enabled);
        if self.present && u173::set_decoding(&mut self.bus, enabled).is_err() {
            self.present = false;
            log::warn!("scanner: U173 did not accept the trigger; will retry setup");
        }
    }
}
