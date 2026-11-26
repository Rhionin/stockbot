//! The AtomS3 Lite's one front button (GPIO41, active low; #747). It is both the trigger and the
//! mode button, as in the old C++ pin map (`PIN_BTN_TRIGGER` and `PIN_BTN_MODE` were both 41), so
//! `main` feeds the same level to both inputs of the core debouncer:
//! - a short press toggles stock in and out (and, before pairing, scans while held);
//! - a 5 s hold starts Wi-Fi setup and a 10 s hold erases the device (`button_hold_tracker.rs`).
//!
//! Once paired the AtomS3 is always on USB power, so the reader is already scanning hands free and
//! the trigger half of a press changes nothing.

use esp_idf_svc::hal::gpio::{Gpio41, Input, PinDriver, Pull};
use esp_idf_svc::sys::EspError;

pub struct AtomS3Button {
    pin: PinDriver<'static, Gpio41, Input>,
}

impl AtomS3Button {
    pub fn new(pin: Gpio41) -> Result<Self, EspError> {
        let mut pin = PinDriver::input(pin)?;
        pin.set_pull(Pull::Up)?;
        Ok(Self { pin })
    }

    /// Raw "pressed" level; the button reads low when pressed.
    pub fn is_pressed(&self) -> bool {
        self.pin.is_low()
    }
}
