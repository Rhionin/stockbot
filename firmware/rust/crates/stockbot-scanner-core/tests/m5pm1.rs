//! M5PM1 driver against a fake register file.

use stockbot_scanner_core::m5pm1::{
    self, battery_percent, charge_pin_says_charging, I2cBus, I2cError, Pm1Status,
    CHARGE_PIN_ACTIVE_LOW, M5PM1_ADDR,
};
use stockbot_scanner_core::PowerStatus;

struct FakeBus {
    regs: [u8; 256],
    writes: Vec<(u8, u8)>,
    /// Every transaction fails once this many have succeeded.
    fail_after: Option<usize>,
    transactions: usize,
}

impl FakeBus {
    fn filled(value: u8) -> Self {
        Self {
            regs: [value; 256],
            writes: Vec::new(),
            fail_after: None,
            transactions: 0,
        }
    }

    fn gate(&mut self) -> Result<(), I2cError> {
        if self.fail_after.is_some_and(|n| self.transactions >= n) {
            return Err(I2cError);
        }
        self.transactions += 1;
        Ok(())
    }
}

impl I2cBus for FakeBus {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        assert_eq!(addr, M5PM1_ADDR);
        self.gate()?;
        assert_eq!(bytes.len(), 2, "single register writes only");
        self.regs[bytes[0] as usize] = bytes[1];
        self.writes.push((bytes[0], bytes[1]));
        Ok(())
    }

    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError> {
        assert_eq!(addr, M5PM1_ADDR);
        self.gate()?;
        assert_eq!(bytes.len(), 1);
        let start = bytes[0] as usize;
        buf.copy_from_slice(&self.regs[start..start + buf.len()]);
        Ok(())
    }
}

/// Registers `begin` is allowed to change, with the exact bits it may touch.
const GPIO0: u8 = 0x01;
const GPIO2: u8 = 0x04;
const GPIO3: u8 = 0x08;

fn assert_begin_result(fill: u8) {
    let mut bus = FakeBus::filled(fill);
    bus.regs[0x00] = 0x50;
    bus.regs[0x01] = 0x20;
    let report = m5pm1::begin(&mut bus).expect("begin");
    assert_eq!(report.device_id, 0x2050);
    assert!(report.device_id_matches());

    assert_eq!(bus.regs[0x09], 0x00, "I2C_CFG idle sleep off");
    assert_eq!(bus.regs[0x0A], 0x00, "WDT_CNT watchdog off");

    let touched_func = GPIO0 | GPIO2 | GPIO3;
    // FUNC0: GPIO0, 2 and 3 cleared (plain GPIO), everything else as it was.
    assert_eq!(bus.regs[0x16], fill & !touched_func, "FUNC0");
    // MODE: GPIO0 input (cleared), GPIO2 and GPIO3 outputs (set).
    assert_eq!(bus.regs[0x10], (fill & !GPIO0) | GPIO2 | GPIO3, "MODE");
    // DRV: GPIO2 and GPIO3 push-pull (cleared).
    assert_eq!(bus.regs[0x13], fill & !(GPIO2 | GPIO3), "DRV");
    // OUT: LCD power high, amplifier low.
    assert_eq!(bus.regs[0x11], (fill | GPIO2) & !GPIO3, "OUT");

    // Nothing outside those registers was written.
    for (reg, _) in &bus.writes {
        assert!(
            [0x09, 0x0A, 0x10, 0x11, 0x13, 0x16].contains(reg),
            "unexpected write to register {reg:#04x}"
        );
    }
    // PWR_CFG (the Grove boost) is not touched by begin.
    assert_eq!(bus.regs[0x06], fill);
}

#[test]
fn begin_writes_exact_bits_and_preserves_the_rest_from_all_ones() {
    assert_begin_result(0xFF);
}

#[test]
fn begin_writes_exact_bits_and_preserves_the_rest_from_all_zeros() {
    assert_begin_result(0x00);
}

#[test]
fn begin_preserves_a_mixed_pattern() {
    assert_begin_result(0xA5);
    assert_begin_result(0x5A);
}

#[test]
fn begin_reports_a_device_id_mismatch_without_failing() {
    let mut bus = FakeBus::filled(0);
    bus.regs[0x00] = 0x34;
    bus.regs[0x01] = 0x12;
    let report = m5pm1::begin(&mut bus).expect("mismatch is not an error");
    assert_eq!(report.device_id, 0x1234);
    assert!(!report.device_id_matches());
}

#[test]
fn read_status_decodes_little_endian() {
    let mut bus = FakeBus::filled(0);
    bus.regs[0x22] = 0x34;
    bus.regs[0x23] = 0x12;
    bus.regs[0x24] = 0x88;
    bus.regs[0x25] = 0x13;
    bus.regs[0x04] = 0b101;
    bus.regs[0x12] = 0xC3;
    let s = m5pm1::read_status(&mut bus).expect("status");
    assert_eq!(
        s,
        Pm1Status {
            vbat_mv: 0x1234,
            vin_mv: 0x1388,
            pwr_src: 0b101,
            gpio_in: 0xC3,
        }
    );
    assert_eq!(s.vin_mv, 5000);
}

fn status(vbat_mv: u16, pwr_src: u8, gpio_in: u8) -> Pm1Status {
    Pm1Status {
        vbat_mv,
        vin_mv: 0,
        pwr_src,
        gpio_in,
    }
}

#[test]
fn battery_percent_formula() {
    assert_eq!(battery_percent(0), None);
    assert_eq!(battery_percent(3300), Some(0));
    assert_eq!(battery_percent(3000), Some(0), "below empty clamps to 0");
    assert_eq!(battery_percent(3700), Some(50));
    // (4150 - 3300) * 100 / 800 = 106, clamped.
    assert_eq!(battery_percent(4150), Some(100));
    assert_eq!(battery_percent(4300), Some(100));
}

#[test]
fn power_status_battery_and_vbus() {
    assert_eq!(power_status_of(status(0, 0, 0xFF)).battery_pct, None);
    assert_eq!(power_status_of(status(3700, 0, 0xFF)).battery_pct, Some(50));
    assert!(power_status_of(status(3700, 0b001, 0xFF)).vbus_present);
    assert!(!power_status_of(status(3700, 0b110, 0xFF)).vbus_present);
}

fn power_status_of(s: Pm1Status) -> PowerStatus {
    m5pm1::power_status(&s)
}

#[test]
fn charging_needs_vbus_and_the_charge_pin_level() {
    let (charging_level, idle_level) = if CHARGE_PIN_ACTIVE_LOW {
        (0x00, 0x01)
    } else {
        (0x01, 0x00)
    };
    assert!(power_status_of(status(3700, 0b001, charging_level)).charging);
    assert!(!power_status_of(status(3700, 0b001, idle_level)).charging);
    // Pin says charging but no USB: not charging.
    assert!(!power_status_of(status(3700, 0b000, charging_level)).charging);
    // Only GPIO0 matters.
    let noisy = if CHARGE_PIN_ACTIVE_LOW { 0xFE } else { 0x01 };
    assert!(power_status_of(status(3700, 0b001, noisy)).charging);
}

#[test]
fn charge_pin_polarity_helper_covers_both_polarities() {
    assert!(charge_pin_says_charging(0x00, true));
    assert!(!charge_pin_says_charging(0x01, true));
    assert!(charge_pin_says_charging(0x01, false));
    assert!(!charge_pin_says_charging(0x00, false));
}

#[test]
fn set_ext_output_flips_only_bit3() {
    for fill in [0x00u8, 0xFF, 0xA5] {
        let mut bus = FakeBus::filled(fill);
        m5pm1::set_ext_output(&mut bus, true).expect("on");
        assert_eq!(bus.regs[0x06], fill | 0x08);
        m5pm1::set_ext_output(&mut bus, false).expect("off");
        assert_eq!(bus.regs[0x06], fill & !0x08);
        assert!(bus.writes.iter().all(|(reg, _)| *reg == 0x06));
    }
}

#[test]
fn shutdown_writes_the_shutdown_command() {
    let mut bus = FakeBus::filled(0);
    m5pm1::shutdown(&mut bus).expect("shutdown");
    assert_eq!(bus.writes, vec![(0x0C, 0xA1)]);
}

#[test]
fn i2c_errors_propagate_and_stop_the_sequence() {
    let mut bus = FakeBus::filled(0);
    bus.fail_after = Some(0);
    assert_eq!(m5pm1::begin(&mut bus), Err(I2cError));
    assert_eq!(m5pm1::read_status(&mut bus), Err(I2cError));
    assert_eq!(m5pm1::set_ext_output(&mut bus, true), Err(I2cError));
    assert_eq!(m5pm1::shutdown(&mut bus), Err(I2cError));
    assert!(bus.writes.is_empty());

    // A failure in the middle of begin surfaces and later writes do not happen.
    let mut bus = FakeBus::filled(0);
    bus.fail_after = Some(3);
    assert_eq!(m5pm1::begin(&mut bus), Err(I2cError));
    assert_eq!(bus.writes.len(), 2, "only I2C_CFG and WDT_CNT were written");
}
