//! Audio cue tone table and a non-blocking tone sequencer (#744, part 2).
//!
//! The table is the old C++ `hal_feedback.cpp` (`M5.Speaker.tone` calls with `delay` between
//! them), as listed in issue #744. Instead of sleeping, the firmware asks [`ToneSequencer::fill`]
//! for the next block of samples every pass of its main loop and hands them to the I2S driver
//! without waiting. Timing is counted in samples, so a slow loop pass delays a cue but never
//! changes the length of a tone or a gap.
//!
//! The waveform is the 16-step sine of M5Unified's `Speaker_Class::_default_tone_wav`
//! (<https://github.com/m5stack/M5Unified/blob/master/src/utility/Speaker_Class.inl>, MIT).

use crate::display_model::AudioCue;

/// One step of a cue: a tone, or silence when `freq_hz` is 0.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Tone {
    pub freq_hz: u16,
    pub ms: u16,
}

const fn tone(freq_hz: u16, ms: u16) -> Tone {
    Tone { freq_hz, ms }
}

const fn gap(ms: u16) -> Tone {
    Tone { freq_hz: 0, ms }
}

const ASCENDING_PAIR_SUCCESS: [Tone; 2] = [tone(880, 80), tone(1320, 80)];
const RISING_TWO_TONE: [Tone; 2] = [tone(660, 70), tone(990, 70)];
const FALLING_TWO_TONE: [Tone; 2] = [tone(990, 70), tone(660, 70)];
const HIGH_BEEP: [Tone; 1] = [tone(1800, 70)];
const MEDIUM_BEEP: [Tone; 1] = [tone(1300, 70)];
const DOUBLE_BEEP: [Tone; 3] = [tone(1300, 60), gap(40), tone(950, 70)];
const SOFT_DOUBLE_PULSE: [Tone; 3] = [tone(1100, 60), gap(50), tone(1100, 60)];
const QUESTION_TONE: [Tone; 2] = [tone(1000, 70), tone(1400, 70)];
const SCAN_BEEP_LOW_TICK: [Tone; 3] = [tone(1300, 50), gap(30), tone(600, 40)];
const TRIPLE_ERROR_BUZZ: [Tone; 6] = [
    tone(450, 80),
    gap(60),
    tone(450, 80),
    gap(60),
    tone(450, 80),
    gap(60),
];
const MODE_REVERT_CHIME: [Tone; 2] = [tone(880, 60), tone(660, 60)];
const DOCK_TICK: [Tone; 1] = [tone(900, 40)];
const OFF_DOCK_CHIRP: [Tone; 2] = [tone(880, 50), tone(880, 50)];

/// The tones for `cue`, in order. Empty only for [`AudioCue::Silent`].
pub fn cue_tones(cue: AudioCue) -> &'static [Tone] {
    match cue {
        AudioCue::Silent => &[],
        AudioCue::AscendingPairSuccess => &ASCENDING_PAIR_SUCCESS,
        AudioCue::RisingTwoTone => &RISING_TWO_TONE,
        AudioCue::FallingTwoTone => &FALLING_TWO_TONE,
        AudioCue::HighBeep => &HIGH_BEEP,
        AudioCue::MediumBeep => &MEDIUM_BEEP,
        AudioCue::DoubleBeep => &DOUBLE_BEEP,
        AudioCue::SoftDoublePulse => &SOFT_DOUBLE_PULSE,
        AudioCue::QuestionTone => &QUESTION_TONE,
        AudioCue::ScanBeepLowTick => &SCAN_BEEP_LOW_TICK,
        AudioCue::TripleErrorBuzz => &TRIPLE_ERROR_BUZZ,
        AudioCue::ModeRevertChime => &MODE_REVERT_CHIME,
        AudioCue::DockTick => &DOCK_TICK,
        AudioCue::OffDockChirp => &OFF_DOCK_CHIRP,
    }
}

/// Length of the longest cue, so the firmware can size its audio buffer to hold a whole cue.
pub fn longest_cue_ms() -> u32 {
    [
        AudioCue::AscendingPairSuccess,
        AudioCue::RisingTwoTone,
        AudioCue::FallingTwoTone,
        AudioCue::HighBeep,
        AudioCue::MediumBeep,
        AudioCue::DoubleBeep,
        AudioCue::SoftDoublePulse,
        AudioCue::QuestionTone,
        AudioCue::ScanBeepLowTick,
        AudioCue::TripleErrorBuzz,
        AudioCue::ModeRevertChime,
        AudioCue::DockTick,
        AudioCue::OffDockChirp,
    ]
    .into_iter()
    .map(|cue| cue_tones(cue).iter().map(|t| u32::from(t.ms)).sum())
    .max()
    .unwrap_or(0)
}

/// M5Unified's tone wave (unsigned 8-bit, 128 is the midpoint).
const TONE_WAVE: [u8; 16] = [
    177, 219, 246, 255, 246, 219, 177, 128, 79, 37, 10, 1, 10, 37, 79, 128,
];

/// Peak sample value. A quarter of full scale, like M5Unified's default master volume (64/255).
pub const TONE_AMPLITUDE: i32 = 8192;

/// Plays one cue at a time as 16-bit mono samples. Starting a cue cuts off the one playing, as
/// `M5.Speaker.tone` does by default.
#[derive(Clone, Debug)]
pub struct ToneSequencer {
    sample_rate_hz: u32,
    steps: &'static [Tone],
    step: usize,
    samples_left: u32,
    phase: u32,
}

impl ToneSequencer {
    pub fn new(sample_rate_hz: u32) -> Self {
        Self {
            sample_rate_hz,
            steps: &[],
            step: 0,
            samples_left: 0,
            phase: 0,
        }
    }

    pub fn sample_rate_hz(&self) -> u32 {
        self.sample_rate_hz
    }

    pub fn start(&mut self, cue: AudioCue) {
        self.steps = cue_tones(cue);
        self.step = 0;
        self.phase = 0;
        self.samples_left = self.step_samples(0);
        self.skip_finished_steps();
    }

    pub fn stop(&mut self) {
        self.steps = &[];
        self.step = 0;
        self.samples_left = 0;
    }

    pub fn is_playing(&self) -> bool {
        self.step < self.steps.len()
    }

    /// The step now playing: `Some(0)` during a gap, `None` once the cue has ended.
    pub fn current_freq_hz(&self) -> Option<u16> {
        self.steps.get(self.step).map(|t| t.freq_hz)
    }

    /// Writes the next samples of the cue into `out` and returns how many it wrote. Fewer than
    /// `out.len()` means the cue ended; 0 means nothing is playing.
    pub fn fill(&mut self, out: &mut [i16]) -> usize {
        let mut written = 0;
        while written < out.len() && self.is_playing() {
            let freq = self.steps[self.step].freq_hz;
            let run = (out.len() - written).min(self.samples_left as usize);
            for slot in &mut out[written..written + run] {
                *slot = self.next_sample(freq);
            }
            written += run;
            self.samples_left -= run as u32;
            if self.samples_left == 0 {
                self.step += 1;
                self.phase = 0;
                self.samples_left = self.step_samples(self.step);
                self.skip_finished_steps();
            }
        }
        written
    }

    fn step_samples(&self, step: usize) -> u32 {
        self.steps.get(step).map_or(0, |t| {
            (u64::from(t.ms) * u64::from(self.sample_rate_hz) / 1000) as u32
        })
    }

    /// Steps too short to last one sample at this rate are skipped.
    fn skip_finished_steps(&mut self) {
        while self.is_playing() && self.samples_left == 0 {
            self.step += 1;
            self.samples_left = self.step_samples(self.step);
        }
    }

    fn next_sample(&mut self, freq_hz: u16) -> i16 {
        if freq_hz == 0 || self.sample_rate_hz == 0 {
            return 0;
        }
        let index = (self.phase >> 28) as usize;
        let increment = (u64::from(freq_hz) << 32) / u64::from(self.sample_rate_hz);
        self.phase = self.phase.wrapping_add(increment as u32);
        let centred = i32::from(TONE_WAVE[index]) - 128;
        (centred * TONE_AMPLITUDE / 127) as i16
    }
}
