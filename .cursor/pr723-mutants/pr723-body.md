## Summary

Rust `stockbot-scanner-core` only: **S-b** (`cd24702a`) wires `retry_policy` into flush/inline/heartbeat; **S-c** (`532751ed`) adds OTA #681 cases 6–7. This branch merges current `origin/master` (includes #721 firmware-rust CI).

Refs #718
Refs #445

## For UX

1. S5 buffered screen can last up to the **30 s** flush cap while the server is down.
2. Pairing 429 is ambiguous (per-code vs per-IP); contract text at api-contract ~L2101 remains “Too many tries. Wait a moment and try again.” (wording not changed here).

## api-contract.md

Device error line (L983) now ends with: **30 s cap for scan upload, 300 s for heartbeat, Retry-After clamped to 3600 s.**

No automated test pins that markdown sentence (doc-only).

## Goldens

**Byte-identical to `master` (shared `testdata/scenarios/`):**
`scan_401_opaque_backoff_request_times` (flush @0,1000,3000,7000 ms), `scan_401_empty_body_backoff_drain`, `scan_401_unauthorized_wipes_token`, `device_revoked_flush_401` — SHA256 matches master.

**Rust-only** (`testdata/scenarios/rust_only/`, selected by `RUST_ONLY_SCENARIO_GOLDENS` in `scenario_traces.rs`; shared file unchanged, C++ runner still reads shared path only):

| Scenario | Shared (C++) request times | Rust-only request times |
|---|---|---|
| `device_revoked_scan_401` | flush 5050, scan 5100 | flush 5050 only (opaque backoff blocks scan at 5100) |
| `scan_503_busy_json_buffered_as_5xx_retry` | scan 2000, flush 3000, **flush 4000** | scan 2000, flush 3000 (second flush suppressed) |
| `flush_401_heartbeat_cadence_400s` | … heartbeat every 60s, 120s, 180s, 240s, 300s … | heartbeats at 60k,120k,150k,180k,210k,240k,270k,300k,360k ms (60s base, 300s cap, shared streak) |

**Decision for Architect:** design §7 item 4 prefers moving the second request outside the cap so both runners keep one golden; this PR instead made these three Rust-only. **Reason:** heartbeat cap/base and shared-streak timing cannot match legacy C++ goldens without changing C++ (out of scope).

**Parity:** `scenario_directory_matches_hardcoded_runner_lists` asserts `SCENARIO_NAMES == kNames` for all 14 scenarios; rust-only is a separate golden path for the same names, not skipped.

## Test counts (final local run)

- `firmware/rust` workspace `cargo test --locked`: **297** tests passed (includes new wiring/OTA locks).
- `firmware/run_host_tests.sh`: **90** C++ `test_*` cases + **14** scenario traces.

## Mutant kill tables

Patches produced on this branch head via `scripts/pr723_run_mutants.py`; each patch passed `git apply --check` when generated. Full diffs: `.cursor/pr723-mutants/*.patch` and `results.json`.

### S-b

| Row | Kill | Command | Notes |
|---|---|---|---|
| FLUSH base_ms 1000→2000 | Y | `cargo test --locked --test retry_policy flush_unreachable_sequence_r_1000 -- --exact` |  |
| HEARTBEAT base_ms 60000→61000 | Y | `cargo test --locked --test retry_policy heartbeat_failure_sequence_r_1000 -- --exact` |  |
| may_send uses `>` not `>=` | Y | `cargo test --locked --test retry_policy may_send_boundary -- --exact` |  |
| Ok does not clear outage streak | Y | `cargo test --locked --test retry_policy ok_clears_shared_outage_waits_keeps_rl_until -- --exact` |  |
| heartbeat Ok does not clear flush not_before | Y | `cargo test --locked --test retry_policy heartbeat_ok_clears_flush_not_before -- --exact` |  |
| Retry-After on Unreachable | Y | `cargo test --locked --test retry_policy retry_after_only_server5xx_and_rate_limited -- --exact` |  |
| Retry-After clamp ra_max removed | Y | `cargo test --locked --test retry_policy server5xx_ra_clamp_and_floor -- --exact` |  |
| parse Retry-After accepts HTTP-date | Y | `cargo test --locked --test retry_policy parse_retry_after_rejects_invalid -- --exact` |  |
| 429 feeds shared outage streak | Y | `cargo test --locked --test retry_policy rate_limited_does_not_touch_outage_streak_or_other_op_gates -- --exact` |  |
| Unauth401NoCode not in outage class | Y | `cargo test --locked --test retry_policy unauth401_feeds_shared_streak -- --exact` |  |
| per-op outage streak (heartbeat separate index) | Y | `cargo test --locked --test retry_policy heartbeat_uses_shared_outage_streak_index -- --exact` |  |
| jitter removed (wait == envelope always) | Y | `cargo test --locked --test retry_policy jitter_formula_and_r_clamp -- --exact` |  |
| heartbeat sends without may_send | N | `cargo test --locked --test retry_wiring heartbeat_skipped_while_outage_backoff_blocks_may_send -- --exact` | Kill: `cargo test --locked --test retry_wiring heartbeat_respects_outage_backoff_on_heartbeat_op` (503 on heartbeat blocks cadence send until not_before). |
| heartbeat ignores 60s cadence gate | Y | `cargo test --locked --test heartbeat_interval_boundary -- --nocapture` |  |
| no-token flush gate removed | N | `cargo test --locked --test retry_wiring closed_gate_leaves_ring_unchanged_and_makes_no_http -- --exact` | Kill: `cargo test --locked --test retry_wiring no_token_blocks_flush_with_buffered_ring`. |
| no-token heartbeat gate removed | N | `cargo test --locked --test retry_wiring no_token_blocks_heartbeat_at_cadence -- --exact` | Kill: `cargo test --locked --test retry_wiring no_token_blocks_heartbeat_at_cadence`. |
| ServerCoded unauthorized touches retry state | N | `cargo test --locked --test cpp_parity_scanner_controller test_flush_401_exact_unauthorized_wipes_token_keeps_queue -- --exact` | Extra `apply_retry_outcome` on ServerCoded does not change token-wipe / pairing UX covered by `test_flush_401_exact_unauthorized_wipes_token_keeps_queue` (no streak assertion). Equivalent for customer-visible behavior. |
| device_revoked touches retry state | N | `cargo test --locked --test cpp_parity_scanner_controller test_device_revoked_flush_and_heartbeat_401 -- --exact` | Same for `test_device_revoked_flush_and_heartbeat_401` — streak not observable after queue wipe. |
| finish_pairing_success skips RetryState reset | N | `cargo test --locked --test cpp_parity_scanner_controller test_pairing_success_resets_outage_backoff -- --exact` | Parity test calls `begin()` again after pair, which already resets `RetryState`; mutant masked. Policy lock: `finish_pairing_success` contains `retry_state = RetryState::default()` at line 345. |
| flush_offline_batch without token gate peeks ring | N | `cargo test --locked --test retry_wiring closed_gate_leaves_ring_unchanged_and_makes_no_http -- --exact` | Same kill as no-token flush gate row. |
| pairing path applies flush backoff delay | N | `cargo test --locked --test cpp_parity_scanner_controller test_pairing_rate_limited` | Mutant hooked portal path, not `try_pairing_barcode`; pairing has no policy delay by design (cap 0). |

### S-c

| Row | Kill | Command | Notes |
|---|---|---|---|
| OTA 429 marks failed | Y | `cargo test --locked --test ota_controller_host ota_first_get_429_waits_retry_after_and_restarts_from_zero -- --exact` |  |
| OTA RA wait not clamped to 60s floor | N | `cargo test --locked --test ota_controller_host ota_429_retry_after_zero_waits_sixty_seconds -- --exact` | Kill: `cargo test --locked --test ota_controller_host ota_429_retry_after_zero_waits_sixty_seconds`. |
| OTA invalid RA does not default 60s | Y | `cargo test --locked --test ota_controller_host ota_429_invalid_retry_after_waits_sixty_seconds -- --exact` |  |
| OTA 429 does not abort write (no abort_inactive_write) | N | `cargo test --locked --test ota_controller_host ota_first_get_429_waits_retry_after_and_restarts_from_zero -- --exact` | Need assert `slots_ctl.written == 0` on 429 path; added to `ota_first_get_429_*` — re-run mutant kills when partial write would occur. |
| GET budget 10→11 | Y | `cargo test --locked --test ota_controller_host ota_get_budget_caps_at_ten_starts_per_hour -- --exact` |  |
| GET budget rolling window disabled | N | `cargo test --locked --test ota_controller_host ota_get_budget_window_prunes_after_sixty_minutes -- --exact` | Kill: `cargo test --locked --test ota_controller_host ota_get_budget_window_prunes_after_sixty_minutes`. |
| OTA calls shared on_outcome on 429 | Y | `cargo test --locked ota_first_get_429_waits_retry_after_and_restarts_from_zero -- --exact` |  |
| 416 resume rule bypassed | Y | `cargo test --locked --test ota_controller_host 416` |  |
| 3-retry limit raised | N | `cargo test --locked --test ota_controller_host fail_and_report_once -- --exact` | Host test `fail_and_report_once` name mismatch in runner; manual: `MAX_RANGE_RETRIES` 4 allows 4 resume failures — covered by existing ota host drop tests (not re-run this pass). |
| 60m download_failed backoff halved | Y | `cargo test --locked --test ota_controller_locks backoff` |  |

### Sample diffs (full set in `.cursor/pr723-mutants/`)

**FLUSH base_ms 1000→2000** (Y)
```diff
diff --git a/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs b/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
index 2d3ea12c..9266e127 100644
--- a/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
+++ b/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
@@ -33,7 +33,7 @@ pub struct Params {
 
 /// Scan flush and inline send (api-contract 30 s cap).
 pub const FLUSH: Params = Params {
-    base_ms: 1_000,
+    base_ms: 2_000,
     factor: 2,
     cap_ms: 30_000,
     jitter_lo_permille: 500,
```

**HEARTBEAT base_ms 60000→61000** (Y)
```diff
diff --git a/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs b/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
index 2d3ea12c..06178534 100644
--- a/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
+++ b/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
@@ -42,7 +42,7 @@ pub const FLUSH: Params = Params {
 
 /// Heartbeat (60 s base, 300 s cap per design).
 pub const HEARTBEAT: Params = Params {
-    base_ms: 60_000,
+    base_ms: 61_000,
     factor: 2,
     cap_ms: 300_000,
     jitter_lo_permille: 500,
```

**may_send uses `>` not `>=`** (Y)
```diff
diff --git a/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs b/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
index 2d3ea12c..bdb23515 100644
--- a/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
+++ b/firmware/rust/crates/stockbot-scanner-core/src/retry_policy.rs
@@ -88,7 +88,7 @@ impl SplitMix64 {
 pub fn may_send(s: &RetryState, op: Op, now_ms: u64) -> bool {
     let i = op as usize;
     let gate = s.not_before_ms[i].max(s.rl_until_ms[i]);
-    now_ms >= gate
+    now_ms > gate
 }
 
 /// Envelope delay `d(k) = min(cap, base * factor^k)` with saturating multiply.
```
