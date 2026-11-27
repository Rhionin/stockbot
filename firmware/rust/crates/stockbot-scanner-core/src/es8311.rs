//! ES8311 audio codec behind the StickS3 speaker (#744, part 2), pure over the [`I2cBus`] trait.
//!
//! Source: M5Unified `src/M5Unified.inl`, `_speaker_enabled_cb_sticks3` and the StickS3 speaker
//! config (MIT), read 2026-11-25:
//! <https://github.com/m5stack/M5Unified/blob/master/src/M5Unified.inl>
//!
//! M5Unified drives the speaker as I2S0 (MCLK GPIO18, BCK GPIO17, WS GPIO15, DOUT GPIO14),
//! 16-bit stereo at 22050 Hz, then switches the amplifier on (M5PM1 GPIO3 high, see
//! [`crate::m5pm1::set_speaker_amp`]) and writes [`SPEAKER_ENABLE_SEQUENCE`] to the codec.
//! Register `0x01 = 0xB5` takes the codec's master clock from BCK, and `0x02 = 0x18` multiplies it
//! by 8, so 16-bit stereo (BCK = 32 × rate) gives the codec 256 × rate at any sample rate.
//!
//! UNVERIFIED on the bench: everything here is read from M5Unified.

pub use crate::i2c::{I2cBus, I2cError};

/// 7-bit I2C address (M5Unified `es8311_i2c_addr0`).
pub const ES8311_ADDR: u8 = 0x18;

/// `(register, value)` pairs, in M5Unified's order.
pub const SPEAKER_ENABLE_SEQUENCE: [(u8, u8); 8] = [
    (0x00, 0x80), // reset: CSM power on
    (0x01, 0xB5), // clock manager: MCLK from BCLK
    (0x02, 0x18), // clock manager: pre-multiply ×8
    (0x0D, 0x01), // power up the analog circuits
    (0x12, 0x00), // power up the DAC
    (0x13, 0x10), // enable the output to the headphone driver
    (0x32, 0xBF), // DAC volume 0 dB
    (0x37, 0x08), // bypass the DAC equalizer
];

pub fn enable_speaker(bus: &mut dyn I2cBus) -> Result<(), I2cError> {
    for (reg, value) in SPEAKER_ENABLE_SEQUENCE {
        bus.write(ES8311_ADDR, &[reg, value])?;
    }
    Ok(())
}
