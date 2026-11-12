//! Mode-button hold tracking (Wi‑Fi setup / factory reset thresholds).
//!
//! Held duration uses `uint64_t` wrapping subtraction (`now_ms - down_ms_`), same as
//! `firmware/src/core/button_hold_tracker.cpp` after `millis()` is widened in `main.cpp`.
#[inline]
fn millis_delta(now_ms: u64, earlier_ms: u64) -> u64 {
    now_ms.wrapping_sub(earlier_ms)
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ModeButtonHoldPhase {
    None,
    WifiSetupHold,
    EraseWarning,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ModeButtonReleaseAction {
    None,
    ShortPress,
    WifiSetup,
    FactoryReset,
}

pub struct ButtonHoldTracker {
    down: bool,
    down_ms: u64,
    phase: ModeButtonHoldPhase,
}

impl Default for ButtonHoldTracker {
    fn default() -> Self {
        Self::new()
    }
}

impl ButtonHoldTracker {
    pub fn new() -> Self {
        Self {
            down: false,
            down_ms: 0,
            phase: ModeButtonHoldPhase::None,
        }
    }

    pub fn on_press(&mut self, now_ms: u64) {
        self.down = true;
        self.down_ms = now_ms;
        self.phase = ModeButtonHoldPhase::None;
    }

    pub fn held_ms(&self, now_ms: u64) -> u64 {
        if !self.down {
            0
        } else {
            millis_delta(now_ms, self.down_ms)
        }
    }

    pub fn on_hold_progress(&mut self, now_ms: u64) -> Option<ModeButtonHoldPhase> {
        if !self.down {
            return None;
        }
        let held = millis_delta(now_ms, self.down_ms);
        if held < 5000 {
            return None;
        }
        if held >= 10_000 {
            if self.phase != ModeButtonHoldPhase::EraseWarning {
                self.phase = ModeButtonHoldPhase::EraseWarning;
                return Some(ModeButtonHoldPhase::EraseWarning);
            }
            return None;
        }
        if self.phase != ModeButtonHoldPhase::WifiSetupHold {
            self.phase = ModeButtonHoldPhase::WifiSetupHold;
            return Some(ModeButtonHoldPhase::WifiSetupHold);
        }
        None
    }

    pub fn on_release(&mut self, now_ms: u64) -> ModeButtonReleaseAction {
        if !self.down {
            return ModeButtonReleaseAction::None;
        }
        let held = millis_delta(now_ms, self.down_ms);
        self.down = false;
        self.phase = ModeButtonHoldPhase::None;
        if held < 5000 {
            ModeButtonReleaseAction::ShortPress
        } else if held >= 10_000 {
            ModeButtonReleaseAction::FactoryReset
        } else {
            ModeButtonReleaseAction::WifiSetup
        }
    }

    pub fn is_down(&self) -> bool {
        self.down
    }
}
