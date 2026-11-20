#!/usr/bin/env python3
"""Apply each mutation, record git diff, run kill test, revert. Run from repo root."""
from __future__ import annotations

import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CRATE = ROOT / "firmware/rust/crates/stockbot-scanner-core"


@dataclass
class Mutant:
    slice: str
    name: str
    path: Path
    old: str
    new: str
    cmd: list[str]
    count: str  # one|all — must fail at least one test


MUTANTS: list[Mutant] = [
    # S-b — retry_policy.rs pure
    Mutant(
        "S-b",
        "FLUSH base_ms 1000→2000",
        CRATE / "src/retry_policy.rs",
        "    base_ms: 1_000,\n    factor: 2,\n    cap_ms: 30_000,",
        "    base_ms: 2_000,\n    factor: 2,\n    cap_ms: 30_000,",
        ["cargo", "test", "--locked", "--test", "retry_policy", "flush_unreachable_sequence_r_1000", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "HEARTBEAT base_ms 60000→61000",
        CRATE / "src/retry_policy.rs",
        "    base_ms: 60_000,\n    factor: 2,\n    cap_ms: 300_000,",
        "    base_ms: 61_000,\n    factor: 2,\n    cap_ms: 300_000,",
        ["cargo", "test", "--locked", "--test", "retry_policy", "heartbeat_failure_sequence_r_1000", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "FLUSH factor 2→3",
        CRATE / "src/retry_policy.rs",
        "    factor: 2,\n    cap_ms: 30_000,",
        "    factor: 3,\n    cap_ms: 30_000,",
        ["cargo", "test", "--locked", "--test", "retry_policy", "flush_unreachable_sequence_r_1000", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "FLUSH cap_ms 30000→31000",
        CRATE / "src/retry_policy.rs",
        "    cap_ms: 30_000,\n    jitter_lo_permille: 500,",
        "    cap_ms: 31_000,\n    jitter_lo_permille: 500,",
        ["cargo", "test", "--locked", "--test", "retry_policy", "flush_unreachable_sequence_r_1000", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "HEARTBEAT factor 2→3",
        CRATE / "src/retry_policy.rs",
        "/// Heartbeat (60 s base, 300 s cap per design).\npub const HEARTBEAT: Params = Params {\n    base_ms: 60_000,\n    factor: 2,",
        "/// Heartbeat (60 s base, 300 s cap per design).\npub const HEARTBEAT: Params = Params {\n    base_ms: 60_000,\n    factor: 3,",
        ["cargo", "test", "--locked", "--test", "retry_policy", "heartbeat_failure_sequence_r_1000", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "HEARTBEAT cap_ms 300000→301000",
        CRATE / "src/retry_policy.rs",
        "    factor: 2,\n    cap_ms: 300_000,",
        "    factor: 2,\n    cap_ms: 301_000,",
        ["cargo", "test", "--locked", "--test", "retry_policy", "heartbeat_failure_sequence_r_1000", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "may_send uses `>` not `>=`",
        CRATE / "src/retry_policy.rs",
        "    now_ms >= gate",
        "    now_ms > gate",
        ["cargo", "test", "--locked", "--test", "retry_policy", "may_send_boundary", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "Ok does not clear outage streak",
        CRATE / "src/retry_policy.rs",
        "            s.outage_streak = 0;\n            s.op_rl_streak[i] = 0;\n            s.not_before_ms = [0; 2];",
        "            s.op_rl_streak[i] = 0;\n            s.not_before_ms[i] = 0;",
        ["cargo", "test", "--locked", "--test", "retry_policy", "ok_clears_shared_outage_waits_keeps_rl_until", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "heartbeat Ok does not clear flush not_before",
        CRATE / "src/retry_policy.rs",
        "            s.not_before_ms = [0; 2];",
        "            s.not_before_ms[i] = 0;",
        ["cargo", "test", "--locked", "--test", "retry_policy", "heartbeat_ok_clears_flush_not_before", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "Retry-After on Unreachable",
        CRATE / "src/retry_policy.rs",
        """        Outcome::Unreachable | Outcome::Server5xx | Outcome::Unauth401NoCode => {
            let k = s.outage_streak;
            s.outage_streak = k.saturating_add(1);
            let mut w = jittered_ms(p, envelope_ms(p, k), rng());
            if matches!(out, Outcome::Server5xx) {
                if let Some(ra) = ra_ms {
                    w = w.max(ra.min(p.ra_max_ms));
                }
            }""",
        """        Outcome::Unreachable | Outcome::Server5xx | Outcome::Unauth401NoCode => {
            let k = s.outage_streak;
            s.outage_streak = k.saturating_add(1);
            let mut w = jittered_ms(p, envelope_ms(p, k), rng());
            if let Some(ra) = ra_ms {
                w = w.max(ra.min(p.ra_max_ms));
            }""",
        ["cargo", "test", "--locked", "--test", "retry_policy", "retry_after_only_server5xx_and_rate_limited", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "Retry-After clamp ra_max removed",
        CRATE / "src/retry_policy.rs",
        "w = w.max(ra.min(p.ra_max_ms));",
        "w = w.max(ra);",
        ["cargo", "test", "--locked", "--test", "retry_policy", "server5xx_ra_clamp_and_floor", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "parse Retry-After accepts HTTP-date",
        CRATE / "src/retry_policy.rs",
        "    if !trimmed.bytes().all(|b| b.is_ascii_digit()) {\n        return None;\n    }",
        "    if trimmed.contains(',') {\n        return Some(60_000);\n    }\n    if !trimmed.bytes().all(|b| b.is_ascii_digit()) {\n        return None;\n    }",
        ["cargo", "test", "--locked", "--test", "retry_policy", "parse_retry_after_rejects_invalid", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "429 feeds shared outage streak",
        CRATE / "src/retry_policy.rs",
        "        Outcome::RateLimited => {\n            let k = s.op_rl_streak[i];",
        "        Outcome::RateLimited => {\n            let k = s.outage_streak;\n            s.outage_streak = k.saturating_add(1);\n            let k = s.op_rl_streak[i];",
        ["cargo", "test", "--locked", "--test", "retry_policy", "rate_limited_does_not_touch_outage_streak_or_other_op_gates", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "Unauth401NoCode not in outage class",
        CRATE / "src/retry_policy.rs",
        "        Outcome::Unreachable | Outcome::Server5xx | Outcome::Unauth401NoCode => {",
        "        Outcome::Unreachable | Outcome::Server5xx => {",
        ["cargo", "test", "--locked", "--test", "retry_policy", "unauth401_feeds_shared_streak", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "per-op outage streak (heartbeat separate index)",
        CRATE / "src/retry_policy.rs",
        "            let k = s.outage_streak;\n            s.outage_streak = k.saturating_add(1);",
        "            let k = s.op_rl_streak[i];\n            s.op_rl_streak[i] = k.saturating_add(1);",
        ["cargo", "test", "--locked", "--test", "retry_policy", "heartbeat_uses_shared_outage_streak_index", "--", "--exact"],
        "one",
    ),
    Mutant(
        "S-b",
        "jitter removed (wait == envelope always)",
        CRATE / "src/retry_policy.rs",
        "    lo.saturating_add(span.saturating_mul(r) / 1000)",
        "    d",
        ["cargo", "test", "--locked", "--test", "retry_policy", "jitter_formula_and_r_clamp", "--", "--exact"],
        "one",
    ),
    # S-b — scanner_controller wiring
    Mutant(
        "S-b",
        "heartbeat sends without may_send",
        CRATE / "src/scanner_controller.rs",
        "        if !self.device_may_send_heartbeat_traffic(now_ms) {\n            return;\n        }",
        "        if false && !self.device_may_send_heartbeat_traffic(now_ms) {\n            return;\n        }",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "retry_wiring",
            "heartbeat_respects_outage_backoff_on_heartbeat_op",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "heartbeat ignores 60s cadence gate",
        CRATE / "src/scanner_controller.rs",
        "        if now_ms - self.last_heartbeat_ms < HEARTBEAT_INTERVAL_MS {\n            return;\n        }",
        "        if false && now_ms - self.last_heartbeat_ms < HEARTBEAT_INTERVAL_MS {\n            return;\n        }",
        ["cargo", "test", "--locked", "--test", "heartbeat_interval_boundary", "--", "--nocapture"],
        "one",
    ),
    Mutant(
        "S-b",
        "no-token flush gate removed",
        CRATE / "src/scanner_controller.rs",
        "        if self.config.device_token.is_empty() {\n            return false;\n        }",
        "        if false && self.config.device_token.is_empty() {\n            return false;\n        }",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "retry_wiring",
            "no_token_blocks_flush_with_buffered_ring",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "no-token heartbeat gate removed",
        CRATE / "src/scanner_controller.rs",
        "        if self.state.operational != OperationalState::Paired || self.config.device_token.is_empty()\n        {\n            return;\n        }",
        "        if self.state.operational != OperationalState::Paired {\n            return;\n        }",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "retry_wiring",
            "no_token_blocks_heartbeat_at_cadence",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "flush ignores may_send gate",
        CRATE / "src/scanner_controller.rs",
        "        if !self.device_may_send_flush_traffic(now_ms) {\n            return;\n        }",
        "        if false && !self.device_may_send_flush_traffic(now_ms) {\n            return;\n        }",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "retry_wiring",
            "flush_respects_outage_backoff_between_attempts",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "closed token gate allows heartbeat HTTP",
        CRATE / "src/scanner_controller.rs",
        "    fn device_may_send_heartbeat_traffic(&self, now_ms: u64) -> bool {\n        if self.config.device_token.is_empty() {\n            return false;\n        }",
        "    fn device_may_send_heartbeat_traffic(&self, now_ms: u64) -> bool {\n        if false && self.config.device_token.is_empty() {\n            return false;\n        }",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "retry_wiring",
            "no_token_blocks_heartbeat_at_cadence",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "retry RNG seed pinned constant",
        CRATE / "src/scanner_controller.rs",
        "        let seed =\n            (hw_random_nonce as u64).wrapping_shl(32) ^ now_ms ^ (self.config.boot_count as u64);",
        "        let seed = 1_u64;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "cpp_parity_scanner_controller",
            "test_scan_503_busy_json_buffered_as_5xx_retry",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "ServerCoded unauthorized touches retry state",
        CRATE / "src/scanner_controller.rs",
        "            DeviceAuth401Kind::ServerCoded => {\n                self.nvs.clear_device_token();",
        "            DeviceAuth401Kind::ServerCoded => {\n                self.apply_retry_outcome(op, Outcome::Unauth401NoCode, None, now_ms);\n                self.nvs.clear_device_token();",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "cpp_parity_scanner_controller",
            "test_flush_401_exact_unauthorized_wipes_token_keeps_queue",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "device_revoked touches retry state",
        CRATE / "src/scanner_controller.rs",
        "            DeviceAuth401Kind::DeviceRevoked => {\n                let _ = self.ring.clear_all();",
        "            DeviceAuth401Kind::DeviceRevoked => {\n                self.apply_retry_outcome(op, Outcome::Unreachable, None, now_ms);\n                let _ = self.ring.clear_all();",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "cpp_parity_scanner_controller",
            "test_device_revoked_flush_and_heartbeat_401",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "finish_pairing_success skips RetryState reset",
        CRATE / "src/scanner_controller.rs",
        "        self.device_revoked = false;\n        self.retry_state = RetryState::default();\n        self.state.operational = OperationalState::Paired;",
        "        self.device_revoked = false;\n        self.state.operational = OperationalState::Paired;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "cpp_parity_scanner_controller",
            "test_pairing_success_resets_outage_backoff",
            "--",
            "--exact",
        ],
        "one",
    ),
    # S-c — OtaController
    Mutant(
        "S-c",
        "OTA 429 marks failed",
        CRATE / "src/ota_controller.rs",
        "            self.abort_download_to_waiting_after_429(ra);\n            return false;",
        "            self.fail_post_offer(DeviceOtaError::HttpStatus);\n            return false;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "ota_first_get_429_waits_retry_after_and_restarts_from_zero",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "OTA RA wait not clamped to 60s floor",
        CRATE / "src/ota_controller.rs",
        "        Some(ms) => ms.clamp(OTA_429_WAIT_MIN_MS, OTA_429_WAIT_MAX_MS),",
        "        Some(ms) => ms.min(OTA_429_WAIT_MAX_MS),",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "ota_429_retry_after_zero_waits_sixty_seconds",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "OTA invalid RA does not default 60s",
        CRATE / "src/ota_controller.rs",
        "        None => OTA_429_WAIT_MIN_MS,",
        "        None => 0,",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "ota_429_invalid_retry_after_waits_sixty_seconds",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "OTA 429 does not abort write (no abort_inactive_write)",
        CRATE / "src/ota_controller.rs",
        "        self.http_close();\n        self.slots.abort_inactive_write();\n        self.hasher = None;",
        "        self.http_close();\n        self.hasher = None;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "ota_resume_get_429_clears_partial_write",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "GET budget 10→11",
        CRATE / "src/ota_controller.rs",
        "const OTA_GET_BUDGET_MAX_STARTS: usize = 10;",
        "const OTA_GET_BUDGET_MAX_STARTS: usize = 11;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "ota_get_budget_caps_at_ten_starts_per_hour",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "GET budget rolling window disabled",
        CRATE / "src/ota_controller.rs",
        "        self.get_start_ms_log.retain(|&t| t > cutoff);",
        "        let _ = cutoff;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "ota_get_budget_window_prunes_after_sixty_minutes",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "OTA calls shared on_outcome on 429",
        CRATE / "src/ota_controller.rs",
        "        self.phase = Phase::Waiting;\n        self.display.clear_ota_overlay();",
        "        self.phase = Phase::Waiting;\n        let _ = crate::retry_policy::on_outcome(\n            crate::retry_policy::RetryState::default(),\n            crate::retry_policy::Op::Flush,\n            crate::retry_policy::Outcome::RateLimited,\n            None,\n            self.clock.now_ms(),\n            &mut || 1000,\n        );\n        self.display.clear_ota_overlay();",
        [
            "cargo",
            "test",
            "--locked",
            "ota_first_get_429_waits_retry_after_and_restarts_from_zero",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "416 resume rule bypassed",
        CRATE / "src/ota_controller.rs",
        "            if open.http_status == 416 {",
        "            if false && open.http_status == 416 {",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_host",
            "416",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "3-retry limit raised",
        CRATE / "src/ota_controller.rs",
        "pub const MAX_RANGE_RETRIES: u32 = 3;",
        "pub const MAX_RANGE_RETRIES: u32 = 4;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_locks",
            "max_range_retries_constant_is_three",
            "--",
            "--exact",
        ],
        "one",
    ),
    Mutant(
        "S-c",
        "60m download_failed backoff halved",
        CRATE / "src/ota_controller.rs",
        "pub const DOWNLOAD_FAILED_BACKOFF_MS: u64 = 60 * 60 * 1000;",
        "pub const DOWNLOAD_FAILED_BACKOFF_MS: u64 = 30 * 60 * 1000;",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "ota_controller_locks",
            "backoff",
        ],
        "one",
    ),
    Mutant(
        "S-b",
        "pairing path applies flush backoff delay",
        CRATE / "src/scanner_controller.rs",
        "        let res = self.client.complete_pairing(",
        "        self.apply_retry_outcome(Op::Flush, Outcome::RateLimited, Some(60_000), now_ms);\n        let res = self.client.complete_pairing(",
        [
            "cargo",
            "test",
            "--locked",
            "--test",
            "cpp_parity_scanner_controller",
            "test_pairing_rate_limited",
        ],
        "one",
    ),
]


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)


def cargo_test(cmd: list[str]) -> subprocess.CompletedProcess:
    return run(cmd, CRATE)


def main() -> int:
    out_dir = ROOT / ".cursor" / "pr723-mutants"
    out_dir.mkdir(parents=True, exist_ok=True)
    results = []
    for m in MUTANTS:
        text = m.path.read_text()
        if m.old not in text:
            results.append(
                {
                    "slice": m.slice,
                    "name": m.name,
                    "error": "old substring not found",
                    "kill": "?",
                }
            )
            continue
        text = text.replace(m.old, m.new, 1)
        m.path.write_text(text)
        rel = m.path.relative_to(ROOT)
        diff = run(["git", "diff", "--", str(rel)], ROOT)
        patch_path = out_dir / f"{m.slice}_{m.name.replace(' ', '_')[:40]}.patch"
        patch_path.write_text(diff.stdout)
        subprocess.run(["git", "checkout", "--", str(rel)], cwd=ROOT, check=True)
        check = run(["git", "apply", "--check", str(patch_path)], ROOT)
        if check.returncode != 0:
            results.append(
                {
                    "slice": m.slice,
                    "name": m.name,
                    "apply_check": "fail",
                    "kill": "?",
                    "patch": str(patch_path),
                }
            )
            continue
        subprocess.run(["git", "apply", str(patch_path)], cwd=ROOT, check=True)
        chk = run(["cargo", "check", "--locked", "-p", "stockbot-scanner-core"], CRATE.parent.parent)
        if chk.returncode != 0:
            subprocess.run(["git", "checkout", "--", str(rel)], cwd=ROOT, check=True)
            results.append(
                {
                    "slice": m.slice,
                    "name": m.name,
                    "kill": "?",
                    "compile": "fail",
                    "cmd": " ".join(m.cmd),
                    "patch": patch_path.read_text(),
                }
            )
            continue
        test = cargo_test(m.cmd)
        killed = test.returncode != 0
        subprocess.run(["git", "checkout", "--", str(rel)], cwd=ROOT, check=True)
        results.append(
            {
                "slice": m.slice,
                "name": m.name,
                "kill": "Y" if killed else "N",
                "cmd": " ".join(m.cmd),
                "patch": patch_path.read_text(),
            }
        )
    (out_dir / "results.json").write_text(json.dumps(results, indent=2))
    print(json.dumps(results, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())