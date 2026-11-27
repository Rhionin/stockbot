//! Bosch BMI270 IMU on the StickS3 internal I2C bus (#744, part 2), accelerometer only, pure over
//! the [`I2cBus`] trait. The firmware reads it for the docked jostle guard ([`crate::motion`]).
//!
//! Sources, read 2026-11-25:
//! - M5Unified `src/utility/imu/BMI270_Class.inl` and `.hpp` (MIT): the init order, the address
//!   probe (`0x69`, then `0x68`) and that M5Unified finds a BMI270 on the StickS3
//!   (`IMU_Class.inl`: "StickS3 BMI270 : X=+Y, Y=-X, Z=+Z").
//!   <https://github.com/m5stack/M5Unified/tree/master/src/utility/imu>
//! - Bosch `BMI270_SensorAPI` v2.86.1 (`bmi2.c`, `bmi2_defs.h`, BSD-3-Clause): register
//!   addresses, the chunked config upload and [`CONFIG_FILE`], which must be loaded before the
//!   sensor runs. <https://github.com/boschsensortec/BMI270_SensorAPI>
//!
//! [`CONFIG_FILE`] is Bosch's `bmi270_config_file[]`, byte for byte (M5Unified ships the same
//! bytes). Its licence is in `src/bmi270/LICENSE-BOSCH`.
//!
//! Axis order does not matter here: the guard uses only the length of the acceleration vector.
//!
//! UNVERIFIED on the bench: everything here is read from the sources above.

pub use crate::i2c::{I2cBus, I2cError};

/// Addresses in the order M5Unified tries them.
pub const BMI270_ADDRS: [u8; 2] = [0x69, 0x68];

pub const REG_CHIP_ID: u8 = 0x00;
pub const CHIP_ID: u8 = 0x24;
pub const REG_ACC_X_LSB: u8 = 0x0C;
pub const REG_INTERNAL_STATUS: u8 = 0x21;
pub const REG_ACC_RANGE: u8 = 0x41;
pub const REG_INIT_CTRL: u8 = 0x59;
pub const REG_INIT_ADDR_0: u8 = 0x5B;
pub const REG_INIT_DATA: u8 = 0x5E;
pub const REG_PWR_CONF: u8 = 0x7C;
pub const REG_PWR_CTRL: u8 = 0x7D;
pub const REG_CMD: u8 = 0x7E;

pub const CMD_SOFT_RESET: u8 = 0xB6;
/// `INTERNAL_STATUS` message bits.
pub const INTERNAL_STATUS_MESSAGE_MASK: u8 = 0x0F;
pub const INTERNAL_STATUS_INIT_OK: u8 = 0x01;
/// `PWR_CTRL` accelerometer enable; gyro, aux and temperature stay off.
pub const PWR_CTRL_ACC_EN: u8 = 0x04;
/// ±8 g, the BMI270 default and what M5Unified scales by (`8.0f / 32768.0f`).
pub const ACC_RANGE_8G: u8 = 0x02;
pub const LSB_PER_G: i32 = 4096;

pub static CONFIG_FILE: &[u8; 8192] = include_bytes!("bmi270/config.bin");

/// Bytes per config upload burst. Even, as the upload address counts 16-bit words, and small
/// enough that one burst stays well inside the bus timeout at 100 kHz.
pub const UPLOAD_CHUNK: usize = 64;

/// Bosch waits 2 ms after a soft reset and 450 µs after leaving power save.
pub const RESET_WAIT_MS: u32 = 2;
pub const POWER_SAVE_WAIT_MS: u32 = 1;
/// Bosch allows 20 ms for the config to start; polled in 1 ms steps.
pub const INIT_STATUS_POLLS: u32 = 20;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Bmi270Error {
    /// No BMI270 answered at either address.
    NotFound,
    Bus,
    /// The sensor did not report a good config load.
    InitFailed(u8),
}

impl From<I2cError> for Bmi270Error {
    fn from(_: I2cError) -> Self {
        Bmi270Error::Bus
    }
}

fn read_reg(bus: &mut dyn I2cBus, addr: u8, reg: u8) -> Result<u8, I2cError> {
    let mut buf = [0u8; 1];
    bus.write_read(addr, &[reg], &mut buf)?;
    Ok(buf[0])
}

fn write_reg(bus: &mut dyn I2cBus, addr: u8, reg: u8, value: u8) -> Result<(), I2cError> {
    bus.write(addr, &[reg, value])
}

/// Finds the sensor, loads [`CONFIG_FILE`] and turns the accelerometer on at ±8 g. Returns the
/// address it answered on. `delay_ms` blocks; this runs once at start-up.
pub fn begin(bus: &mut dyn I2cBus, delay_ms: &mut dyn FnMut(u32)) -> Result<u8, Bmi270Error> {
    let addr = BMI270_ADDRS
        .into_iter()
        .find(|&addr| read_reg(bus, addr, REG_CHIP_ID) == Ok(CHIP_ID))
        .ok_or(Bmi270Error::NotFound)?;

    write_reg(bus, addr, REG_CMD, CMD_SOFT_RESET)?;
    delay_ms(RESET_WAIT_MS);
    write_reg(bus, addr, REG_PWR_CONF, 0x00)?;
    delay_ms(POWER_SAVE_WAIT_MS);
    write_reg(bus, addr, REG_INIT_CTRL, 0x00)?;
    upload_config(bus, addr)?;
    write_reg(bus, addr, REG_INIT_CTRL, 0x01)?;

    let mut status = 0;
    for _ in 0..INIT_STATUS_POLLS {
        delay_ms(1);
        status = read_reg(bus, addr, REG_INTERNAL_STATUS)? & INTERNAL_STATUS_MESSAGE_MASK;
        if status == INTERNAL_STATUS_INIT_OK {
            break;
        }
    }
    if status != INTERNAL_STATUS_INIT_OK {
        return Err(Bmi270Error::InitFailed(status));
    }

    write_reg(bus, addr, REG_ACC_RANGE, ACC_RANGE_8G)?;
    write_reg(bus, addr, REG_PWR_CTRL, PWR_CTRL_ACC_EN)?;
    Ok(addr)
}

/// Bosch `upload_file`: each burst is preceded by its offset in 16-bit words, low nibble in
/// `INIT_ADDR_0` and the rest in `INIT_ADDR_1`.
fn upload_config(bus: &mut dyn I2cBus, addr: u8) -> Result<(), I2cError> {
    let mut frame = Vec::with_capacity(1 + UPLOAD_CHUNK);
    for (n, chunk) in CONFIG_FILE.chunks(UPLOAD_CHUNK).enumerate() {
        let word = n * UPLOAD_CHUNK / 2;
        bus.write(
            addr,
            &[REG_INIT_ADDR_0, (word & 0x0F) as u8, (word >> 4) as u8],
        )?;
        frame.clear();
        frame.push(REG_INIT_DATA);
        frame.extend_from_slice(chunk);
        bus.write(addr, &frame)?;
    }
    Ok(())
}

/// Acceleration in milli-g on the sensor's own axes.
pub fn read_accel_mg(bus: &mut dyn I2cBus, addr: u8) -> Result<[i32; 3], I2cError> {
    let mut buf = [0u8; 6];
    bus.write_read(addr, &[REG_ACC_X_LSB], &mut buf)?;
    let axis = |i: usize| i32::from(i16::from_le_bytes([buf[i], buf[i + 1]])) * 1000 / LSB_PER_G;
    Ok([axis(0), axis(2), axis(4)])
}
