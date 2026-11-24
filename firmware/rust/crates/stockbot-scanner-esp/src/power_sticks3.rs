//! `PowerHal` for the StickS3 over the M5PM1 power chip (#741).
//!
//! Deep sleep is not built: it needs the ESP-IDF sleep sys calls, which are unsafe and banned in
//! this crate. `enter_deep_sleep_wake_on_button` only turns the Grove output off and logs. How to
//! allow that call (a reviewed unsafe island or a safe wrapper crate) is a follow-up decision.
//!
//! Charge-pin polarity and the battery-percent formula are UNVERIFIED until the bench run; see
//! `stockbot_scanner_core::m5pm1` and `HARDWARE-sticks3.md`.

use std::cell::{Cell, RefCell};
use std::time::{Duration, Instant};

use stockbot_scanner_core::m5pm1::{self, I2cBus};
use stockbot_scanner_core::{PowerHal, PowerStatus};

const ERROR_LOG_INTERVAL: Duration = Duration::from_secs(30);

pub struct Sticks3Power<B: I2cBus> {
    // `PowerHal::read` takes `&self`, so the bus and the cache sit behind interior mutability.
    bus: RefCell<B>,
    last_good: RefCell<Option<PowerStatus>>,
    last_error_log: Cell<Option<Instant>>,
}

impl<B: I2cBus> Sticks3Power<B> {
    pub fn new(bus: B) -> Self {
        Self {
            bus: RefCell::new(bus),
            last_good: RefCell::new(None),
            last_error_log: Cell::new(None),
        }
    }

    /// Logs a warning at most once per [`ERROR_LOG_INTERVAL`].
    fn warn_rate_limited(&self, what: &str) {
        let now = Instant::now();
        let due = match self.last_error_log.get() {
            None => true,
            Some(at) => now.duration_since(at) >= ERROR_LOG_INTERVAL,
        };
        if due {
            self.last_error_log.set(Some(now));
            log::warn!("power: {what}");
        }
    }

    /// Before any good reading: assume dock power and no gauge, like the bench stand-in did.
    fn fallback(&self) -> PowerStatus {
        self.last_good.borrow().clone().unwrap_or(PowerStatus {
            vbus_present: true,
            battery_pct: None,
            charging: false,
        })
    }
}

impl<B: I2cBus> PowerHal for Sticks3Power<B> {
    fn begin(&mut self) {
        let mut bus = self.bus.borrow_mut();
        match m5pm1::begin(&mut *bus) {
            Ok(report) => {
                if report.device_id_matches() {
                    log::info!("power: M5PM1 ready (id {:#06x})", report.device_id);
                } else {
                    log::warn!(
                        "power: M5PM1 id {:#06x}, expected {:#06x}; carrying on",
                        report.device_id,
                        m5pm1::DEVICE_ID_EXPECTED
                    );
                }
            }
            Err(_) => log::error!("power: M5PM1 setup failed on the internal I2C bus"),
        }
        // The U173 reader runs from the 5 V Grove output.
        match m5pm1::set_ext_output(&mut *bus, true) {
            Ok(()) => log::info!("power: grove 5 V on"),
            Err(_) => log::error!("power: could not switch the grove 5 V output on"),
        }
    }

    fn read(&self) -> PowerStatus {
        let result = m5pm1::read_status(&mut *self.bus.borrow_mut());
        match result {
            Ok(raw) => {
                let status = m5pm1::power_status(&raw);
                *self.last_good.borrow_mut() = Some(status.clone());
                status
            }
            Err(_) => {
                self.warn_rate_limited("M5PM1 status read failed; using the last good reading");
                self.fallback()
            }
        }
    }

    fn set_grove_power(&mut self, enabled: bool) {
        match m5pm1::set_ext_output(&mut *self.bus.borrow_mut(), enabled) {
            Ok(()) => log::info!("power: grove 5 V {}", if enabled { "on" } else { "off" }),
            Err(_) => self.warn_rate_limited("could not change the grove 5 V output"),
        }
    }

    fn enter_deep_sleep_wake_on_button(&mut self) {
        self.set_grove_power(false);
        log::warn!("power: deep sleep not available yet");
    }
}
