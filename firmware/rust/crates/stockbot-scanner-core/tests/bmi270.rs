//! BMI270 accelerometer driver against a fake sensor (#744, part 2).

use stockbot_scanner_core::bmi270::{
    self, Bmi270Error, I2cBus, I2cError, CONFIG_FILE, UPLOAD_CHUNK,
};

/// A BMI270 at `addr` that rebuilds the uploaded config from the `INIT_ADDR` words, and reports
/// a good load once `INIT_CTRL` goes to 1 if the upload was complete and correct.
struct FakeBmi {
    addr: u8,
    regs: [u8; 256],
    uploaded: Vec<u8>,
    upload_word: usize,
    writes: Vec<(u8, u8)>,
    init_ok_after_load: bool,
    fail_reads: bool,
}

impl FakeBmi {
    fn at(addr: u8) -> Self {
        let mut regs = [0u8; 256];
        regs[0x00] = 0x24;
        Self {
            addr,
            regs,
            uploaded: vec![0; CONFIG_FILE.len()],
            upload_word: 0,
            writes: Vec::new(),
            init_ok_after_load: true,
            fail_reads: false,
        }
    }
}

impl I2cBus for FakeBmi {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        if addr != self.addr {
            return Err(I2cError);
        }
        match bytes[0] {
            0x5B => {
                assert_eq!(bytes.len(), 3, "INIT_ADDR_0 and _1 in one write");
                self.upload_word = usize::from(bytes[1] & 0x0F) | usize::from(bytes[2]) << 4;
            }
            0x5E => {
                let start = self.upload_word * 2;
                self.uploaded[start..start + bytes.len() - 1].copy_from_slice(&bytes[1..]);
            }
            reg => {
                assert_eq!(bytes.len(), 2, "single register writes only");
                self.regs[reg as usize] = bytes[1];
                self.writes.push((reg, bytes[1]));
                if reg == 0x59 && bytes[1] == 1 && self.init_ok_after_load {
                    let good = self.uploaded.as_slice() == CONFIG_FILE.as_slice();
                    self.regs[0x21] = if good { 0x01 } else { 0x02 };
                }
            }
        }
        Ok(())
    }

    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError> {
        if addr != self.addr || self.fail_reads {
            return Err(I2cError);
        }
        let start = bytes[0] as usize;
        buf.copy_from_slice(&self.regs[start..start + buf.len()]);
        Ok(())
    }
}

fn begin(bus: &mut FakeBmi) -> (Result<u8, Bmi270Error>, Vec<u32>) {
    let mut delays = Vec::new();
    let result = bmi270::begin(bus, &mut |ms| delays.push(ms));
    (result, delays)
}

#[test]
fn begin_uploads_the_config_and_turns_the_accelerometer_on() {
    let mut bus = FakeBmi::at(0x68);
    let (result, delays) = begin(&mut bus);
    assert_eq!(result, Ok(0x68));
    assert_eq!(bus.uploaded.as_slice(), CONFIG_FILE.as_slice());
    assert_eq!(
        bus.writes,
        vec![
            (0x7E, 0xB6),
            (0x7C, 0x00),
            (0x59, 0x00),
            (0x59, 0x01),
            (0x41, 0x02),
            (0x7D, 0x04),
        ]
    );
    assert_eq!(&delays[..2], &[2, 1], "reset and power-save waits");
}

#[test]
fn begin_finds_the_sensor_at_the_other_address() {
    let mut bus = FakeBmi::at(0x69);
    assert_eq!(begin(&mut bus).0, Ok(0x69));
}

#[test]
fn config_file_is_boschs_8_kib_blob() {
    assert_eq!(UPLOAD_CHUNK % 2, 0);
    assert_eq!(CONFIG_FILE.len(), 8192);
    // Bosch's file starts with these bytes (bmi270.c, v2.86.1).
    assert_eq!(
        &CONFIG_FILE[..8],
        &[0xc8, 0x2e, 0x00, 0x2e, 0x80, 0x2e, 0x3d, 0xb1]
    );
}

#[test]
fn begin_fails_when_the_sensor_never_reports_init_ok() {
    let mut bus = FakeBmi::at(0x68);
    bus.init_ok_after_load = false;
    let (result, delays) = begin(&mut bus);
    assert_eq!(result, Err(Bmi270Error::InitFailed(0)));
    assert_eq!(delays.len(), 2 + 20, "polled for 20 ms");
    assert!(
        !bus.writes.iter().any(|(reg, _)| *reg == 0x7D),
        "the accelerometer stays off"
    );
}

#[test]
fn begin_reports_a_missing_sensor() {
    let mut bus = FakeBmi::at(0x68);
    bus.regs[0x00] = 0x00;
    assert_eq!(begin(&mut bus).0, Err(Bmi270Error::NotFound));
    assert!(bus.writes.is_empty());
}

#[test]
fn read_accel_converts_8g_counts_to_milli_g() {
    let mut bus = FakeBmi::at(0x68);
    let raw: [i16; 3] = [4096, -2048, 4423];
    for (i, value) in raw.iter().enumerate() {
        let [lo, hi] = value.to_le_bytes();
        bus.regs[0x0C + 2 * i] = lo;
        bus.regs[0x0D + 2 * i] = hi;
    }
    assert_eq!(
        bmi270::read_accel_mg(&mut bus, 0x68),
        Ok([1000, -500, 1079])
    );
}

#[test]
fn read_accel_surfaces_bus_errors() {
    let mut bus = FakeBmi::at(0x68);
    bus.fail_reads = true;
    assert_eq!(bmi270::read_accel_mg(&mut bus, 0x68), Err(I2cError));
}
