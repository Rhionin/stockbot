//! ES8311 speaker codec start-up (#744, part 2).

use stockbot_scanner_core::es8311::{self, I2cBus, I2cError, ES8311_ADDR};

#[derive(Default)]
struct RecordingBus {
    writes: Vec<(u8, Vec<u8>)>,
    fail: bool,
}

impl I2cBus for RecordingBus {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        if self.fail {
            return Err(I2cError);
        }
        self.writes.push((addr, bytes.to_vec()));
        Ok(())
    }

    fn write_read(&mut self, _addr: u8, _bytes: &[u8], _buf: &mut [u8]) -> Result<(), I2cError> {
        panic!("the speaker start-up only writes");
    }
}

#[test]
fn enable_speaker_writes_m5unifieds_sequence_in_order() {
    let mut bus = RecordingBus::default();
    es8311::enable_speaker(&mut bus).expect("enable");
    let expected: Vec<(u8, Vec<u8>)> = [
        (0x00, 0x80),
        (0x01, 0xB5),
        (0x02, 0x18),
        (0x0D, 0x01),
        (0x12, 0x00),
        (0x13, 0x10),
        (0x32, 0xBF),
        (0x37, 0x08),
    ]
    .into_iter()
    .map(|(reg, value)| (0x18, vec![reg, value]))
    .collect();
    assert_eq!(ES8311_ADDR, 0x18);
    assert_eq!(bus.writes, expected);
}

#[test]
fn enable_speaker_surfaces_bus_errors() {
    let mut bus = RecordingBus {
        fail: true,
        ..RecordingBus::default()
    };
    assert_eq!(es8311::enable_speaker(&mut bus), Err(I2cError));
}
