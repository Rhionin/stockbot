mod common;

#[path = "common/scenario_runner.rs"]
mod scenario_runner;

use std::fs;
use std::path::PathBuf;

use scenario_runner::{
    golden_trace_from_file, load_and_run_scenario, trace_matches_golden, SCENARIO_NAMES,
};

/// Goldens for these scenarios live in `testdata/scenarios/rust_only/`. This one was
/// split out of the shared directory for history: it follows the PR #713 heartbeat
/// backoff timing, which differs from the other scenarios' goldens.
const RUST_ONLY_SCENARIO_GOLDENS: &[&str] = &["flush_401_heartbeat_cadence_400s"];

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

#[test]
fn scenario_traces_match_committed_goldens() {
    let scenarios_dir = repo_root().join("firmware/test/scenarios");
    let goldens_dir = repo_root().join("testdata/scenarios");

    let rust_only = repo_root().join("testdata/scenarios/rust_only");
    for name in SCENARIO_NAMES {
        let scenario_path = scenarios_dir.join(format!("{name}.json"));
        let golden_path = if RUST_ONLY_SCENARIO_GOLDENS.contains(name) {
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
fn scenario_directory_matches_scenario_names() {
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
        "firmware/test/scenarios/*.json file stems must match SCENARIO_NAMES (same set)"
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
