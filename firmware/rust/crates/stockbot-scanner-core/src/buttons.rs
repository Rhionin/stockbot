//! StickS3 button debounce and edge decoding (#744, part 1).
//!
//! The firmware reads the raw pin levels every pass of its main loop and feeds them to
//! [`ButtonPoller::sample`]; the decoded [`ButtonEvent`]s go to the controller through
//! [`dispatch`]. Kept free of hardware so the host tests cover it.

use crate::ring_buffer::RingStorage;
use crate::scanner_controller::ScannerController;

/// A raw level change must hold this long before it counts.
pub const DEBOUNCE_MS: u64 = 20;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ButtonEvent {
    /// The trigger button (BtnA) changed to this debounced state (`true` is pressed).
    TriggerChanged(bool),
    /// The mode button (BtnB) was pressed.
    ModeDown,
    /// The mode button is still held; sent on every sample while it is.
    ModeHoldPoll,
    /// The mode button was released.
    ModeUp,
}

#[derive(Clone, Copy, Debug, Default)]
struct Debounced {
    stable: bool,
    raw: bool,
    raw_changed_ms: u64,
}

impl Debounced {
    /// Returns the new stable state when it flips on this sample.
    fn sample(&mut self, now_ms: u64, pressed: bool) -> Option<bool> {
        if pressed != self.raw {
            self.raw = pressed;
            self.raw_changed_ms = now_ms;
        }
        if self.raw != self.stable && now_ms.wrapping_sub(self.raw_changed_ms) >= DEBOUNCE_MS {
            self.stable = self.raw;
            return Some(self.stable);
        }
        None
    }
}

/// Debounces the trigger and mode buttons and decodes their edges.
#[derive(Clone, Copy, Debug, Default)]
pub struct ButtonPoller {
    trigger: Debounced,
    mode: Debounced,
}

impl ButtonPoller {
    pub fn new() -> Self {
        Self::default()
    }

    /// `trigger_pressed` and `mode_pressed` are raw levels already converted to "pressed".
    /// Events are appended to `out`.
    pub fn sample(
        &mut self,
        now_ms: u64,
        trigger_pressed: bool,
        mode_pressed: bool,
        out: &mut Vec<ButtonEvent>,
    ) {
        if let Some(pressed) = self.trigger.sample(now_ms, trigger_pressed) {
            out.push(ButtonEvent::TriggerChanged(pressed));
        }
        match self.mode.sample(now_ms, mode_pressed) {
            Some(true) => out.push(ButtonEvent::ModeDown),
            Some(false) => out.push(ButtonEvent::ModeUp),
            None if self.mode.stable => out.push(ButtonEvent::ModeHoldPoll),
            None => {}
        }
    }
}

/// Hands one decoded event to the controller.
pub fn dispatch<RS: RingStorage>(
    controller: &mut ScannerController<'_, RS>,
    event: ButtonEvent,
    now_ms: u64,
) {
    match event {
        ButtonEvent::TriggerChanged(pressed) => {
            controller.on_trigger_button_changed(pressed, now_ms)
        }
        ButtonEvent::ModeDown => controller.on_mode_button_down(now_ms),
        ButtonEvent::ModeHoldPoll => controller.on_mode_button_hold_poll(now_ms),
        ButtonEvent::ModeUp => controller.on_mode_button_up(now_ms),
    }
}
