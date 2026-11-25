//! Minimal blocking I2C master shared by the register drivers ([`crate::m5pm1`], [`crate::u173`]):
//! implemented over esp-idf-hal on the device, faked in tests.

pub trait I2cBus {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError>;
    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError>;
}

/// Any bus failure (NACK, timeout, arbitration loss); the cause is logged by the bus itself.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct I2cError;

/// One bus handed to several drivers in the same thread, such as the U173 reader and the U120
/// screen on the AtomS3 Grove hub. Each call borrows the bus for that one transfer.
pub struct SharedI2cBus<B>(std::rc::Rc<std::cell::RefCell<B>>);

impl<B> SharedI2cBus<B> {
    pub fn new(bus: B) -> Self {
        Self(std::rc::Rc::new(std::cell::RefCell::new(bus)))
    }
}

impl<B> Clone for SharedI2cBus<B> {
    fn clone(&self) -> Self {
        Self(self.0.clone())
    }
}

impl<B: I2cBus> I2cBus for SharedI2cBus<B> {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        self.0.borrow_mut().write(addr, bytes)
    }

    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError> {
        self.0.borrow_mut().write_read(addr, bytes, buf)
    }
}
