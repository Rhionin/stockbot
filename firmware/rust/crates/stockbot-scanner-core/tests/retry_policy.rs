//! Acceptance tests for `retry_policy` (#718 §9, pure policy slice S-a).

use stockbot_scanner_core::{
    envelope_ms, jittered_ms, may_send, on_outcome, parse_retry_after_ms, Op, Outcome, RetryState,
    SplitMix64, FLUSH, HEARTBEAT,
};

fn step(s: RetryState, op: Op, out: Outcome, ra: Option<u64>, now: u64, r: u32) -> RetryState {
    on_outcome(s, op, out, ra, now, &mut move || r)
}

fn fixed_r(r: u32) -> impl FnMut() -> u32 {
    move || r
}

fn fr(r: u32) -> impl FnMut() -> u32 {
    move || r
}

fn go(s: RetryState, op: Op, o: Outcome, ra: Option<u64>, now: u64, r: u32) -> RetryState {
    on_outcome(s, op, o, ra, now, &mut fr(r))
}

fn wait_after_outage(
    mut state: RetryState,
    op: Op,
    out: Outcome,
    ra_ms: Option<u64>,
    now_ms: u64,
    r: u32,
) -> (RetryState, u64) {
    state = on_outcome(state, op, out, ra_ms, now_ms, &mut fixed_r(r));
    let i = op as usize;
    let gate = state.not_before_ms[i].max(state.rl_until_ms[i]);
    let wait = gate.saturating_sub(now_ms);
    (state, wait)
}

/// SDET-readable envelope sequence table (r=1000 → wait equals envelope).
#[test]
fn table_flush_and_heartbeat_envelope_sequences() {
    #[derive(Debug)]
    struct Row {
        k: u32,
        flush_ms: u64,
        heartbeat_ms: u64,
    }
    let rows = [
        Row {
            k: 0,
            flush_ms: 1_000,
            heartbeat_ms: 60_000,
        },
        Row {
            k: 1,
            flush_ms: 2_000,
            heartbeat_ms: 120_000,
        },
        Row {
            k: 2,
            flush_ms: 4_000,
            heartbeat_ms: 240_000,
        },
        Row {
            k: 3,
            flush_ms: 8_000,
            heartbeat_ms: 300_000,
        },
        Row {
            k: 4,
            flush_ms: 16_000,
            heartbeat_ms: 300_000,
        },
        Row {
            k: 5,
            flush_ms: 30_000,
            heartbeat_ms: 300_000,
        },
        Row {
            k: 6,
            flush_ms: 30_000,
            heartbeat_ms: 300_000,
        },
    ];
    for row in rows {
        assert_eq!(
            envelope_ms(&FLUSH, row.k),
            row.flush_ms,
            "flush k={}",
            row.k
        );
        assert_eq!(
            envelope_ms(&HEARTBEAT, row.k),
            row.heartbeat_ms,
            "heartbeat k={}",
            row.k
        );
    }
}

// §9 case 1
#[test]
fn flush_unreachable_sequence_r_1000() {
    let expected = [1_000, 2_000, 4_000, 8_000, 16_000, 30_000, 30_000, 30_000];
    let mut state = RetryState::default();
    let mut now = 0u64;
    for (i, &exp) in expected.iter().enumerate() {
        let (next, wait) =
            wait_after_outage(state, Op::Flush, Outcome::Unreachable, None, now, 1000);
        assert_eq!(wait, exp, "failure {}", i + 1);
        state = next;
        now += exp;
    }
}

// §9 case 2
#[test]
fn flush_unreachable_sequence_r_0() {
    let expected = [500, 1_000, 2_000, 4_000, 8_000, 15_000, 15_000];
    let mut state = RetryState::default();
    let mut now = 0u64;
    for (i, &exp) in expected.iter().enumerate() {
        let (next, wait) = wait_after_outage(state, Op::Flush, Outcome::Unreachable, None, now, 0);
        assert_eq!(wait, exp, "failure {}", i + 1);
        state = next;
        now += exp;
    }
}

// §9 case 3
#[test]
fn jitter_formula_and_r_clamp() {
    let d = 10_000u64;
    let lo = d * FLUSH.jitter_lo_permille / 1000;
    for r in [0u32, 1, 500, 1000, 1001] {
        let r_eff = r.min(1000);
        let want = lo + (d - lo) * r_eff as u64 / 1000;
        assert_eq!(jittered_ms(&FLUSH, d, r), want, "r={r}");
    }
}

// §9 case 4
#[test]
fn envelope_at_max_streak_saturates_to_cap() {
    let cap = envelope_ms(&FLUSH, u32::MAX);
    assert_eq!(cap, FLUSH.cap_ms);
    assert_eq!(envelope_ms(&HEARTBEAT, u32::MAX), HEARTBEAT.cap_ms);
}

// §9 case 5
#[test]
fn may_send_boundary() {
    let mut s = RetryState::default();
    s.not_before_ms[0] = 10_000;
    assert!(!may_send(&s, Op::Flush, 9_999));
    assert!(may_send(&s, Op::Flush, 10_000));
}

// §9 case 6 — Ok clears outage waits on every op, keeps rl_until
#[test]
fn ok_clears_shared_outage_waits_keeps_rl_until() {
    let mut s = RetryState::default();
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::Unreachable,
        None,
        0,
        &mut fixed_r(1000),
    );
    s.not_before_ms[1] = 99_000;
    s.rl_until_ms[1] = 50_000;
    s = on_outcome(s, Op::Flush, Outcome::Ok, None, 1_000, &mut fixed_r(1000));
    assert_eq!(s.outage_streak, 0);
    assert_eq!(s.not_before_ms, [0, 0]);
    assert_eq!(s.rl_until_ms[1], 50_000);
    let (_, wait) = wait_after_outage(s, Op::Flush, Outcome::Unreachable, None, 2_000, 1000);
    assert_eq!(wait, 1_000);
}

// §9 case 7
#[test]
fn reachable_4xx_no_wait() {
    let mut s = RetryState::default();
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::Reachable4xx,
        None,
        0,
        &mut fixed_r(1000),
    );
    assert_eq!(s.outage_streak, 0);
    assert_eq!(s.not_before_ms, [0, 0]);
}

// §9 case 8
#[test]
fn flush_429_ra_120_sets_rl_until() {
    let mut s = RetryState::default();
    let now = 1_000u64;
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::RateLimited,
        Some(120_000),
        now,
        &mut fixed_r(1000),
    );
    assert_eq!(s.rl_until_ms[0], now + 120_000);
}

// §9 cases 9–10
#[test]
fn flush_429_ra_envelope_vs_clamp() {
    let now = 0u64;
    for (ra, want_wait) in [(Some(0u64), 1_000), (Some(1_000), 1_000)] {
        let (_, wait) = wait_after_outage(
            RetryState::default(),
            Op::Flush,
            Outcome::RateLimited,
            ra,
            now,
            1000,
        );
        assert_eq!(wait, want_wait, "ra={ra:?}");
    }
    let (_, wait) = wait_after_outage(
        RetryState::default(),
        Op::Flush,
        Outcome::RateLimited,
        Some(86_400_000),
        now,
        1000,
    );
    assert_eq!(wait, 3_600_000);
}

// §9 cases 11–13 parse_retry_after_ms
#[test]
fn parse_retry_after_valid_and_trim() {
    assert_eq!(parse_retry_after_ms(Some("120")), Some(120_000));
    assert_eq!(parse_retry_after_ms(Some(" 120 ")), Some(120_000));
    assert_eq!(parse_retry_after_ms(Some("0")), Some(0));
}

#[test]
fn parse_retry_after_rejects_invalid() {
    for raw in [
        None,
        Some(""),
        Some("abc"),
        Some("-5"),
        Some("+5"),
        Some("1.5"),
        Some("0x10"),
        Some("Fri, 31 Dec 1999 23:59:59 GMT"),
    ] {
        assert_eq!(parse_retry_after_ms(raw), None, "{raw:?}");
    }
}

#[test]
fn parse_retry_after_overflow_saturates_seconds() {
    let ms = parse_retry_after_ms(Some("99999999999999999999")).unwrap();
    assert_eq!(ms, u64::MAX.saturating_mul(1000));
    let (_, wait) = wait_after_outage(
        RetryState::default(),
        Op::Flush,
        Outcome::RateLimited,
        Some(ms),
        0,
        1000,
    );
    assert_eq!(wait, 3_600_000);
}

// §9 case 14 — RA only on 5xx and 429 paths
#[test]
fn retry_after_only_server5xx_and_rate_limited() {
    let ra = Some(90_000u64);
    let (_, w5) = wait_after_outage(
        RetryState::default(),
        Op::Flush,
        Outcome::Server5xx,
        ra,
        0,
        1000,
    );
    assert_eq!(w5, 90_000);
    let (_, w401) = wait_after_outage(
        RetryState::default(),
        Op::Flush,
        Outcome::Unauth401NoCode,
        ra,
        0,
        1000,
    );
    assert_eq!(w401, 1_000);
    let (_, w_u) = wait_after_outage(
        RetryState::default(),
        Op::Flush,
        Outcome::Unreachable,
        ra,
        0,
        1000,
    );
    assert_eq!(w_u, 1_000);
}

// §9 case 15
#[test]
fn heartbeat_failure_sequence_r_1000() {
    let expected = [60_000, 120_000, 240_000, 300_000, 300_000];
    let mut state = RetryState::default();
    let mut now = 0u64;
    for (i, &exp) in expected.iter().enumerate() {
        let (next, wait) =
            wait_after_outage(state, Op::Heartbeat, Outcome::Unreachable, None, now, 1000);
        assert_eq!(wait, exp, "hb failure {}", i + 1);
        state = next;
        now += exp;
    }
}

// §9 case 16 — shared outage index for heartbeat
#[test]
fn heartbeat_uses_shared_outage_streak_index() {
    let mut s = RetryState::default();
    for _ in 0..3 {
        s = on_outcome(
            s,
            Op::Flush,
            Outcome::Unreachable,
            None,
            0,
            &mut fixed_r(1000),
        );
    }
    assert_eq!(s.outage_streak, 3);
    let (_, wait) = wait_after_outage(s, Op::Heartbeat, Outcome::Unreachable, None, 0, 1000);
    assert_eq!(wait, 300_000);
}

// §9 case 17 — 429 does not feed outage streak
#[test]
fn rate_limited_does_not_touch_outage_streak_or_other_op_gates() {
    let mut s = RetryState {
        outage_streak: 2,
        not_before_ms: [0, 5_000],
        ..Default::default()
    };
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::RateLimited,
        Some(2_000),
        0,
        &mut fixed_r(1000),
    );
    assert_eq!(s.outage_streak, 2);
    assert_eq!(s.not_before_ms[1], 5_000);
    assert_eq!(s.op_rl_streak[0], 1);
}

// §9 case 18 — heartbeat Ok clears flush gate
#[test]
fn heartbeat_ok_clears_flush_not_before() {
    let mut s = RetryState::default();
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::Unreachable,
        None,
        0,
        &mut fixed_r(1000),
    );
    assert_eq!(s.not_before_ms[0], 1_000);
    s = on_outcome(s, Op::Heartbeat, Outcome::Ok, None, 500, &mut fixed_r(1000));
    assert!(may_send(&s, Op::Flush, 500));
}

// §9 case 25 — no-code 401 then Ok resets streak
#[test]
fn unauth401_then_ok_resets_next_wait_to_base() {
    let mut s = RetryState::default();
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::Unauth401NoCode,
        None,
        0,
        &mut fixed_r(1000),
    );
    s = on_outcome(s, Op::Flush, Outcome::Ok, None, 1_000, &mut fixed_r(1000));
    let (_, wait) = wait_after_outage(s, Op::Flush, Outcome::Unreachable, None, 2_000, 1000);
    assert_eq!(wait, 1_000);
}

// §9 case 27 — u64 now_ms, no u32 truncation
#[test]
fn may_send_u32_boundary_now_ms() {
    let base = 1u64 << 32;
    let mut s = RetryState::default();
    s.not_before_ms[0] = base - 1_000;
    assert!(may_send(&s, Op::Flush, base + 5));
}

// §9 case 28 — one rng draw per scheduled wait, none on Ok
#[test]
fn rng_draw_count_per_outcome() {
    use std::cell::Cell;
    let draws = Cell::new(0usize);
    let mut rng = || {
        draws.set(draws.get() + 1);
        1000u32
    };
    let mut s = RetryState::default();
    s = on_outcome(s, Op::Flush, Outcome::Unreachable, None, 0, &mut rng);
    assert_eq!(draws.get(), 1);
    s = on_outcome(s, Op::Flush, Outcome::Ok, None, 1, &mut rng);
    assert_eq!(draws.get(), 1);
    on_outcome(s, Op::Flush, Outcome::RateLimited, None, 2, &mut rng);
    assert_eq!(draws.get(), 2);
}

#[test]
fn per_op_not_before_and_rl_streaks_independent() {
    let mut s = RetryState::default();
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::RateLimited,
        Some(5_000),
        0,
        &mut fixed_r(1000),
    );
    assert_eq!(s.op_rl_streak[0], 1);
    assert_eq!(s.op_rl_streak[1], 0);
    s = on_outcome(
        s,
        Op::Flush,
        Outcome::RateLimited,
        None,
        10_000,
        &mut fixed_r(1000),
    );
    assert_eq!(s.op_rl_streak[0], 2);
    let w2 = envelope_ms(&FLUSH, 1);
    assert_eq!(s.rl_until_ms[0], 10_000 + w2);
}

#[test]
fn default_retry_state_is_open() {
    let s = RetryState::default();
    assert!(may_send(&s, Op::Flush, 0));
    assert!(may_send(&s, Op::Heartbeat, u64::MAX));
}

// --- SDET supplemental tests (mutant killers) ---

#[test]
fn outage_streak_u32_max_no_panic_and_cap_wait() {
    for o in [
        Outcome::Unreachable,
        Outcome::Server5xx,
        Outcome::Unauth401NoCode,
    ] {
        let s = RetryState {
            outage_streak: u32::MAX,
            ..Default::default()
        };
        let s = go(s, Op::Flush, o, None, 1_000, 1000);
        assert_eq!(s.outage_streak, u32::MAX);
        assert_eq!(s.not_before_ms[0], 1_000 + FLUSH.cap_ms);
    }
    let s = RetryState {
        op_rl_streak: [u32::MAX, u32::MAX],
        ..Default::default()
    };
    let s = go(s, Op::Heartbeat, Outcome::RateLimited, None, 0, 1000);
    assert_eq!(s.op_rl_streak[1], u32::MAX);
    assert_eq!(s.rl_until_ms[1], HEARTBEAT.cap_ms);
}

#[test]
fn gate_saturates_near_u64_max() {
    let s = go(
        RetryState::default(),
        Op::Flush,
        Outcome::Unreachable,
        None,
        u64::MAX - 10,
        1000,
    );
    assert_eq!(s.not_before_ms[0], u64::MAX);
    let s = go(
        RetryState::default(),
        Op::Flush,
        Outcome::RateLimited,
        None,
        u64::MAX - 10,
        1000,
    );
    assert_eq!(s.rl_until_ms[0], u64::MAX);
}

#[test]
fn reachable_4xx_after_failures_resets_like_ok() {
    for o in [Outcome::Reachable4xx, Outcome::Ok] {
        let mut s = RetryState::default();
        for _ in 0..3 {
            s = go(s, Op::Flush, Outcome::Unreachable, None, 0, 1000);
        }
        s = go(s, Op::Flush, Outcome::RateLimited, None, 0, 1000);
        s = go(s, Op::Heartbeat, Outcome::Unreachable, None, 0, 1000);
        let rl = s.rl_until_ms;
        s = go(s, Op::Flush, o, None, 5, 1000);
        assert_eq!(s.outage_streak, 0, "{o:?}");
        assert_eq!(s.op_rl_streak[0], 0, "{o:?}");
        assert_eq!(s.not_before_ms, [0, 0], "{o:?}");
        assert_eq!(s.rl_until_ms, rl, "{o:?}");
    }
}

#[test]
fn may_send_uses_rl_until_and_own_op_slot() {
    let s = go(
        RetryState::default(),
        Op::Flush,
        Outcome::RateLimited,
        Some(120_000),
        0,
        1000,
    );
    assert!(!may_send(&s, Op::Flush, 119_999));
    assert!(may_send(&s, Op::Flush, 120_000));
    assert!(may_send(&s, Op::Heartbeat, 0));
    let s = go(
        RetryState::default(),
        Op::Heartbeat,
        Outcome::Unreachable,
        None,
        0,
        1000,
    );
    assert!(!may_send(&s, Op::Heartbeat, 59_999));
    assert!(may_send(&s, Op::Heartbeat, 60_000));
    assert!(may_send(&s, Op::Flush, 0));
    let s = go(
        RetryState::default(),
        Op::Heartbeat,
        Outcome::RateLimited,
        Some(7_000),
        0,
        1000,
    );
    assert!(!may_send(&s, Op::Heartbeat, 59_999));
    assert!(may_send(&s, Op::Flush, 0));
}

#[test]
fn heartbeat_429_streak_and_ra_clamp() {
    let mut s = RetryState::default();
    s = go(s, Op::Heartbeat, Outcome::RateLimited, None, 0, 1000);
    s = go(s, Op::Heartbeat, Outcome::RateLimited, None, 0, 1000);
    assert_eq!(s.op_rl_streak, [0, 2]);
    assert_eq!(s.rl_until_ms, [0, 120_000]);
    let s = go(
        RetryState::default(),
        Op::Heartbeat,
        Outcome::RateLimited,
        Some(86_400_000),
        0,
        1000,
    );
    assert_eq!(s.rl_until_ms[1], 3_600_000);
}

#[test]
fn server5xx_ra_clamp_and_floor() {
    for (op, i, env) in [(Op::Flush, 0usize, 1_000u64), (Op::Heartbeat, 1, 60_000)] {
        let s = go(
            RetryState::default(),
            op,
            Outcome::Server5xx,
            Some(86_400_000),
            0,
            1000,
        );
        assert_eq!(s.not_before_ms[i], 3_600_000);
        let s = go(
            RetryState::default(),
            op,
            Outcome::Server5xx,
            Some(0),
            0,
            1000,
        );
        assert_eq!(s.not_before_ms[i], env);
    }
}

#[test]
fn heartbeat_jitter_r0_and_r500() {
    let mut now = 0u64;
    let mut s = RetryState::default();
    for exp in [30_000u64, 60_000, 120_000, 150_000, 150_000] {
        s = go(s, Op::Heartbeat, Outcome::Unreachable, None, now, 0);
        assert_eq!(s.not_before_ms[1] - now, exp);
        now = s.not_before_ms[1];
    }
    let s = go(
        RetryState::default(),
        Op::Heartbeat,
        Outcome::Unreachable,
        None,
        0,
        500,
    );
    assert_eq!(s.not_before_ms[1], 45_000);
}

#[test]
fn unauth401_feeds_shared_streak() {
    let mut s = RetryState::default();
    for (k, exp) in [(1u32, 1_000u64), (2, 2_000), (3, 4_000)] {
        s = go(s, Op::Flush, Outcome::Unauth401NoCode, None, 0, 1000);
        assert_eq!(s.outage_streak, k);
        assert_eq!(s.not_before_ms[0], exp);
    }
}

#[test]
fn parse_retry_after_tab_and_edges() {
    assert_eq!(parse_retry_after_ms(Some("\t7\t ")), Some(7_000));
    for raw in [
        " ",
        "\t",
        "1 2",
        "12\n",
        "١٢",
        "Fri, 31 Dec 1999 23:59:59 GMT",
        "Sun, 06 Nov 1994 08:49:37 GMT",
    ] {
        assert_eq!(parse_retry_after_ms(Some(raw)), None, "{raw:?}");
    }
}

#[test]
fn splitmix64_known_vector_and_determinism() {
    let mut a = SplitMix64::new(0);
    let mut b = SplitMix64::new(0);
    assert_eq!(a.next_u32(), 0x7B1D_CDAF);
    assert_eq!(b.next_u32(), 0x7B1D_CDAF);
    assert_eq!(a.next_u32(), b.next_u32());
}

#[test]
fn jitter_literal_vectors_and_odd_d() {
    for (r, want) in [
        (0u32, 5_000u64),
        (1, 5_005),
        (500, 7_500),
        (1000, 10_000),
        (1001, 10_000),
        (u32::MAX, 10_000),
    ] {
        assert_eq!(jittered_ms(&FLUSH, 10_000, r), want, "r={r}");
    }
    for (d, r, want) in [
        (0u64, 1000u32, 0u64),
        (1, 0, 0),
        (1, 1000, 1),
        (3, 0, 1),
        (3, 1000, 3),
        (7, 0, 3),
        (7, 1000, 7),
    ] {
        assert_eq!(jittered_ms(&FLUSH, d, r), want, "d={d} r={r}");
    }
}

#[test]
fn parse_retry_after_whitespace_digits_and_overflow() {
    assert_eq!(parse_retry_after_ms(Some("\t120\t")), Some(120_000));
    assert_eq!(parse_retry_after_ms(Some(" \t 7 \t ")), Some(7_000));
    assert_eq!(parse_retry_after_ms(Some("007")), Some(7_000));
    for raw in [
        "120\r", "120\r\n", "\n120", "1 2", "   ", "\t", "5e3", "\u{ff15}", "\u{663}", "\u{a0}5",
        "-0",
    ] {
        assert_eq!(parse_retry_after_ms(Some(raw)), None, "{raw:?}");
    }
    assert_eq!(
        parse_retry_after_ms(Some("18446744073709551616")),
        Some(u64::MAX)
    );
    assert_eq!(
        parse_retry_after_ms(Some("18446744073709551615")),
        Some(u64::MAX)
    );
    assert_eq!(
        parse_retry_after_ms(Some("18446744073709551")),
        Some(18_446_744_073_709_551_000)
    );
}

#[test]
fn splitmix64_reference_vectors_and_determinism() {
    let mut g = SplitMix64::new(0);
    let got: Vec<u32> = (0..4).map(|_| g.next_u32()).collect();
    assert_eq!(got, [0x7b1d_cdaf, 0xa1b9_65f4, 0x8009_454f, 0x724c_81ec]);
    let (mut a, mut b) = (
        SplitMix64::new(0x1234_5678_9abc_def0),
        SplitMix64::new(0x1234_5678_9abc_def0),
    );
    for _ in 0..64 {
        assert_eq!(a.next_u32(), b.next_u32());
    }
}

#[test]
fn rate_limit_uses_only_rl_until_and_own_op() {
    let s = step(
        RetryState::default(),
        Op::Heartbeat,
        Outcome::RateLimited,
        None,
        5_000,
        1000,
    );
    assert_eq!(s.rl_until_ms, [0, 5_000 + 60_000]);
    assert_eq!(s.not_before_ms, [0, 0]);
    assert_eq!((s.outage_streak, s.op_rl_streak), (0, [0, 1]));
    assert!(may_send(&s, Op::Flush, 5_000));
    assert!(!may_send(&s, Op::Heartbeat, 5_000 + 59_999));
    assert!(may_send(&s, Op::Heartbeat, 5_000 + 60_000));
    let s = step(
        RetryState::default(),
        Op::Flush,
        Outcome::RateLimited,
        None,
        5_000,
        1000,
    );
    assert_eq!(s.rl_until_ms, [5_000 + 1_000, 0]);
    assert_eq!(s.not_before_ms, [0, 0]);
    assert!(!may_send(&s, Op::Flush, 5_999));
    assert!(may_send(&s, Op::Flush, 6_000));
    assert!(may_send(&s, Op::Heartbeat, 5_000));
}

#[test]
fn splitmix64_next_permille_range_and_advances_state() {
    let mut rng = SplitMix64::new(42);
    let mut min = u32::MAX;
    let mut max = 0u32;
    for _ in 0..200_000 {
        let v = rng.next_permille();
        assert!(v <= 1000);
        min = min.min(v);
        max = max.max(v);
    }
    assert_eq!(min, 0);
    assert_eq!(max, 1000);

    let mut u = SplitMix64::new(99);
    let mut p = SplitMix64::new(99);
    let raw = u.next_u32();
    assert_eq!(p.next_permille(), ((raw as u64 * 1001) >> 32) as u32);
    for _ in 0..20 {
        assert_eq!(u.next_u32(), p.next_u32());
    }
}

#[test]
fn on_outcome_flush_unreachable_with_splitmix_permille_spreads_waits() {
    let mut rng = SplitMix64::new(0xdead_beef_cafe);
    let mut state = RetryState::default();
    let mut now = 0u64;
    let mut waits = Vec::new();
    for step in 0..8u32 {
        let d = envelope_ms(&FLUSH, step);
        state = on_outcome(
            state,
            Op::Flush,
            Outcome::Unreachable,
            None,
            now,
            &mut || rng.next_permille(),
        );
        let w = state.not_before_ms[0] - now;
        waits.push(w);
        assert!(w >= d / 2 && w <= d, "wait {w} not in [{}/2, {d}]", d / 2);
        now = state.not_before_ms[0];
    }
    let all_full = waits
        .iter()
        .enumerate()
        .all(|(i, &w)| w == envelope_ms(&FLUSH, i as u32));
    assert!(!all_full, "waits must not all equal envelope (no jitter)");
}
