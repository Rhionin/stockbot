mod common;

#[path = "common/scenario_runner.rs"]
mod scenario_runner;

use std::fs;
use std::path::PathBuf;
use std::process::Command;

use scenario_runner::{
    golden_trace_from_file, load_and_run_scenario, trace_matches_golden, SCENARIO_NAMES,
};

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

    for name in SCENARIO_NAMES {
        let scenario_path = scenarios_dir.join(format!("{name}.json"));
        let golden_path = goldens_dir.join(format!("{name}.trace.json"));
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

#[test]
fn scenario_golden_files_exist_for_each_scenario_script() {
    for name in SCENARIO_NAMES {
        let scenario = repo_root()
            .join("firmware/test/scenarios")
            .join(format!("{name}.json"));
        let golden = repo_root()
            .join("testdata/scenarios")
            .join(format!("{name}.trace.json"));
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
