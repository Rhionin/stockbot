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
fn partition_csv_reaches_esp_idf_out_via_glob() {
    const PARTITION_CSV: &str = "partitions_8mb_ota.csv";
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let sdkconfig =
        fs::read_to_string(esp_root.join("sdkconfig.defaults")).expect("sdkconfig.defaults");
    assert_eq!(
        sdkconfig_value(&sdkconfig, "CONFIG_PARTITION_TABLE_CUSTOM_FILENAME").as_deref(),
        Some(format!("\"{PARTITION_CSV}\"").as_str()),
        "sdkconfig must name the partition CSV filename"
    );
    for (key, want) in [
        ("CONFIG_PARTITION_TABLE_CUSTOM", "y"),
        ("CONFIG_PARTITION_TABLE_OFFSET", "0x8000"),
        ("CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE", "y"),
        ("CONFIG_ESPTOOLPY_FLASHSIZE_8MB", "y"),
        ("CONFIG_ESPTOOLPY_FLASHSIZE", "\"8MB\""),
    ] {
        assert_eq!(
            sdkconfig_value(&sdkconfig, key).as_deref(),
            Some(want),
            "{key} must be exactly {want} (last assignment wins, comments ignored)"
        );
    }
    let cargo_config =
        fs::read_to_string(esp_root.join(".cargo/config.toml")).expect(".cargo/config.toml");
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
        "partition glob base must be relative to the ESP crate root"
    );
    let base_rel = glob_base_value(&cargo_config);
    let base = esp_root.join(&base_rel);
    let globbed = base.join(PARTITION_CSV);
    assert!(
        globbed.is_file(),
        "glob base {base_rel:?} must contain {PARTITION_CSV} (looked at {})",
        globbed.display()
    );
    let canonical = repo_root().join("firmware").join(PARTITION_CSV);
    assert_eq!(
        globbed.canonicalize().expect("canonicalize globbed csv"),
        canonical.canonicalize().expect("canonicalize firmware csv"),
        "glob base must resolve to the single committed firmware/{PARTITION_CSV}"
    );
}

/// The `value = "..."` of `ESP_IDF_GLOB_PARTITION_BASE` in `.cargo/config.toml`.
fn glob_base_value(cargo_config: &str) -> String {
    let line = cargo_config
        .lines()
        .find(|l| l.trim_start().starts_with("ESP_IDF_GLOB_PARTITION_BASE"))
        .expect("ESP_IDF_GLOB_PARTITION_BASE line");
    let rest = line
        .split_once("value")
        .expect("value key in ESP_IDF_GLOB_PARTITION_BASE")
        .1;
    let rest = &rest[rest.find('"').expect("opening quote") + 1..];
    rest[..rest.find('"').expect("closing quote")].to_owned()
}

/// Last effective `KEY=value` in a Kconfig defaults file (comments and blank lines ignored).
fn sdkconfig_value(body: &str, key: &str) -> Option<String> {
    body.lines()
        .map(str::trim)
        .filter(|l| !l.starts_with('#'))
        .filter_map(|l| l.split_once('='))
        .filter(|(k, _)| *k == key)
        .map(|(_, v)| v.trim().to_string())
        .last()
}

#[derive(Debug, PartialEq, Eq)]
struct Part {
    name: String,
    ty: String,
    sub: String,
    off: u64,
    size: u64,
}

fn parse_csv_partitions(csv: &str) -> Vec<Part> {
    let num = |s: &str| u64::from_str_radix(s.trim_start_matches("0x"), 16).expect("hex number");
    csv.lines()
        .map(str::trim)
        .filter(|l| !l.is_empty() && !l.starts_with('#'))
        .map(|l| {
            let f: Vec<&str> = l.split(',').map(str::trim).collect();
            assert!(f.len() >= 5, "partition row needs 5 fields: {l:?}");
            Part {
                name: f[0].into(),
                ty: f[1].into(),
                sub: f[2].into(),
                off: num(f[3]),
                size: num(f[4]),
            }
        })
        .collect()
}

/// firmware-ota.md section 3 freezes this table: two 3 MB OTA slots, `otadata`, rollback needs both.
#[test]
fn partition_layout_is_the_frozen_ota_table() {
    let csv = fs::read_to_string(repo_root().join("firmware/partitions_8mb_ota.csv")).unwrap();
    let got = parse_csv_partitions(&csv);
    let p = |name: &str, ty: &str, sub: &str, off: u64, size: u64| Part {
        name: name.into(),
        ty: ty.into(),
        sub: sub.into(),
        off,
        size,
    };
    let want = vec![
        p("nvs", "data", "nvs", 0x9000, 0x6000),
        p("otadata", "data", "ota", 0xf000, 0x2000),
        p("app0", "app", "ota_0", 0x20000, 0x300000),
        p("app1", "app", "ota_1", 0x320000, 0x300000),
        p("littlefs", "data", "spiffs", 0x620000, 0x100000),
    ];
    assert_eq!(
        got, want,
        "partition table is frozen (firmware-ota.md section 3)"
    );

    // Structural invariants, independent of the literal table above.
    let flash_end = 8 * 1024 * 1024;
    for (i, a) in got.iter().enumerate() {
        assert!(a.off >= 0x9000, "{} overlaps the table at 0x8000", a.name);
        assert!(a.off + a.size <= flash_end, "{} runs past 8 MB", a.name);
        if a.ty == "app" {
            assert_eq!(a.off % 0x10000, 0, "{} must be 64 KB aligned", a.name);
        }
        for b in &got[i + 1..] {
            assert!(
                a.off + a.size <= b.off || b.off + b.size <= a.off,
                "{} overlaps {}",
                a.name,
                b.name
            );
        }
    }

    // The CI size gate must fit the slot it protects.
    let script = fs::read_to_string(repo_root().join("scripts/firmware_rust_esp_ci.sh")).unwrap();
    let gate: u64 = script
        .lines()
        .find_map(|l| l.strip_prefix("MAX_APP_BYTES="))
        .expect("MAX_APP_BYTES in firmware_rust_esp_ci.sh")
        .parse()
        .expect("decimal gate");
    assert_eq!(gate, 2_516_582, "2.4 MB gate (firmware-ota.md section 3)");
    for slot in got.iter().filter(|x| x.ty == "app") {
        assert!(
            gate <= slot.size,
            "size gate {gate} exceeds slot {}",
            slot.name
        );
    }
}

/// S7 is boot + log only (firmware-ota.md section 7): the image must not mark itself valid at
/// startup, and OTA, network, auth and retry logic belong to later slices (retry_policy.rs is
/// reused there, never re-implemented or called from the boot crate).
#[test]
fn esp_boot_crate_stays_boot_and_log_only() {
    const FORBIDDEN: [&str; 12] = [
        "mark_app_valid",
        "mark_running_slot",
        "cancel_rollback",
        "mark_valid",
        "EspOta",
        "esp_ota_",
        "retry_policy",
        "stockbot_scanner_core",
        "EspHttpConnection",
        "EspWifi",
        "EspNvs",
        "EspDefaultNvsPartition",
    ];
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let mut files = vec![esp_root.join("Cargo.toml")];
    for entry in fs::read_dir(esp_root.join("src")).expect("esp src dir") {
        files.push(entry.unwrap().path());
    }
    for file in files {
        let body = fs::read_to_string(&file).unwrap();
        for token in FORBIDDEN {
            assert!(
                !body.contains(token),
                "{} contains {token:?}: not allowed in the S7 boot + log crate",
                file.display()
            );
        }
    }
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
