//! `FeedbackHal` for the StickS3 speaker (#744, part 2): ES8311 codec on I2S0, amplifier on
//! M5PM1 GPIO3.
//!
//! How M5Unified drives it (MIT, `src/M5Unified.inl`, StickS3 speaker config and
//! `_speaker_enabled_cb_sticks3`): I2S0 with MCLK GPIO18, BCK GPIO17, WS GPIO15, DOUT GPIO14,
//! 16-bit stereo at 22050 Hz; then PM1 GPIO3 high and the codec register sequence in
//! `stockbot_scanner_core::es8311`. See `HARDWARE-sticks3.md`.
//!
//! Playing never blocks. `play` starts the core `ToneSequencer` and pushes as many samples as the
//! I2S DMA buffer takes without waiting; the main loop calls [`Sticks3Speaker::tick`] every pass
//! to push the rest. The DMA buffer holds the longest cue, so a cue keeps sounding even when a
//! slow network call stalls the loop right after it starts. The buffer clears itself to silence
//! once played. A new cue starts after whatever is already in the buffer.
//!
//! The codec and amplifier start on the first `play` or `tick`, not in `begin`: the controller
//! calls `feedback.begin()` before `power.begin()`, and the M5PM1 set-up switches the amplifier
//! pin off.
//!
//! UNVERIFIED on the bench: everything above is read from M5Unified.

use std::cell::RefCell;
use std::time::{Duration, Instant};

use esp_idf_svc::hal::gpio::{Gpio14, Gpio15, Gpio17, Gpio18};
use esp_idf_svc::hal::i2s::config::{
    Config as I2sChannelConfig, DataBitWidth, SlotMode, StdClkConfig, StdConfig, StdGpioConfig,
    StdSlotConfig,
};
use esp_idf_svc::hal::i2s::{I2sDriver, I2sTx, I2S0};
use esp_idf_svc::sys::{EspError, ESP_ERR_TIMEOUT};
use stockbot_scanner_core::i2c::I2cBus;
use stockbot_scanner_core::tone::{longest_cue_ms, ToneSequencer};
use stockbot_scanner_core::{es8311, m5pm1, AudioCue, FeedbackHal};

/// M5Unified's StickS3 speaker rate.
const SAMPLE_RATE_HZ: u32 = 22_050;
/// Stereo 16-bit frames are 4 bytes; a DMA buffer is at most 4092 bytes.
const FRAMES_PER_DMA_BUFFER: u32 = 960;
const BYTES_PER_FRAME: usize = 4;
const SAMPLES_PER_BLOCK: usize = 256;
const CODEC_RETRY_INTERVAL: Duration = Duration::from_secs(30);
const ERROR_LOG_INTERVAL: Duration = Duration::from_secs(30);

pub struct SpeakerPeripherals {
    pub i2s: I2S0,
    pub mclk: Gpio18,
    pub bclk: Gpio17,
    pub ws: Gpio15,
    pub dout: Gpio14,
}

pub struct Sticks3Speaker<B: I2cBus> {
    bus: B,
    i2s: I2sDriver<'static, I2sTx>,
    sequencer: ToneSequencer,
    block: Vec<i16>,
    pending: Vec<u8>,
    pending_at: usize,
    codec_ready: bool,
    last_codec_attempt: Option<Instant>,
    last_error_log: Option<Instant>,
}

impl<B: I2cBus> Sticks3Speaker<B> {
    pub fn new(bus: B, p: SpeakerPeripherals) -> Result<Self, EspError> {
        let frames_needed = longest_cue_ms() * SAMPLE_RATE_HZ / 1000;
        let channel = I2sChannelConfig::new()
            .dma_buffer_count(frames_needed.div_ceil(FRAMES_PER_DMA_BUFFER))
            .frames_per_buffer(FRAMES_PER_DMA_BUFFER)
            .auto_clear(true);
        let config = StdConfig::new(
            channel,
            StdClkConfig::from_sample_rate_hz(SAMPLE_RATE_HZ),
            StdSlotConfig::philips_slot_default(DataBitWidth::Bits16, SlotMode::Stereo),
            StdGpioConfig::default(),
        );
        let mut i2s = I2sDriver::new_std_tx(p.i2s, &config, p.bclk, p.dout, Some(p.mclk), p.ws)?;
        i2s.tx_enable()?;
        Ok(Self {
            bus,
            i2s,
            sequencer: ToneSequencer::new(SAMPLE_RATE_HZ),
            block: vec![0; SAMPLES_PER_BLOCK],
            pending: Vec::with_capacity(SAMPLES_PER_BLOCK * BYTES_PER_FRAME),
            pending_at: 0,
            codec_ready: false,
            last_codec_attempt: None,
            last_error_log: None,
        })
    }

    pub fn play(&mut self, cue: AudioCue) {
        log::info!("speaker: {cue:?}");
        self.ensure_codec();
        self.sequencer.start(cue);
        self.pending.clear();
        self.pending_at = 0;
        self.pump();
    }

    /// Called every pass of the main loop.
    pub fn tick(&mut self) {
        self.ensure_codec();
        self.pump();
    }

    fn ensure_codec(&mut self) {
        if self.codec_ready {
            return;
        }
        let now = Instant::now();
        if self
            .last_codec_attempt
            .is_some_and(|at| now.duration_since(at) < CODEC_RETRY_INTERVAL)
        {
            return;
        }
        self.last_codec_attempt = Some(now);
        let started = m5pm1::set_speaker_amp(&mut self.bus, true)
            .and_then(|()| es8311::enable_speaker(&mut self.bus));
        match started {
            Ok(()) => {
                self.codec_ready = true;
                log::info!("speaker: ES8311 and amplifier on");
            }
            Err(_) => log::error!("speaker: could not start the ES8311 codec; retrying in 30 s"),
        }
    }

    /// Hands queued samples to I2S until its buffer is full or the cue ends; never waits.
    fn pump(&mut self) {
        loop {
            if self.pending_at == self.pending.len() {
                let n = self.sequencer.fill(&mut self.block);
                if n == 0 {
                    return;
                }
                self.pending.clear();
                self.pending_at = 0;
                for sample in &self.block[..n] {
                    let bytes = sample.to_le_bytes();
                    self.pending.extend_from_slice(&bytes);
                    self.pending.extend_from_slice(&bytes);
                }
            }
            match self.i2s.write(&self.pending[self.pending_at..], 0) {
                Ok(written) => {
                    self.pending_at += written;
                    if self.pending_at < self.pending.len() {
                        return;
                    }
                }
                Err(err) if err.code() == ESP_ERR_TIMEOUT => return,
                Err(err) => {
                    self.warn_rate_limited(&format!("I2S write failed ({err}); cue dropped"));
                    self.sequencer.stop();
                    self.pending.clear();
                    self.pending_at = 0;
                    return;
                }
            }
        }
    }

    fn warn_rate_limited(&mut self, what: &str) {
        let now = Instant::now();
        if self
            .last_error_log
            .is_none_or(|at| now.duration_since(at) >= ERROR_LOG_INTERVAL)
        {
            self.last_error_log = Some(now);
            log::warn!("speaker: {what}");
        }
    }
}

/// The controller borrows its feedback for its whole life, and the main loop still needs to
/// [`Sticks3Speaker::tick`] the speaker, so both share it through a `RefCell`.
pub struct SpeakerFeedback<'a, B: I2cBus>(pub &'a RefCell<Sticks3Speaker<B>>);

impl<B: I2cBus> FeedbackHal for SpeakerFeedback<'_, B> {
    fn begin(&mut self) {}

    fn play(&mut self, cue: AudioCue) {
        self.0.borrow_mut().play(cue);
    }

    fn flash_led_for_scan(&mut self, _stock_in: bool) {}
}
