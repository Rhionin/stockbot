//! Monotonic milliseconds since boot, from `std::time::Instant`.

use std::time::Instant;

use stockbot_scanner_core::ClockHal;

pub struct BootClock {
    boot: Instant,
}

impl BootClock {
    pub fn new() -> Self {
        Self {
            boot: Instant::now(),
        }
    }
}

impl ClockHal for BootClock {
    fn now_ms(&self) -> u64 {
        u64::try_from(self.boot.elapsed().as_millis()).unwrap_or(u64::MAX)
    }
}
