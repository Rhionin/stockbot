//! `DisplayHal` for the U120 Unit LCD on the AtomS3 Lite's Grove hub (#747).
//!
//! The unit shares the Grove I2C bus (port 0, SDA GPIO2, SCL GPIO1) with the U173 reader, through
//! the A162 hub, and runs from USB 5 V, so it can start in `begin`. The command set and the frame
//! push are in `stockbot_scanner_core::u120`; the bands are drawn by the same
//! `stockbot_scanner_core::screen::draw_bands` as the StickS3 screen.
//!
//! The scanner must keep working without a screen: a unit that does not answer is logged once and
//! tried again at most every [`RETRY_INTERVAL`], on the next render. That also finds a unit
//! plugged in later, though it stays blank until the screen content next changes (the controller
//! renders only on changes).

use std::time::{Duration, Instant};

use esp_idf_svc::hal::delay::FreeRtos;
use stockbot_scanner_core::i2c::I2cBus;
use stockbot_scanner_core::screen::draw_bands;
use stockbot_scanner_core::u120::{brightness_for, Frame, U120};
use stockbot_scanner_core::{BacklightLevel, DisplayBands, DisplayHal};

const RETRY_INTERVAL: Duration = Duration::from_secs(5);
const BOOT_ATTEMPTS: u32 = 3;
const BOOT_RETRY_MS: u32 = 200;

fn wait_ms(ms: u32) {
    FreeRtos::delay_ms(ms);
}

pub struct U120Display<B: I2cBus> {
    bus: B,
    panel: U120,
    /// 63 KB of pixels, allocated once.
    frame: Frame,
    ready: bool,
    last_attempt: Option<Instant>,
    logged_missing: bool,
    level: BacklightLevel,
    last: Option<DisplayBands>,
}

impl<B: I2cBus> U120Display<B> {
    pub fn new(bus: B) -> Self {
        Self {
            bus,
            panel: U120::new(),
            frame: Frame::new(),
            ready: false,
            last_attempt: None,
            logged_missing: false,
            level: BacklightLevel::Full100,
            last: None,
        }
    }

    /// Starts the unit if it is not running and a retry is due. Returns whether it is running.
    fn ensure_ready(&mut self) -> bool {
        if self.ready {
            return true;
        }
        let due = self
            .last_attempt
            .is_none_or(|at| at.elapsed() >= RETRY_INTERVAL);
        if !due {
            return false;
        }
        self.last_attempt = Some(Instant::now());
        match self.panel.begin(&mut self.bus, &mut wait_ms) {
            Ok(version) => {
                log::info!("display: U120 ready (firmware {version})");
                self.logged_missing = false;
                self.ready = true;
                self.apply_backlight();
            }
            Err(err) if !self.logged_missing => {
                self.logged_missing = true;
                log::error!("display: {err}; will retry");
            }
            Err(err) => log::debug!("display: {err}"),
        }
        self.ready
    }

    fn apply_backlight(&mut self) {
        let level = brightness_for(self.level);
        if let Err(err) = self
            .panel
            .set_brightness(&mut self.bus, &mut wait_ms, level)
        {
            log::warn!("display: backlight {:?} failed: {err}", self.level);
            self.ready = false;
        }
    }
}

impl<B: I2cBus> DisplayHal for U120Display<B> {
    fn begin(&mut self) {
        // The unit powers up with the board and may still be starting; give it a moment before
        // falling back to the slow retry.
        for _ in 0..BOOT_ATTEMPTS {
            self.last_attempt = None;
            if self.ensure_ready() {
                return;
            }
            wait_ms(BOOT_RETRY_MS);
        }
    }

    fn render(&mut self, bands: &DisplayBands) {
        if self.last.as_ref() == Some(bands) {
            return;
        }
        draw_bands(&mut self.frame, bands).unwrap_or_else(|never| match never {});
        // One failed push (a loose cable, a unit that restarted) gets one immediate restart, since
        // nothing calls render again until the screen content changes.
        for attempt in 0..2 {
            if attempt > 0 {
                self.last_attempt = None;
            }
            if !self.ensure_ready() {
                return;
            }
            match self.panel.push(&mut self.bus, &mut wait_ms, &self.frame) {
                Ok(rows) => {
                    log::debug!("display: {rows} rows sent");
                    self.last = Some(bands.clone());
                    return;
                }
                Err(err) => {
                    log::warn!("display: {err}; starting the screen again");
                    self.ready = false;
                    self.last = None;
                }
            }
        }
    }

    fn set_backlight(&mut self, level: BacklightLevel) {
        self.level = level;
        if self.ready {
            self.apply_backlight();
        }
    }
}
