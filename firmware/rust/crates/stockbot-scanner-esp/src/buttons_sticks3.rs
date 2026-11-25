//! StickS3 buttons (#744, part 1): BtnA (GPIO11) is the trigger and BtnB (GPIO12) is the mode
//! button. Both are active low (M5Unified `src/M5Unified.inl`; see `HARDWARE-sticks3.md`).
//!
//! The internal pull-up is enabled in case the board has no external one. Debouncing and edge
//! decoding live in `stockbot_scanner_core::buttons`.

use esp_idf_svc::hal::gpio::{Gpio11, Gpio12, Input, PinDriver, Pull};
use esp_idf_svc::sys::EspError;

pub struct ButtonPeripherals {
    pub trigger: Gpio11,
    pub mode: Gpio12,
}

pub struct Sticks3Buttons {
    trigger: PinDriver<'static, Gpio11, Input>,
    mode: PinDriver<'static, Gpio12, Input>,
}

impl Sticks3Buttons {
    pub fn new(p: ButtonPeripherals) -> Result<Self, EspError> {
        let mut trigger = PinDriver::input(p.trigger)?;
        trigger.set_pull(Pull::Up)?;
        let mut mode = PinDriver::input(p.mode)?;
        mode.set_pull(Pull::Up)?;
        Ok(Self { trigger, mode })
    }

    /// Raw "pressed" levels: `(trigger, mode)`. A pressed button reads low.
    pub fn read_pressed(&self) -> (bool, bool) {
        (self.trigger.is_low(), self.mode.is_low())
    }
}
