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
    let sdkconfig =
        fs::read_to_string(esp_root.join("sdkconfig.defaults")).expect("sdkconfig.defaults");
    assert_eq!(
        sdkconfig_value(&sdkconfig, "CONFIG_PARTITION_TABLE_CUSTOM_FILENAME").as_deref(),
        Some(format!("\"{PARTITION_CSV}\"").as_str()),
        "sdkconfig must name the committed partition CSV filename"
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
        "partition copy base must be the ESP crate root"
    );
    assert!(
        esp_root.join(PARTITION_CSV).is_file(),
        "committed copy must exist at crate root for glob copy"
    );
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

const BENCH_MOD_DECLARATION: &str = "#[cfg(feature = \"bench-serial\")]\nmod bench;";

const TARGET_FEATURES: [&str; 4] = [
    "sticks3_u173",
    "sticks3_u173_uart",
    "atoms3lite_u173_bench",
    "atoms3lite_symcode_hid",
];

fn esp_sources() -> Vec<(PathBuf, String)> {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let mut files = vec![esp_root.join("Cargo.toml")];
    for entry in fs::read_dir(esp_root.join("src")).expect("esp src dir") {
        files.push(entry.unwrap().path());
    }
    files
        .into_iter()
        .map(|file| {
            let body = fs::read_to_string(&file)
                .unwrap_or_else(|e| panic!("read {}: {e}", file.display()));
            (file, body)
        })
        .collect()
}

/// S8 (#740) wires the controller to real clock, NVS, Wi-Fi and HTTPS. These locks keep the
/// image from weakening TLS, using `unsafe`, or marking itself valid before OTA (#746) exists,
/// and keep the serial stand-ins behind the `bench-serial` feature that every target turns on.
#[test]
fn esp_crate_s8_wiring_locks() {
    const FORBIDDEN: [&str; 9] = [
        "skip_cert_common_name_check",
        "use_global_ca_store: true",
        "set_insecure",
        "mark_running_slot_valid",
        "mark_app_valid",
        "mark_valid",
        "cancel_rollback",
        "EspOta",
        "esp_ota_",
    ];
    for (file, body) in esp_sources() {
        for token in FORBIDDEN {
            assert!(
                !body.contains(token),
                "{} contains {token:?}: not allowed in the ESP crate",
                file.display()
            );
        }
        if file.extension().is_some_and(|e| e == "rs") {
            for (n, line) in body.lines().enumerate() {
                let code = line.split("//").next().unwrap_or("");
                let words: Vec<&str> = code
                    .split(|c: char| !(c.is_alphanumeric() || c == '_'))
                    .collect();
                assert!(
                    !words.contains(&"unsafe"),
                    "{}:{} uses unsafe: {line}",
                    file.display(),
                    n + 1
                );
            }
        }
    }
}

#[test]
fn esp_crate_keeps_serial_stand_ins_behind_bench_serial() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    assert!(
        main_rs.contains(BENCH_MOD_DECLARATION),
        "main.rs must declare the stand-ins exactly as {BENCH_MOD_DECLARATION:?}"
    );
    assert!(
        esp_root.join("src/bench.rs").is_file(),
        "src/bench.rs must exist"
    );
    let manifest = fs::read_to_string(esp_root.join("Cargo.toml")).expect("Cargo.toml");
    let features = manifest
        .split("[features]")
        .nth(1)
        .and_then(|rest| rest.split("\n[").next())
        .expect("[features] table");
    let feature_line = |name: &str| {
        features
            .lines()
            .map(str::trim)
            .find(|l| l.split_once('=').is_some_and(|(k, _)| k.trim() == name))
            .unwrap_or_else(|| panic!("feature {name} missing"))
            .to_owned()
    };
    assert!(feature_line("bench-serial")
        .replace(' ', "")
        .ends_with("=[]"));
    for target in TARGET_FEATURES {
        assert!(
            feature_line(target).contains("\"bench-serial\""),
            "target feature {target} must turn on bench-serial (CI builds only target features)"
        );
    }
}

/// #741: the StickS3 power driver sits behind the StickS3 features only, and deep sleep (which
/// needs the unsafe ESP-IDF sleep sys calls) is not built in this crate.
#[test]
fn esp_crate_power_driver_is_sticks3_only_and_has_no_deep_sleep() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let gate = "#[cfg(any(feature = \"sticks3_u173\", feature = \"sticks3_u173_uart\"))]";
    for module in ["i2c_bus", "power_sticks3", "display_sticks3"] {
        assert!(
            main_rs.contains(&format!("{gate}\nmod {module};")),
            "main.rs must declare mod {module} behind the StickS3 features"
        );
    }
    let power = fs::read_to_string(esp_root.join("src/power_sticks3.rs")).expect("power_sticks3");
    assert!(
        !power.contains("esp_sleep"),
        "power_sticks3.rs must not call the esp_sleep_* sys calls (unsafe, banned in this crate)"
    );
    assert!(
        power.contains("deep sleep not available yet"),
        "deep sleep stays a logged no-op until the follow-up decision"
    );
}

/// #742: the StickS3 screen driver sits behind the StickS3 features, the AtomS3 targets keep the
/// log stand-in, and the panel is not started until the power chip has switched the LCD on.
#[test]
fn esp_crate_display_driver_is_sticks3_only_and_starts_lazily() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let sticks3 = "#[cfg(any(feature = \"sticks3_u173\", feature = \"sticks3_u173_uart\"))]";
    let not_sticks3 =
        "#[cfg(not(any(feature = \"sticks3_u173\", feature = \"sticks3_u173_uart\")))]";
    assert!(
        main_rs.contains(&format!(
            "{sticks3}\n    let mut display = display_sticks3::Sticks3Display::new("
        )),
        "main.rs must build the StickS3 screen behind the StickS3 features"
    );
    assert!(
        main_rs.contains(&format!(
            "{not_sticks3}\n    let mut display = bench::LogDisplay::default();"
        )),
        "the AtomS3 targets keep the log display"
    );
    let display =
        fs::read_to_string(esp_root.join("src/display_sticks3.rs")).expect("display_sticks3");
    let begin = display
        .split("fn begin(&mut self) {")
        .nth(1)
        .and_then(|rest| rest.split("fn render").next())
        .expect("DisplayHal::begin body");
    assert!(
        !begin.contains("Hardware::start") && !begin.contains("hardware()"),
        "display begin() runs before power begin(); the panel must start on first render"
    );
    let manifest = fs::read_to_string(esp_root.join("Cargo.toml")).expect("Cargo.toml");
    assert!(
        manifest.contains("mipidsi = { version = \"=0.9.0\" }"),
        "the panel driver crate must stay pinned exactly"
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
