//! Pure retry/backoff policy for flush and heartbeat (no I/O).
//!
//! Constants and state machine from issue #718 architect design §3–§5.

/// Operation gated by shared outage streak plus per-op waits.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum Op {
    Flush = 0,
    Heartbeat = 1,
}

/// Classified HTTP / transport result for policy updates.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Outcome {
    Ok,
    Reachable4xx,
    Unreachable,
    Server5xx,
    RateLimited,
    Unauth401NoCode,
}

/// Per-operation backoff shape (base, growth, cap, jitter, Retry-After clamp).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Params {
    pub base_ms: u64,
    pub factor: u64,
    pub cap_ms: u64,
    pub jitter_lo_permille: u64,
    pub ra_max_ms: u64,
}

/// Scan flush and inline send (api-contract 30 s cap).
pub const FLUSH: Params = Params {
    base_ms: 1_000,
    factor: 2,
    cap_ms: 30_000,
    jitter_lo_permille: 500,
    ra_max_ms: 3_600_000,
};

/// Heartbeat (60 s base, 300 s cap per design).
pub const HEARTBEAT: Params = Params {
    base_ms: 60_000,
    factor: 2,
    cap_ms: 300_000,
    jitter_lo_permille: 500,
    ra_max_ms: 3_600_000,
};

/// RAM-only retry state; cleared on reboot / re-pair.
#[derive(Clone, Default, PartialEq, Eq, Debug)]
pub struct RetryState {
    pub outage_streak: u32,
    pub op_rl_streak: [u32; 2],
    /// Send gate per op; `0` means no outage wait.
    pub not_before_ms: [u64; 2],
    pub rl_until_ms: [u64; 2],
}

/// Deterministic PRNG for jitter draws in production wiring (host tests inject a closure).
#[derive(Clone, Debug)]
pub struct SplitMix64 {
    state: u64,
}

impl SplitMix64 {
    pub fn new(seed: u64) -> Self {
        Self { state: seed }
    }

    pub fn next_u32(&mut self) -> u32 {
        self.state = self.state.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let mut z = self.state;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        (z ^ (z >> 31)) as u32
    }

    /// Jitter draw in `0..=1000` for `jittered_ms` (maps full `next_u32` without modulo bias).
    pub fn next_permille(&mut self) -> u32 {
        ((self.next_u32() as u64 * 1001) >> 32) as u32
    }
}

/// True when monotonic `now_ms` is at or past both per-op gates.
pub fn may_send(s: &RetryState, op: Op, now_ms: u64) -> bool {
    let i = op as usize;
    let gate = s.not_before_ms[i].max(s.rl_until_ms[i]);
    now_ms >= gate
}

/// Envelope delay `d(k) = min(cap, base * factor^k)` with saturating multiply.
pub fn envelope_ms(p: &Params, k: u32) -> u64 {
    let mut d = p.base_ms;
    for _ in 0..k.min(64) {
        d = d.saturating_mul(p.factor);
        if d >= p.cap_ms {
            return p.cap_ms;
        }
    }
    d.min(p.cap_ms)
}

/// Equal jitter: wait in `[d * lo/1000, d]` with `r` in `0..=1000`.
///
/// `d` must be `<= p.cap_ms` (the envelope value from `envelope_ms`).
pub fn jittered_ms(p: &Params, d: u64, r: u32) -> u64 {
    let r = r.min(1000) as u64;
    let lo = d.saturating_mul(p.jitter_lo_permille) / 1000;
    let span = d.saturating_sub(lo);
    lo.saturating_add(span.saturating_mul(r) / 1000)
}

/// RFC 9110 delay-seconds only: trim SP/HTAB, `1*DIGIT`, saturate on overflow → milliseconds.
/// HTTP-date, empty, signs, decimals, hex => `None`.
pub fn parse_retry_after_ms(raw: Option<&str>) -> Option<u64> {
    let s = raw?;
    let trimmed = trim_ascii_sp_htab(s);
    if trimmed.is_empty() {
        return None;
    }
    if !trimmed.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut secs: u64 = 0;
    for b in trimmed.bytes() {
        let digit = (b - b'0') as u64;
        secs = secs.saturating_mul(10).saturating_add(digit);
    }
    Some(secs.saturating_mul(1000))
}

fn trim_ascii_sp_htab(s: &str) -> &str {
    let bytes = s.as_bytes();
    let mut start = 0usize;
    let mut end = bytes.len();
    while start < end && (bytes[start] == b' ' || bytes[start] == b'\t') {
        start += 1;
    }
    while end > start && (bytes[end - 1] == b' ' || bytes[end - 1] == b'\t') {
        end -= 1;
    }
    &s[start..end]
}

/// Update state from one classified outcome; draws `rng` once per scheduled wait.
pub fn on_outcome(
    mut s: RetryState,
    op: Op,
    out: Outcome,
    ra_ms: Option<u64>,
    now_ms: u64,
    rng: &mut dyn FnMut() -> u32,
) -> RetryState {
    let (p, i) = (
        if matches!(op, Op::Flush) {
            &FLUSH
        } else {
            &HEARTBEAT
        },
        op as usize,
    );
    match out {
        Outcome::Ok | Outcome::Reachable4xx => {
            s.outage_streak = 0;
            s.op_rl_streak[i] = 0;
            s.not_before_ms = [0; 2];
        }
        Outcome::Unreachable | Outcome::Server5xx | Outcome::Unauth401NoCode => {
            let k = s.outage_streak;
            s.outage_streak = k.saturating_add(1);
            let mut w = jittered_ms(p, envelope_ms(p, k), rng());
            if matches!(out, Outcome::Server5xx) {
                if let Some(ra) = ra_ms {
                    w = w.max(ra.min(p.ra_max_ms));
                }
            }
            s.not_before_ms[i] = now_ms.saturating_add(w);
        }
        Outcome::RateLimited => {
            let k = s.op_rl_streak[i];
            s.op_rl_streak[i] = k.saturating_add(1);
            let mut w = jittered_ms(p, envelope_ms(p, k), rng());
            if let Some(ra) = ra_ms {
                w = w.max(ra.min(p.ra_max_ms));
            }
            s.rl_until_ms[i] = now_ms.saturating_add(w);
        }
    }
    s
}

#[cfg(test)]
mod unit_tests {
    use super::*;

    #[test]
    fn envelope_first_failure_is_base() {
        assert_eq!(envelope_ms(&FLUSH, 0), 1_000);
        assert_eq!(envelope_ms(&HEARTBEAT, 0), 60_000);
    }

    #[test]
    fn jitter_r_clamps_above_1000() {
        let d = 10_000;
        assert_eq!(jittered_ms(&FLUSH, d, 1001), jittered_ms(&FLUSH, d, 1000));
    }

    #[test]
    fn envelope_at_u32_max_k_returns_cap_without_huge_loop() {
        assert_eq!(envelope_ms(&FLUSH, u32::MAX), FLUSH.cap_ms);
    }
}
