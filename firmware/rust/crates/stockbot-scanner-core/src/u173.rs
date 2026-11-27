//! M5Stack U173 "Unit QRCode" barcode reader over I2C, pure over the [`I2cBus`] trait.
//!
//! Register facts come from M5Stack's official library and I2C protocol sheet (V2, 2023-09-07):
//! - <https://github.com/m5stack/M5Unit-QRCode>
//! - <https://docs.m5stack.com/en/unit/qrcode>
//!
//! Wire format: I2C address `0x21` at 100 kHz. Every register address is 16 bits, sent low byte
//! first (the library's `writeBytes`/`readBytes` send `reg & 0xff`, then `reg >> 8`, then the
//! data). A read is a write of those two address bytes followed by a read.
//!
//! The unit has no symbology, 1D-only or QR-disable register. The register at `0x0030` is the
//! trigger mode. So `ScannerHal::enable_1d_only` is a no-op on this reader, and ignoring QR codes
//! stays the job of the core barcode classifier, which accepts only 1D product barcodes, control
//! codes, pairing codes and (before setup) the setup payload.
//!
//! The reader runs in manual trigger mode: [`set_decoding`] starts and stops decoding, so it scans
//! only while the controller asks for it (docked or trigger button held).
//!
//! UNVERIFIED on the bench (#743): everything here is read from the library and sheet.

pub use crate::i2c::{I2cBus, I2cError};

/// 7-bit I2C address of the U173.
pub const U173_ADDR: u8 = 0x21;

/// Trigger, only effective in manual trigger mode: 1 starts continuous decoding, 0 stops it.
pub const REG_TRIGGER: u16 = 0x0000;
/// Status: 0 not ready, 1 data good, 2 need to read again. Resets after the data is read, or when
/// 0 is written.
pub const REG_STATUS: u16 = 0x0010;
/// Barcode length, 2 bytes little endian (Length-L, Length-H).
pub const REG_LENGTH: u16 = 0x0020;
/// Trigger mode: 0 auto (default), 1 manual.
pub const REG_TRIGGER_MODE: u16 = 0x0030;
/// The unit's own trigger button: 0 pressed, 1 not pressed. Not used by the firmware.
pub const REG_BUTTON: u16 = 0x0040;
/// Firmware version, 1 byte. The protocol sheet's row label says `0x00F0`, but its cell sits in
/// column E and the library uses `0x00FE`.
pub const REG_FW_VERSION: u16 = 0x00FE;
/// I2C address register (read/write). Never written by the firmware.
pub const REG_I2C_ADDRESS: u16 = 0x00FF;
/// Start of the barcode data, `0x1000..=0x11FF`.
pub const REG_DATA: u16 = 0x1000;

/// Longest barcode the unit reports (the data window is 512 bytes).
pub const MAX_DATA_LEN: usize = 512;

/// [`REG_STATUS`] value: no data yet.
pub const STATUS_NOT_READY: u8 = 0;
/// [`REG_STATUS`] value: a barcode is ready to read.
pub const STATUS_DATA_GOOD: u8 = 1;
/// [`REG_STATUS`] value: the unit wants the data read again.
pub const STATUS_READ_AGAIN: u8 = 2;

/// [`REG_TRIGGER_MODE`] value for manual trigger.
pub const TRIGGER_MODE_MANUAL: u8 = 1;

/// Reads `buf.len()` bytes starting at `reg`; the 16-bit address goes out low byte first.
pub fn read_reg(bus: &mut dyn I2cBus, reg: u16, buf: &mut [u8]) -> Result<(), I2cError> {
    bus.write_read(U173_ADDR, &reg.to_le_bytes(), buf)
}

/// Writes `data` starting at `reg`; the 16-bit address goes out low byte first.
pub fn write_reg(bus: &mut dyn I2cBus, reg: u16, data: &[u8]) -> Result<(), I2cError> {
    let mut frame = Vec::with_capacity(2 + data.len());
    frame.extend_from_slice(&reg.to_le_bytes());
    frame.extend_from_slice(data);
    bus.write(U173_ADDR, &frame)
}

fn read_u8(bus: &mut dyn I2cBus, reg: u16) -> Result<u8, I2cError> {
    let mut buf = [0u8; 1];
    read_reg(bus, reg, &mut buf)?;
    Ok(buf[0])
}

/// Puts the unit in manual trigger mode with decoding stopped and returns its firmware version.
/// The caller waits 10 ms after bus start first, as the library does.
pub fn begin(bus: &mut dyn I2cBus) -> Result<u8, I2cError> {
    write_reg(bus, REG_TRIGGER_MODE, &[TRIGGER_MODE_MANUAL])?;
    write_reg(bus, REG_TRIGGER, &[0])?;
    read_u8(bus, REG_FW_VERSION)
}

/// Starts (`true`) or stops (`false`) continuous decoding. Needs manual trigger mode.
pub fn set_decoding(bus: &mut dyn I2cBus, on: bool) -> Result<(), I2cError> {
    write_reg(bus, REG_TRIGGER, &[u8::from(on)])
}

/// Reads one finished barcode, if the unit has one.
///
/// Status 1 means data is ready: the length and data are read, which resets the status. A length
/// of 0 or more than [`MAX_DATA_LEN`] is bogus, so the status is cleared and nothing is returned.
/// Status 2 ("need to read again") and every other status return `None`; the next poll looks again.
pub fn poll(bus: &mut dyn I2cBus) -> Result<Option<String>, I2cError> {
    if read_u8(bus, REG_STATUS)? != STATUS_DATA_GOOD {
        return Ok(None);
    }
    let mut len_bytes = [0u8; 2];
    read_reg(bus, REG_LENGTH, &mut len_bytes)?;
    let len = usize::from(u16::from_le_bytes(len_bytes));
    if len == 0 || len > MAX_DATA_LEN {
        write_reg(bus, REG_STATUS, &[STATUS_NOT_READY])?;
        return Ok(None);
    }
    let mut data = vec![0u8; len];
    read_reg(bus, REG_DATA, &mut data)?;
    let text = String::from_utf8_lossy(&data);
    let line = text.trim_end_matches(['\r', '\n']);
    Ok((!line.is_empty()).then(|| line.to_owned()))
}
