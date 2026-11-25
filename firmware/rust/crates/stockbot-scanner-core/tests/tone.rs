//! Audio cue tone table and the sample-clocked tone sequencer (#744, part 2).

use stockbot_scanner_core::tone::{cue_tones, longest_cue_ms, Tone, ToneSequencer};
use stockbot_scanner_core::AudioCue;

const ALL_CUES: [AudioCue; 13] = [
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
];

fn t(freq_hz: u16, ms: u16) -> Tone {
    Tone { freq_hz, ms }
}

fn gap(ms: u16) -> Tone {
    Tone { freq_hz: 0, ms }
}

/// Issue #744's table, written out independently of the source.
fn expected(cue: AudioCue) -> Vec<Tone> {
    match cue {
        AudioCue::AscendingPairSuccess => vec![t(880, 80), t(1320, 80)],
        AudioCue::RisingTwoTone => vec![t(660, 70), t(990, 70)],
        AudioCue::FallingTwoTone => vec![t(990, 70), t(660, 70)],
        AudioCue::HighBeep => vec![t(1800, 70)],
        AudioCue::MediumBeep => vec![t(1300, 70)],
        AudioCue::DoubleBeep => vec![t(1300, 60), gap(40), t(950, 70)],
        AudioCue::SoftDoublePulse => vec![t(1100, 60), gap(50), t(1100, 60)],
        AudioCue::QuestionTone => vec![t(1000, 70), t(1400, 70)],
        AudioCue::ScanBeepLowTick => vec![t(1300, 50), gap(30), t(600, 40)],
        AudioCue::TripleErrorBuzz => vec![
            t(450, 80),
            gap(60),
            t(450, 80),
            gap(60),
            t(450, 80),
            gap(60),
        ],
        AudioCue::ModeRevertChime => vec![t(880, 60), t(660, 60)],
        AudioCue::DockTick => vec![t(900, 40)],
        AudioCue::OffDockChirp => vec![t(880, 50), t(880, 50)],
        AudioCue::Silent => vec![],
    }
}

#[test]
fn every_audible_cue_maps_to_the_issue_table() {
    for cue in ALL_CUES {
        let tones = cue_tones(cue);
        assert!(!tones.is_empty(), "{cue:?} has no tones");
        assert!(
            tones.iter().any(|tone| tone.freq_hz > 0),
            "{cue:?} is all silence"
        );
        assert_eq!(tones, expected(cue).as_slice(), "{cue:?}");
    }
}

#[test]
fn silent_has_no_tones() {
    assert!(cue_tones(AudioCue::Silent).is_empty());
    let mut seq = ToneSequencer::new(8000);
    seq.start(AudioCue::Silent);
    assert!(!seq.is_playing());
    assert_eq!(seq.fill(&mut [0i16; 16]), 0);
}

#[test]
fn longest_cue_is_the_triple_error_buzz() {
    assert_eq!(longest_cue_ms(), 420);
}

fn render(seq: &mut ToneSequencer, chunk: usize) -> Vec<i16> {
    let mut all = Vec::new();
    let mut buf = vec![0i16; chunk];
    loop {
        let n = seq.fill(&mut buf);
        all.extend_from_slice(&buf[..n]);
        if n < chunk {
            return all;
        }
    }
}

#[test]
fn sequencer_plays_each_step_for_its_length_in_samples() {
    const RATE: u32 = 8000;
    for cue in ALL_CUES {
        let mut seq = ToneSequencer::new(RATE);
        seq.start(cue);
        let samples = render(&mut seq, 64);
        let mut at = 0usize;
        for step in expected(cue) {
            let len = step.ms as usize * RATE as usize / 1000;
            let part = &samples[at..at + len];
            if step.freq_hz == 0 {
                assert!(part.iter().all(|&s| s == 0), "{cue:?} gap is not silent");
            } else {
                assert!(part.iter().any(|&s| s != 0), "{cue:?} tone is silent");
            }
            at += len;
        }
        assert_eq!(samples.len(), at, "{cue:?} length in samples");
        assert!(!seq.is_playing());
    }
}

#[test]
fn sequencer_steps_change_at_the_right_sample() {
    let mut seq = ToneSequencer::new(22_050);
    seq.start(AudioCue::ScanBeepLowTick);
    assert_eq!(seq.current_freq_hz(), Some(1300));
    let first = 50 * 22_050 / 1000;
    let mut buf = vec![0i16; first - 1];
    assert_eq!(seq.fill(&mut buf), first - 1);
    assert_eq!(seq.current_freq_hz(), Some(1300));
    assert_eq!(seq.fill(&mut [0i16; 1]), 1);
    assert_eq!(seq.current_freq_hz(), Some(0), "30 ms gap");
    let mut gap_buf = vec![0i16; 30 * 22_050 / 1000];
    seq.fill(&mut gap_buf);
    assert_eq!(seq.current_freq_hz(), Some(600));
    let mut last = vec![0i16; 1000];
    assert_eq!(seq.fill(&mut last), 40 * 22_050 / 1000);
    assert_eq!(seq.current_freq_hz(), None);
}

#[test]
fn sequencer_output_does_not_depend_on_the_block_size() {
    let mut a = ToneSequencer::new(16_000);
    let mut b = ToneSequencer::new(16_000);
    a.start(AudioCue::DoubleBeep);
    b.start(AudioCue::DoubleBeep);
    assert_eq!(render(&mut a, 7), render(&mut b, 4096));
}

#[test]
fn tone_frequency_matches_the_table() {
    const RATE: u32 = 22_050;
    let mut seq = ToneSequencer::new(RATE);
    seq.start(AudioCue::HighBeep);
    let samples = render(&mut seq, 256);
    let rising = samples.windows(2).filter(|w| w[0] < 0 && w[1] >= 0).count();
    // 1800 Hz for 70 ms is 126 cycles.
    assert!((125..=127).contains(&rising), "{rising} cycles");
}

#[test]
fn starting_a_cue_replaces_the_one_playing() {
    let mut seq = ToneSequencer::new(8000);
    seq.start(AudioCue::TripleErrorBuzz);
    seq.fill(&mut [0i16; 100]);
    seq.start(AudioCue::DockTick);
    assert_eq!(seq.current_freq_hz(), Some(900));
    assert_eq!(render(&mut seq, 64).len(), 40 * 8);
}

#[test]
fn stop_silences_the_sequencer() {
    let mut seq = ToneSequencer::new(8000);
    seq.start(AudioCue::TripleErrorBuzz);
    seq.stop();
    assert!(!seq.is_playing());
    assert_eq!(seq.fill(&mut [0i16; 8]), 0);
}
