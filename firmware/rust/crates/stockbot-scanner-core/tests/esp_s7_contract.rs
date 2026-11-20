//! S7 ESP crate contract checks runnable on the host workspace (issue #445 / #714).

use std::fs;
use std::path::{Path, PathBuf};

use stockbot_scanner_core::fw_target::{self, TARGET_IDS};

fn repo_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .ancestors()
        .nth(4)
        .expect("firmware/rust/crates/stockbot-scanner-core → repo root")
        .to_path_buf()
}

fn rust_tree() -> PathBuf {
    repo_root().join("firmware/rust")
}

const ALLOWED_PIN_KEYS: [&str; 6] = [
    "ESPUP_VERSION",
    "ESP_RUST_VERSION",
    "ESP_IDF_VERSION",
    "ESPFLASH_VERSION",
    "LDPROXY_VERSION",
    "ESP_HOST_RUST_VERSION",
];

#[test]
fn esp_toolchain_env_has_exactly_six_valid_keys() {
    let pin_file = rust_tree().join("esp-toolchain.env");
    let body = fs::read_to_string(&pin_file)
        .unwrap_or_else(|e| panic!("read {}: {e}", pin_file.display()));
    let mut seen = std::collections::HashMap::new();
    for line in body.lines() {
        let line = line.split('#').next().unwrap_or("").trim();
        if line.is_empty() {
            continue;
        }
        let Some((key, value)) = line.split_once('=') else {
            panic!("invalid pin line: {line:?}");
        };
        assert!(
            !value.contains(' '),
            "pin values must be unquoted tokens without spaces: {line}"
        );
        assert!(
            ALLOWED_PIN_KEYS.contains(&key),
            "unknown or duplicate-class pin key {key:?}"
        );
        assert!(seen.insert(key, value).is_none(), "duplicate pin key {key}");
        assert!(!value.is_empty(), "empty value for {key}");
        match key {
            "ESPUP_VERSION" | "ESPFLASH_VERSION" | "LDPROXY_VERSION" | "ESP_HOST_RUST_VERSION" => {
                assert!(
                    value.chars().all(|c| c.is_ascii_digit() || c == '.'),
                    "invalid semver pin {key}={value}"
                );
            }
            "ESP_IDF_VERSION" => {
                assert!(value.starts_with('v'), "ESP_IDF_VERSION must start with v");
            }
            "ESP_RUST_VERSION" => {
                let parts: Vec<_> = value.split('.').collect();
                assert_eq!(parts.len(), 4, "ESP_RUST_VERSION must be a.b.c.d");
            }
            _ => {}
        }
    }
    for key in ALLOWED_PIN_KEYS {
        assert!(seen.contains_key(key), "missing pin {key}");
    }
    assert_eq!(seen.len(), 6, "expected exactly six pin keys");
}

#[test]
fn partition_csv_copy_matches_canonical() {
    let canonical = repo_root().join("firmware/partitions_8mb_ota.csv");
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let copy = esp_root.join("partitions_8mb_ota.csv");
    let a = fs::read(&canonical).expect("canonical partitions csv");
    let b = fs::read(&copy).expect("esp crate partition copy");
    assert_eq!(
        a, b,
        "esp crate partition copy must be byte-identical to firmware/partitions_8mb_ota.csv"
    );
}

#[test]
fn partition_csv_reaches_esp_idf_out_via_glob() {
    const PARTITION_CSV: &str = "partitions_8mb_ota.csv";
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let sdkconfig = fs::read_to_string(esp_root.join("sdkconfig.defaults"))
        .expect("sdkconfig.defaults");
    assert!(
        sdkconfig.contains(&format!(
            "CONFIG_PARTITION_TABLE_CUSTOM_FILENAME=\"{PARTITION_CSV}\""
        )),
        "sdkconfig must name the committed partition CSV filename"
    );
    assert!(
        sdkconfig.contains("CONFIG_PARTITION_TABLE_CUSTOM=y"),
        "custom partition table must be enabled"
    );
    assert!(
        sdkconfig.contains("CONFIG_PARTITION_TABLE_OFFSET=0x8000"),
        "partition table offset must match OTA contract"
    );
    assert!(
        sdkconfig.contains("CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y"),
        "bootloader rollback must be enabled"
    );
    let cargo_config = fs::read_to_string(esp_root.join(".cargo/config.toml"))
        .expect(".cargo/config.toml");
    assert!(
        cargo_config.contains("ESP_IDF_GLOB_PARTITION_BASE"),
        "ESP_IDF_GLOB must copy partition assets into esp-idf-sys out/"
    );
    assert!(
        cargo_config.contains(&format!("ESP_IDF_GLOB_PARTITION_CSV = \"{PARTITION_CSV}\"")),
        "glob mask must select the committed partition CSV"
    );
    assert!(
        cargo_config.contains("relative = true"),
        "partition copy base must be the ESP crate root"
    );
    assert!(
        esp_root.join(PARTITION_CSV).is_file(),
        "committed copy must exist at crate root for glob copy"
    );
}

#[test]
fn no_stray_esp_idf_manifests_outside_scanner_esp() {
    let esp_crate = rust_tree().join("crates/stockbot-scanner-esp/Cargo.toml");
    let mut manifests = Vec::new();
    for entry in walkdir_light(&rust_tree(), "Cargo.toml") {
        manifests.push(entry);
    }
    for manifest in manifests {
        if manifest == esp_crate {
            continue;
        }
        let body = fs::read_to_string(&manifest).unwrap();
        assert!(
            !body.contains("esp-idf-svc")
                && !body.contains("esp-idf-hal")
                && !body.contains("esp-idf-sys"),
            "stray esp-idf dependency in {}",
            manifest.display()
        );
    }
}

fn walkdir_light(root: &Path, file_name: &str) -> Vec<PathBuf> {
    let mut out = Vec::new();
    let mut stack = vec![root.to_path_buf()];
    while let Some(dir) = stack.pop() {
        let entries =
            fs::read_dir(&dir).unwrap_or_else(|e| panic!("read_dir {}: {e}", dir.display()));
        for entry in entries {
            let entry = entry.unwrap();
            let path = entry.path();
            if path.is_dir() {
                if path.file_name().is_some_and(|n| n == "target") {
                    continue;
                }
                stack.push(path);
            } else if path.file_name().is_some_and(|n| n == file_name) {
                out.push(path);
            }
        }
    }
    out
}

#[test]
fn fw_target_markers_match_ci_matrix_ids() {
    for target in TARGET_IDS {
        let marker = fw_target::marker_with_nul(target).expect("known target");
        let text = fw_target::marker_text(target).unwrap();
        assert_eq!(&marker[..marker.len() - 1], text.as_bytes());
        assert_eq!(marker.last(), Some(&0));
        let rendered = String::from_utf8_lossy(&marker);
        assert!(rendered.starts_with("stockbot-fw-target:"));
        assert!(
            marker.len() <= 64,
            "marker length {len} should stay small for rodata embed",
            len = marker.len()
        );
    }
}
