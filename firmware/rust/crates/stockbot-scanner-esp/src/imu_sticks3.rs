//! StickS3 BMI270 accelerometer for the docked jostle guard (#744, part 2), on the internal I2C
//! bus. The register work is `stockbot_scanner_core::bmi270`; the guard itself is
//! `stockbot_scanner_core::motion`, inside the controller.
//!
//! Without a working sensor the guard stays off and docked hands-free scanning works as before.

use std::time::{Duration, Instant};

use stockbot_scanner_core::bmi270::{self, Bmi270Error};
use stockbot_scanner_core::i2c::I2cBus;

/// 50 readings a second: a shake lasts far longer than this, and each read is a few hundred µs
/// on the 100 kHz bus.
const SAMPLE_INTERVAL_MS: u64 = 20;
const ERROR_LOG_INTERVAL: Duration = Duration::from_secs(30);

pub struct Sticks3Imu<B: I2cBus> {
    bus: B,
    addr: Option<u8>,
    next_sample_ms: u64,
    last_error_log: Option<Instant>,
}

impl<B: I2cBus> Sticks3Imu<B> {
    pub fn new(bus: B) -> Self {
        Self {
            bus,
            addr: None,
            next_sample_ms: 0,
            last_error_log: None,
        }
    }

    /// Loads the sensor's 8 KiB config over I2C, which takes most of a second. Runs once, after
    /// the controller has set up the power chip.
    pub fn begin(&mut self) {
        let mut delay = |ms: u32| std::thread::sleep(Duration::from_millis(u64::from(ms)));
        match bmi270::begin(&mut self.bus, &mut delay) {
            Ok(addr) => {
                self.addr = Some(addr);
                log::info!("imu: BMI270 ready at {addr:#04x}");
            }
            Err(Bmi270Error::NotFound) => {
                log::error!("imu: no BMI270 on the internal I2C bus; the dock shake guard is off")
            }
            Err(Bmi270Error::Bus) => {
                log::error!("imu: BMI270 set-up failed on the I2C bus; the dock shake guard is off")
            }
            Err(Bmi270Error::InitFailed(status)) => log::error!(
                "imu: BMI270 did not start (status {status:#04x}); the dock shake guard is off"
            ),
        }
    }

    /// A reading in milli-g when one is due, at most every [`SAMPLE_INTERVAL_MS`].
    pub fn sample(&mut self, now_ms: u64) -> Option<[i32; 3]> {
        let addr = self.addr?;
        if now_ms < self.next_sample_ms {
            return None;
        }
        self.next_sample_ms = now_ms + SAMPLE_INTERVAL_MS;
        match bmi270::read_accel_mg(&mut self.bus, addr) {
            Ok(accel) => Some(accel),
            Err(_) => {
                let now = Instant::now();
                if self
                    .last_error_log
                    .is_none_or(|at| now.duration_since(at) >= ERROR_LOG_INTERVAL)
                {
                    self.last_error_log = Some(now);
                    log::warn!("imu: BMI270 read failed");
                }
                None
            }
        }
    }
}
