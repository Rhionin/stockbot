//! M5PM1 power-management chip driver (M5StickS3), pure over a tiny blocking I2C trait.
//!
//! Register facts come from M5Stack's public M5Unified and M5GFX sources, read 2026-11-24:
//! - <https://github.com/m5stack/M5Unified/blob/master/src/utility/power/M5PM1_Class.hpp>
//! - <https://github.com/m5stack/M5Unified/blob/master/src/utility/power/M5PM1_Class.inl>
//! - M5GFX `pmic_ops.hpp` (`pmic_ops::sticks3_power_on`) in <https://github.com/m5stack/M5GFX>
//!
//! See `HARDWARE-sticks3.md` in the ESP crate for the full table and which facts were verbatim.
//!
//! UNVERIFIED on the bench (#741): the charge-pin polarity ([`CHARGE_PIN_ACTIVE_LOW`]) and the
//! battery-percent formula, which was read from a summary of M5Unified's `Power_Class` rather than
//! its verbatim text. The PM1 has no charge-status register (M5Unified's `isCharging` is a stub
//! that returns false), so charging is inferred from PM1 GPIO0 plus VBUS.

use crate::scanner_ports::PowerStatus;

pub use crate::i2c::{I2cBus, I2cError};

/// 7-bit I2C address of the M5PM1.
pub const M5PM1_ADDR: u8 = 0x6E;

/// Device id register; M5GFX expects `0x2050` (16-bit little endian).
pub const REG_DEVICE_ID: u8 = 0x00;
/// Expected value of [`REG_DEVICE_ID`].
pub const DEVICE_ID_EXPECTED: u16 = 0x2050;
/// Power source flags: bit0 5V VIN valid, bit1 5VINOUT valid (0 while the boost is on), bit2 VBAT valid.
pub const REG_PWR_SRC: u8 = 0x04;
/// Power config: bit0 CHG_EN, bit1 DCDC_EN, bit2 LDO_EN, bit3 BOOST_EN (5 V Grove output), bit4 LED_EN.
pub const REG_PWR_CFG: u8 = 0x06;
/// Write `0x00` to disable the I2C idle sleep.
pub const REG_I2C_CFG: u8 = 0x09;
/// Write `0x00` to disable the watchdog.
pub const REG_WDT_CNT: u8 = 0x0A;
/// System command register; [`SYS_CMD_SHUTDOWN`] powers the board off.
pub const REG_SYS_CMD: u8 = 0x0C;
/// GPIO mode, one bit per PM1 GPIO: 1 = output.
pub const REG_GPIO_MODE: u8 = 0x10;
/// GPIO output level, one bit per PM1 GPIO.
pub const REG_GPIO_OUT: u8 = 0x11;
/// GPIO input level, one bit per PM1 GPIO.
pub const REG_GPIO_IN: u8 = 0x12;
/// GPIO drive, one bit per PM1 GPIO: 0 = push-pull.
pub const REG_GPIO_DRV: u8 = 0x13;
/// GPIO function select for GPIO0..3 as M5Unified uses it: a cleared bit is the plain GPIO function.
pub const REG_GPIO_FUNC0: u8 = 0x16;
/// Battery voltage, u16 little endian millivolts; 0 means the read failed or there is no battery.
pub const REG_VBAT_L: u8 = 0x22;
/// USB 5 V input voltage, u16 little endian millivolts.
pub const REG_VIN_L: u8 = 0x24;
/// 5 V output voltage, u16 little endian millivolts.
pub const REG_5VOUT_L: u8 = 0x26;

/// [`REG_SYS_CMD`] value that shuts the board down.
pub const SYS_CMD_SHUTDOWN: u8 = 0xA1;
/// [`REG_PWR_SRC`] bit0: the 5 V VIN (USB) input is valid.
pub const PWR_SRC_VIN_VALID: u8 = 0x01;
/// [`REG_PWR_CFG`] bit3: the boost converter drives the 5 V Grove output.
pub const PWR_CFG_BOOST_EN: u8 = 0x08;

/// PM1 GPIO0 (bit mask): input carrying the charge state on the StickS3 (M5Unified `PM1 G0`).
pub const PM1_GPIO0: u8 = 0x01;
/// PM1 GPIO2 (bit mask): LCD power (M5GFX `sticks3_power_on`), driven high.
pub const PM1_GPIO_LCD_POWER: u8 = 0x04;
/// PM1 GPIO3 (bit mask): speaker amplifier enable (M5Unified `_begin`, "PA Control"), boots low.
pub const PM1_GPIO_AMP_ENABLE: u8 = 0x08;

/// UNVERIFIED (bench check, #741): whether PM1 GPIO0 reads low while the battery charges.
/// M5Unified marks the StickS3 charge state as `PM1 G0` without stating the polarity here;
/// active low is the common charger STAT convention. Flip this constant if the bench disagrees.
pub const CHARGE_PIN_ACTIVE_LOW: bool = true;

/// Battery millivolts mapped to 0 percent (M5Unified `Power_Class`, summarised).
pub const BATTERY_EMPTY_MV: i32 = 3300;
/// Upper bound in the percent formula. M5Unified's formula subtracts 3300 in the numerator but
/// 3350 in the denominator; that oddity is copied exactly and must not be "fixed" silently.
pub const BATTERY_SPAN_HIGH_MV: i32 = 4150;
/// Lower bound in the denominator of the percent formula (see [`BATTERY_SPAN_HIGH_MV`]).
pub const BATTERY_SPAN_LOW_MV: i32 = 3350;

/// What [`begin`] learned; the caller logs a device-id mismatch (this crate has no logger).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct BeginReport {
    pub device_id: u16,
}

impl BeginReport {
    pub fn device_id_matches(&self) -> bool {
        self.device_id == DEVICE_ID_EXPECTED
    }
}

/// Raw readings of one status poll.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Pm1Status {
    pub vbat_mv: u16,
    pub vin_mv: u16,
    pub pwr_src: u8,
    pub gpio_in: u8,
}

fn read_reg(bus: &mut dyn I2cBus, reg: u8) -> Result<u8, I2cError> {
    let mut buf = [0u8; 1];
    bus.write_read(M5PM1_ADDR, &[reg], &mut buf)?;
    Ok(buf[0])
}

fn read_u16_le(bus: &mut dyn I2cBus, reg: u8) -> Result<u16, I2cError> {
    let mut buf = [0u8; 2];
    bus.write_read(M5PM1_ADDR, &[reg], &mut buf)?;
    Ok(u16::from_le_bytes(buf))
}

fn write_reg(bus: &mut dyn I2cBus, reg: u8, value: u8) -> Result<(), I2cError> {
    bus.write(M5PM1_ADDR, &[reg, value])
}

/// Read-modify-write of one register: only the bits in `mask` change.
fn update_bits(bus: &mut dyn I2cBus, reg: u8, mask: u8, set: bool) -> Result<(), I2cError> {
    let old = read_reg(bus, reg)?;
    let new = if set { old | mask } else { old & !mask };
    write_reg(bus, reg, new)
}

/// Brings the chip to the state M5Unified leaves it in on a StickS3: idle sleep and watchdog off,
/// GPIO0 an input, LCD power (GPIO2) on, amplifier enable (GPIO3) off.
pub fn begin(bus: &mut dyn I2cBus) -> Result<BeginReport, I2cError> {
    let device_id = read_u16_le(bus, REG_DEVICE_ID)?;
    write_reg(bus, REG_I2C_CFG, 0x00)?;
    write_reg(bus, REG_WDT_CNT, 0x00)?;

    // GPIO0: plain GPIO function, input.
    update_bits(bus, REG_GPIO_FUNC0, PM1_GPIO0, false)?;
    update_bits(bus, REG_GPIO_MODE, PM1_GPIO0, false)?;

    // GPIO2 (LCD power) and GPIO3 (amplifier): same order as M5Unified: function, mode, drive, level.
    for (mask, high) in [(PM1_GPIO_LCD_POWER, true), (PM1_GPIO_AMP_ENABLE, false)] {
        update_bits(bus, REG_GPIO_FUNC0, mask, false)?;
        update_bits(bus, REG_GPIO_MODE, mask, true)?;
        update_bits(bus, REG_GPIO_DRV, mask, false)?;
        update_bits(bus, REG_GPIO_OUT, mask, high)?;
    }
    Ok(BeginReport { device_id })
}

pub fn read_status(bus: &mut dyn I2cBus) -> Result<Pm1Status, I2cError> {
    Ok(Pm1Status {
        vbat_mv: read_u16_le(bus, REG_VBAT_L)?,
        vin_mv: read_u16_le(bus, REG_VIN_L)?,
        pwr_src: read_reg(bus, REG_PWR_SRC)?,
        gpio_in: read_reg(bus, REG_GPIO_IN)?,
    })
}

/// Whether the charge pin level means "charging" for the given polarity.
pub fn charge_pin_says_charging(gpio_in: u8, active_low: bool) -> bool {
    let high = gpio_in & PM1_GPIO0 != 0;
    high != active_low
}

/// Battery percent from millivolts, `None` when the gauge reads 0 (failed read or no battery).
pub fn battery_percent(vbat_mv: u16) -> Option<i32> {
    if vbat_mv == 0 {
        return None;
    }
    let level = (i32::from(vbat_mv) - BATTERY_EMPTY_MV) * 100
        / (BATTERY_SPAN_HIGH_MV - BATTERY_SPAN_LOW_MV);
    Some(level.clamp(0, 100))
}

pub fn power_status(s: &Pm1Status) -> PowerStatus {
    let vbus_present = s.pwr_src & PWR_SRC_VIN_VALID != 0;
    PowerStatus {
        vbus_present,
        battery_pct: battery_percent(s.vbat_mv),
        charging: vbus_present && charge_pin_says_charging(s.gpio_in, CHARGE_PIN_ACTIVE_LOW),
    }
}

/// Switches the 5 V Grove output (boost converter) on or off.
pub fn set_ext_output(bus: &mut dyn I2cBus, enabled: bool) -> Result<(), I2cError> {
    update_bits(bus, REG_PWR_CFG, PWR_CFG_BOOST_EN, enabled)
}

/// Powers the board off. Nothing calls this yet.
pub fn shutdown(bus: &mut dyn I2cBus) -> Result<(), I2cError> {
    write_reg(bus, REG_SYS_CMD, SYS_CMD_SHUTDOWN)
}
