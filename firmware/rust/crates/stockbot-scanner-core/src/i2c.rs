//! Minimal blocking I2C master shared by the register drivers ([`crate::m5pm1`], [`crate::u173`]):
//! implemented over esp-idf-hal on the device, faked in tests.

pub trait I2cBus {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError>;
    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError>;
}

/// Any bus failure (NACK, timeout, arbitration loss); the cause is logged by the bus itself.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct I2cError;
