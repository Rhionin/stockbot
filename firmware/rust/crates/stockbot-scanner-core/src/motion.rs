//! Docked jostle guard (#744, part 2): while the StickS3 sits on its USB dock in hands-free mode,
//! a moving board must not auto-scan. The old C++ U173 poll skipped the trigger when
//! `|‖a‖ − 1 g| ≥ 0.08 g`; that threshold is kept here, in whole milli-g so the comparison is
//! exact.
//!
//! One over-threshold sample keeps the guard on for [`MOTION_HOLD_MS`]. A shaken dock swings
//! through 1 g on every shake, so without the hold the reader would flick on between shakes.

/// `|‖a‖ − 1 g|` at or above this counts as moving (0.08 g).
pub const MOTION_THRESHOLD_MG: i32 = 80;

/// How long the guard stays on after the last moving sample.
pub const MOTION_HOLD_MS: u64 = 500;

const ONE_G_MG: i64 = 1000;

/// `true` when `|‖a‖ − 1 g| ≥ 0.08 g`, with `accel_mg` in milli-g on any three orthogonal axes.
pub fn exceeds_motion_threshold(accel_mg: [i32; 3]) -> bool {
    let squared: i64 = accel_mg.iter().map(|&a| i64::from(a) * i64::from(a)).sum();
    let high = ONE_G_MG + i64::from(MOTION_THRESHOLD_MG);
    let low = ONE_G_MG - i64::from(MOTION_THRESHOLD_MG);
    squared >= high * high || squared <= low * low
}

#[derive(Clone, Copy, Debug, Default)]
pub struct MotionGuard {
    last_motion_ms: Option<u64>,
}

impl MotionGuard {
    pub fn sample(&mut self, accel_mg: [i32; 3], now_ms: u64) {
        if exceeds_motion_threshold(accel_mg) {
            self.last_motion_ms = Some(now_ms);
        }
    }

    pub fn is_moving(&self, now_ms: u64) -> bool {
        self.last_motion_ms
            .is_some_and(|at| now_ms.wrapping_sub(at) < MOTION_HOLD_MS)
    }
}
