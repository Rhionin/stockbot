mod common;

#[path = "common/scenario_runner.rs"]
mod scenario_runner;

use std::fs;
use std::path::PathBuf;
use std::process::Command;

use scenario_runner::{
    golden_trace_from_file, load_and_run_scenario, trace_matches_golden, SCENARIO_NAMES,
};

/// Shared C++ goldens stay on PR #713 timing; Rust policy differs on heartbeat backoff.
const RUST_ONLY_SCENARIO_GOLDENS: &[&str] = &[
    "device_revoked_scan_401",
    "flush_401_heartbeat_cadence_400s",
    "scan_503_busy_json_buffered_as_5xx_retry",
];

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

fn build_cpp_scenario_runner() -> PathBuf {
    let firmware = repo_root().join("firmware");
    let out = PathBuf::from("/tmp/stockbot_run_scenario_traces_rusttest");
    let script = format!(
        r#"
set -euo pipefail
cd {:?}
mapfile -t CORE_SRC < <(find src/core -name '*.cpp' | sort)
"${{CXX:-g++}}" -std=c++17 -Wall -Wextra -Werror \
  -Isrc/core -Isrc/net -Isrc/hal -Itest \
  "${{CORE_SRC[@]}}" test/run_scenario_traces.cpp -o {:?}
"#,
        firmware, out
    );
    let out_status = Command::new("bash")
        .arg("-lc")
        .arg(&script)
        .status()
        .expect("build scenario runner");
    assert!(out_status.success(), "scenario runner build failed");
    out
}

#[test]
fn scenario_traces_match_committed_goldens() {
    let scenarios_dir = repo_root().join("firmware/test/scenarios");
    let goldens_dir = repo_root().join("testdata/scenarios");

    let rust_only = repo_root().join("testdata/scenarios/rust_only");
    for name in SCENARIO_NAMES {
        let scenario_path = scenarios_dir.join(format!("{name}.json"));
        let golden_path = if RUST_ONLY_SCENARIO_GOLDENS.contains(&name) {
            rust_only.join(format!("{name}.trace.json"))
        } else {
            goldens_dir.join(format!("{name}.trace.json"))
        };
        let got = load_and_run_scenario(&scenario_path, name);
        let golden = golden_trace_from_file(&golden_path);
        trace_matches_golden(&got, &golden).unwrap_or_else(|e| panic!("scenario {name}: {e}"));
    }
}

#[test]
#[ignore = "optional C++ runner parity check"]
fn scenario_cpp_runner_matches_goldens() {
    let runner = build_cpp_scenario_runner();
    let scenarios_dir = repo_root().join("firmware/test/scenarios");
    let goldens_dir = repo_root().join("testdata/scenarios");
    let status = Command::new(&runner)
        .arg("--scenarios-dir")
        .arg(&scenarios_dir)
        .arg("--goldens-dir")
        .arg(&goldens_dir)
        .status()
        .expect("run cpp scenario runner");
    assert!(status.success(), "C++ scenario runner golden mismatch");
}

fn parse_cpp_scenario_names(cpp_source: &str) -> Vec<String> {
    let marker = "const char* kNames[] = {";
    let start = cpp_source
        .find(marker)
        .expect("kNames[] not found in run_scenario_traces.cpp");
    let rest = &cpp_source[start + marker.len()..];
    let end = rest
        .find("};")
        .expect("kNames[] terminator not found in run_scenario_traces.cpp");
    rest[..end]
        .split('"')
        .skip(1)
        .step_by(2)
        .map(str::to_owned)
        .collect()
}

#[test]
fn scenario_directory_matches_hardcoded_runner_lists() {
    let cpp_path = repo_root().join("firmware/test/run_scenario_traces.cpp");
    let cpp_source = fs::read_to_string(&cpp_path)
        .unwrap_or_else(|e| panic!("read {}: {e}", cpp_path.display()));
    let cpp_names = parse_cpp_scenario_names(&cpp_source);
    assert_eq!(
        SCENARIO_NAMES,
        cpp_names.as_slice(),
        "Rust SCENARIO_NAMES must match run_scenario_traces.cpp kNames"
    );
    let scenarios_dir = repo_root().join("firmware/test/scenarios");
    let mut on_disk: Vec<String> = fs::read_dir(&scenarios_dir)
        .unwrap_or_else(|e| panic!("read {}: {e}", scenarios_dir.display()))
        .filter_map(|e| e.ok())
        .filter(|e| e.path().extension().is_some_and(|x| x == "json"))
        .map(|e| e.path().file_stem().unwrap().to_string_lossy().into_owned())
        .collect();
    on_disk.sort();
    let mut expected: Vec<String> = SCENARIO_NAMES.iter().map(|s| (*s).to_owned()).collect();
    expected.sort();
    assert_eq!(
        on_disk, expected,
        "firmware/test/scenarios must match hard-coded scenario lists (same set)"
    );
}

#[test]
fn scenario_golden_files_exist_for_each_scenario_script() {
    let rust_only = repo_root().join("testdata/scenarios/rust_only");
    for name in SCENARIO_NAMES {
        let scenario = repo_root()
            .join("firmware/test/scenarios")
            .join(format!("{name}.json"));
        let golden = if RUST_ONLY_SCENARIO_GOLDENS.contains(name) {
            rust_only.join(format!("{name}.trace.json"))
        } else {
            repo_root()
                .join("testdata/scenarios")
                .join(format!("{name}.trace.json"))
        };
        assert!(
            scenario.is_file(),
            "missing scenario script {}",
            scenario.display()
        );
        assert!(
            golden.is_file(),
            "missing golden trace {}",
            golden.display()
        );
        let raw = fs::read_to_string(&golden).expect("read golden");
        assert!(raw.contains("\"steps\""));
    }
}

#[test]
#[ignore = "run once to refresh testdata/scenarios/rust_only/*.trace.json"]
fn emit_rust_only_scenario_goldens() {
    let scenarios_dir = repo_root().join("firmware/test/scenarios");
    let out_dir = repo_root().join("testdata/scenarios/rust_only");
    fs::create_dir_all(&out_dir).expect("mkdir rust_only goldens");
    for name in RUST_ONLY_SCENARIO_GOLDENS {
        let scenario_path = scenarios_dir.join(format!("{name}.json"));
        let got = load_and_run_scenario(&scenario_path, name);
        let pretty = serde_json::to_string_pretty(&got).expect("serialize trace");
        let path = out_dir.join(format!("{name}.trace.json"));
        fs::write(&path, format!("{pretty}\n")).expect("write rust-only golden");
    }
}
