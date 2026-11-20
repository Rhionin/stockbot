//! Minimal shared outage backoff for opaque (no-code) device 401s (PR #713 / #718 PR713 subsumption).

// #718 slice S-b will replace this with the shared retry_policy module.
pub const OUTAGE_BACKOFF_BASE_MS: u64 = 1000;
pub const OUTAGE_BACKOFF_FACTOR: u64 = 2;
pub const OUTAGE_BACKOFF_CAP_MS: u64 = 30_000;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OutageBackoffTrigger {
    Flush,
    Heartbeat,
    InlineScan,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct OutageBackoffState {
    pub streak: u32,
    pub flush_not_before_ms: u64,
    pub heartbeat_not_before_ms: u64,
}

pub fn outage_backoff_envelope_ms(streak: u32) -> u64 {
    let mut d = OUTAGE_BACKOFF_BASE_MS;
    for _ in 0..streak {
        d = d
            .saturating_mul(OUTAGE_BACKOFF_FACTOR)
            .min(OUTAGE_BACKOFF_CAP_MS);
        if d >= OUTAGE_BACKOFF_CAP_MS {
            return OUTAGE_BACKOFF_CAP_MS;
        }
    }
    d.min(OUTAGE_BACKOFF_CAP_MS)
}

pub fn outage_may_send_flush(state: &OutageBackoffState, now_ms: u64) -> bool {
    now_ms >= state.flush_not_before_ms
}

pub fn outage_may_send_heartbeat(state: &OutageBackoffState, now_ms: u64) -> bool {
    now_ms >= state.heartbeat_not_before_ms
}

pub fn outage_backoff_reset(state: &mut OutageBackoffState) {
    *state = OutageBackoffState::default();
}

pub fn outage_backoff_increment_streak(state: &mut OutageBackoffState) {
    state.streak = state.streak.saturating_add(1);
}

pub fn outage_backoff_on_no_code_401(
    state: &mut OutageBackoffState,
    now_ms: u64,
    trigger: OutageBackoffTrigger,
) {
    let wait = outage_backoff_envelope_ms(state.streak);
    match trigger {
        OutageBackoffTrigger::Flush | OutageBackoffTrigger::InlineScan => {
            state.flush_not_before_ms = now_ms.saturating_add(wait);
        }
        OutageBackoffTrigger::Heartbeat => {
            state.heartbeat_not_before_ms = now_ms.saturating_add(wait);
        }
    }
    outage_backoff_increment_streak(state);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn envelope_sequence_matches_pr713() {
        assert_eq!(outage_backoff_envelope_ms(0), 1000);
        assert_eq!(outage_backoff_envelope_ms(1), 2000);
        assert_eq!(outage_backoff_envelope_ms(2), 4000);
        assert_eq!(outage_backoff_envelope_ms(3), 8000);
        assert_eq!(outage_backoff_envelope_ms(4), 16_000);
        assert_eq!(outage_backoff_envelope_ms(5), 30_000);
        assert_eq!(outage_backoff_envelope_ms(6), 30_000);
    }

    #[test]
    fn envelope_saturates_at_cap_for_high_streaks() {
        assert_eq!(outage_backoff_envelope_ms(54), 30_000);
        assert_eq!(outage_backoff_envelope_ms(64), 30_000);
        assert_eq!(outage_backoff_envelope_ms(u32::MAX), 30_000);
    }

    #[test]
    fn streak_increment_saturates_at_u32_max() {
        let mut state = OutageBackoffState {
            streak: u32::MAX,
            ..Default::default()
        };
        outage_backoff_increment_streak(&mut state);
        assert_eq!(state.streak, u32::MAX);
    }
}
