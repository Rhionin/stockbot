//! Scanner UI / mode state (ported from `firmware/src/core/scanner_state.*`).
//!
//! Inactivity and dock timing use `uint64_t` wrapping subtraction
//! (`now_ms - last_activity_ms` / `now_ms - vbus_transition_ms`), matching
//! `firmware/src/core/scanner_state.cpp`.
use crate::barcode::Direction;

#[inline]
fn millis_delta(now_ms: u64, earlier_ms: u64) -> u64 {
    now_ms.wrapping_sub(earlier_ms)
}

pub const DOCK_SUPPRESS_MS: u64 = 1500;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CommitMode {
    Queue,
    Immediate,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BacklightLevel {
    Full100,
    Dim20,
    Off0,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ButtonHoldGesture {
    ShortPress,
    WifiUpdate,
    FactoryReset,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ModeChime {
    None,
    RisingStockIn,
    FallingStockOut,
    ModeRevert,
}

#[derive(Debug, Clone, Copy)]
pub struct ModeButtonResult {
    pub woke_backlight: bool,
    pub toggled_direction: bool,
    pub chime: ModeChime,
}

#[derive(Debug, Clone)]
pub struct ScannerState {
    pub operational: crate::barcode::OperationalState,
    pub active_direction: Direction,
    pub default_direction: Direction,
    pub inactivity_revert_seconds: u32,
    pub commit_mode: CommitMode,
    pub config_version: u32,
    pub backlight: BacklightLevel,
    pub last_activity_ms: u64,
    pub vbus_transition_ms: u64,
    pub vbus_present: bool,
    pub mode_button_down_ms: u64,
}

impl Default for ScannerState {
    fn default() -> Self {
        Self {
            operational: crate::barcode::OperationalState::Unprovisioned,
            active_direction: Direction::StockOut,
            default_direction: Direction::StockOut,
            inactivity_revert_seconds: 300,
            commit_mode: CommitMode::Queue,
            config_version: 0,
            backlight: BacklightLevel::Full100,
            last_activity_ms: 0,
            vbus_transition_ms: 0,
            vbus_present: false,
            mode_button_down_ms: 0,
        }
    }
}

impl ScannerState {
    pub fn wake_backlight(&mut self, now_ms: u64) {
        self.backlight = BacklightLevel::Full100;
        self.last_activity_ms = now_ms;
    }

    pub fn on_mode_button_press(&mut self, now_ms: u64) -> ModeButtonResult {
        if matches!(self.backlight, BacklightLevel::Dim20 | BacklightLevel::Off0) {
            self.wake_backlight(now_ms);
            return ModeButtonResult {
                woke_backlight: true,
                toggled_direction: false,
                chime: ModeChime::None,
            };
        }
        let mut result = ModeButtonResult {
            woke_backlight: false,
            toggled_direction: true,
            chime: ModeChime::None,
        };
        if self.active_direction == Direction::StockOut {
            self.set_direction(Direction::StockIn, now_ms, true);
            result.chime = ModeChime::RisingStockIn;
        } else {
            self.set_direction(Direction::StockOut, now_ms, true);
            result.chime = ModeChime::FallingStockOut;
        }
        result
    }

    pub fn on_scan_or_activity(&mut self, now_ms: u64) {
        self.last_activity_ms = now_ms;
        self.backlight = BacklightLevel::Full100;
    }

    pub fn set_direction(&mut self, dir: Direction, now_ms: u64, play_chime: bool) {
        self.active_direction = dir;
        self.on_scan_or_activity(now_ms);
        if play_chime {
            let _ = if dir == Direction::StockIn {
                ModeChime::RisingStockIn
            } else {
                ModeChime::FallingStockOut
            };
        }
    }

    pub fn tick_inactivity_revert(&mut self, now_ms: u64) -> Option<ModeChime> {
        if self.active_direction == self.default_direction {
            return None;
        }
        if self.last_activity_ms == 0 {
            self.last_activity_ms = now_ms;
            return None;
        }
        let threshold_ms = u64::from(self.inactivity_revert_seconds) * 1000;
        if millis_delta(now_ms, self.last_activity_ms) >= threshold_ms {
            self.active_direction = self.default_direction;
            self.last_activity_ms = now_ms;
            return Some(ModeChime::ModeRevert);
        }
        None
    }

    pub fn on_vbus_transition(&mut self, present: bool, now_ms: u64) {
        self.vbus_present = present;
        self.vbus_transition_ms = now_ms;
    }

    pub fn is_scan_suppressed(&self, now_ms: u64) -> bool {
        if self.vbus_transition_ms == 0 {
            return false;
        }
        millis_delta(now_ms, self.vbus_transition_ms) < DOCK_SUPPRESS_MS
    }

    pub fn classify_button_hold(hold_ms: u64) -> ButtonHoldGesture {
        if hold_ms >= 10_000 {
            ButtonHoldGesture::FactoryReset
        } else if hold_ms >= 5000 {
            ButtonHoldGesture::WifiUpdate
        } else {
            ButtonHoldGesture::ShortPress
        }
    }
}
