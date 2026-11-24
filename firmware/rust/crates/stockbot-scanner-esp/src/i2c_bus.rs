//! The StickS3 internal I2C bus (I2C1, SDA GPIO47, SCL GPIO48, 100 kHz) as a core `I2cBus`.
//! Pins and speed: M5Unified `_pin_table_i2c_ex_in` and M5GFX `esp32s3_board_data.hpp`; see
//! `HARDWARE-sticks3.md`.

use std::time::Duration;

use esp_idf_svc::hal::delay::TickType;
use esp_idf_svc::hal::gpio::{InputPin, OutputPin};
use esp_idf_svc::hal::i2c::{config::Config, I2c, I2cDriver};
use esp_idf_svc::hal::peripheral::Peripheral;
use esp_idf_svc::hal::units::FromValueType;
use esp_idf_svc::sys::{EspError, TickType_t};
use stockbot_scanner_core::m5pm1::{I2cBus, I2cError};

const BUS_SPEED_KHZ: u32 = 100;
const TRANSFER_TIMEOUT: Duration = Duration::from_millis(100);

pub struct EspI2cBus {
    driver: I2cDriver<'static>,
    timeout: TickType_t,
}

impl EspI2cBus {
    pub fn new<I2C: I2c>(
        i2c: impl Peripheral<P = I2C> + 'static,
        sda: impl Peripheral<P = impl InputPin + OutputPin> + 'static,
        scl: impl Peripheral<P = impl InputPin + OutputPin> + 'static,
    ) -> Result<Self, EspError> {
        let config = Config::new().baudrate(BUS_SPEED_KHZ.kHz().into());
        Ok(Self {
            driver: I2cDriver::new(i2c, sda, scl, &config)?,
            timeout: TickType::from(TRANSFER_TIMEOUT).ticks(),
        })
    }
}

/// The cause is dropped here; `Sticks3Power` logs failures at most once per 30 s.
fn bus_error(_err: EspError) -> I2cError {
    I2cError
}

impl I2cBus for EspI2cBus {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        self.driver
            .write(addr, bytes, self.timeout)
            .map_err(bus_error)
    }

    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError> {
        self.driver
            .write_read(addr, bytes, buf, self.timeout)
            .map_err(bus_error)
    }
}
