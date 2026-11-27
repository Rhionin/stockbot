//! U173 driver against a fake unit that decodes the little-endian register prefix.

use stockbot_scanner_core::u173::{self, I2cBus, I2cError, U173_ADDR};

#[derive(Debug, PartialEq, Eq)]
enum Op {
    Write(Vec<u8>),
    Read(Vec<u8>, usize),
}

struct FakeUnit {
    regs: Vec<u8>,
    ops: Vec<Op>,
    fail: bool,
}

impl FakeUnit {
    fn new() -> Self {
        Self {
            regs: vec![0; 0x2000],
            ops: Vec::new(),
            fail: false,
        }
    }

    fn ready(data: &[u8], len: u16) -> Self {
        let mut unit = Self::new();
        unit.regs[0x0010] = 1;
        unit.regs[0x0020..0x0022].copy_from_slice(&len.to_le_bytes());
        unit.regs[0x1000..0x1000 + data.len()].copy_from_slice(data);
        unit
    }

    fn writes(&self) -> Vec<&Vec<u8>> {
        self.ops
            .iter()
            .filter_map(|op| match op {
                Op::Write(w) => Some(w),
                Op::Read(..) => None,
            })
            .collect()
    }

    fn reads(&self) -> Vec<&Vec<u8>> {
        self.ops
            .iter()
            .filter_map(|op| match op {
                Op::Read(r, _) => Some(r),
                Op::Write(_) => None,
            })
            .collect()
    }
}

fn reg_of(prefix: &[u8]) -> usize {
    usize::from(prefix[0]) | (usize::from(prefix[1]) << 8)
}

impl I2cBus for FakeUnit {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        assert_eq!(addr, U173_ADDR);
        if self.fail {
            return Err(I2cError);
        }
        self.ops.push(Op::Write(bytes.to_vec()));
        let reg = reg_of(bytes);
        for (i, b) in bytes[2..].iter().enumerate() {
            self.regs[reg + i] = *b;
        }
        Ok(())
    }

    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError> {
        assert_eq!(addr, U173_ADDR);
        assert_eq!(bytes.len(), 2, "a read sends only the 2 address bytes");
        if self.fail {
            return Err(I2cError);
        }
        self.ops.push(Op::Read(bytes.to_vec(), buf.len()));
        let reg = reg_of(bytes);
        buf.copy_from_slice(&self.regs[reg..reg + buf.len()]);
        // Reading the data resets the status, per the protocol sheet.
        if reg == 0x1000 {
            self.regs[0x0010] = 0;
        }
        Ok(())
    }
}

#[test]
fn begin_sets_manual_mode_then_stops_decoding_and_reads_version() {
    let mut unit = FakeUnit::new();
    unit.regs[0x00FE] = 0x07;
    assert_eq!(u173::begin(&mut unit), Ok(0x07));
    let writes = unit.writes();
    assert_eq!(writes.len(), 2);
    assert_eq!(writes[0], &vec![0x30, 0x00, 0x01], "trigger mode = manual");
    assert_eq!(writes[1], &vec![0x00, 0x00, 0x00], "trigger = stopped");
    assert_eq!(unit.reads(), vec![&vec![0xFE, 0x00]]);
}

#[test]
fn set_decoding_writes_the_trigger_register() {
    let mut unit = FakeUnit::new();
    u173::set_decoding(&mut unit, true).unwrap();
    u173::set_decoding(&mut unit, false).unwrap();
    assert_eq!(
        unit.writes(),
        vec![&vec![0x00, 0x00, 0x01], &vec![0x00, 0x00, 0x00]]
    );
}

#[test]
fn poll_returns_the_barcode_and_reads_data_from_wire_address_0x1000() {
    let mut unit = FakeUnit::ready(b"0123456789012", 13);
    assert_eq!(u173::poll(&mut unit), Ok(Some("0123456789012".to_owned())));
    assert_eq!(
        unit.reads(),
        vec![&vec![0x10, 0x00], &vec![0x20, 0x00], &vec![0x00, 0x10]]
    );
    assert!(
        unit.writes().is_empty(),
        "reading the data resets the status"
    );
    assert_eq!(u173::poll(&mut unit), Ok(None), "status was reset");
}

#[test]
fn poll_reads_the_length_little_endian() {
    let data = vec![b'7'; 300];
    let mut unit = FakeUnit::ready(&data, 300);
    assert_eq!(unit.regs[0x0020..0x0022], [0x2C, 0x01]);
    let line = u173::poll(&mut unit).unwrap().unwrap();
    assert_eq!(line.len(), 300);
    assert!(unit.ops.contains(&Op::Read(vec![0x00, 0x10], 300)));
}

#[test]
fn poll_accepts_the_maximum_length() {
    let data = vec![b'1'; 512];
    let mut unit = FakeUnit::ready(&data, 512);
    assert_eq!(u173::poll(&mut unit).unwrap().unwrap().len(), 512);
}

#[test]
fn poll_rejects_bad_lengths_and_clears_the_status() {
    for len in [0u16, 513] {
        let mut unit = FakeUnit::ready(b"12345", len);
        assert_eq!(u173::poll(&mut unit), Ok(None), "len {len}");
        assert_eq!(unit.writes(), vec![&vec![0x10, 0x00, 0x00]], "len {len}");
        assert_eq!(unit.regs[0x0010], 0, "len {len}");
        assert!(
            !unit.reads().contains(&&vec![0x00, 0x10]),
            "no data read for len {len}"
        );
    }
}

#[test]
fn poll_ignores_status_not_ready_read_again_and_unknown() {
    for status in [0u8, 2, 3, 0xFF] {
        let mut unit = FakeUnit::ready(b"0123456789012", 13);
        unit.regs[0x0010] = status;
        assert_eq!(u173::poll(&mut unit), Ok(None), "status {status}");
        assert_eq!(unit.reads(), vec![&vec![0x10, 0x00]], "status {status}");
        assert!(unit.writes().is_empty(), "status {status}");
    }
}

#[test]
fn poll_trims_trailing_cr_lf_and_drops_empty_lines() {
    let mut unit = FakeUnit::ready(b"4006381333931\r\n", 15);
    assert_eq!(u173::poll(&mut unit), Ok(Some("4006381333931".to_owned())));
    let mut unit = FakeUnit::ready(b"\r\n", 2);
    assert_eq!(u173::poll(&mut unit), Ok(None));
}

#[test]
fn bus_errors_propagate() {
    let mut unit = FakeUnit::new();
    unit.fail = true;
    assert_eq!(u173::begin(&mut unit), Err(I2cError));
    assert_eq!(u173::set_decoding(&mut unit, true), Err(I2cError));
    assert_eq!(u173::poll(&mut unit), Err(I2cError));
}
