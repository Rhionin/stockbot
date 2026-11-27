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
    for module in ["power_sticks3", "display_sticks3", "buttons_sticks3"] {
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

/// #742: the StickS3 screen driver sits behind the StickS3 features, the Symcode AtomS3 target
/// keeps the log stand-in, and the panel is not started until the power chip has switched the LCD
/// on.
#[test]
fn esp_crate_display_driver_is_sticks3_only_and_starts_lazily() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let sticks3 = "#[cfg(any(feature = \"sticks3_u173\", feature = \"sticks3_u173_uart\"))]";
    let no_screen = "#[cfg(not(any(\n        feature = \"sticks3_u173\",\n        feature = \"sticks3_u173_uart\",\n        feature = \"atoms3lite_u173_bench\"\n    )))]";
    assert!(
        main_rs.contains(&format!(
            "{sticks3}\n    let mut display = display_sticks3::Sticks3Display::new("
        )),
        "main.rs must build the StickS3 screen behind the StickS3 features"
    );
    assert!(
        main_rs.contains(&format!(
            "{no_screen}\n    let mut display = bench::LogDisplay::default();"
        )),
        "only the Symcode AtomS3 target keeps the log display"
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

/// #743, #747: the U173 I2C reader is built for `sticks3_u173` and `atoms3lite_u173_bench` only
/// (not the StickS3 UART variant or the Symcode AtomS3 target, which keep the serial stand-in).
#[test]
fn esp_crate_u173_reader_is_built_for_the_i2c_u173_targets_only() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    assert!(
        main_rs.contains(
            "#[cfg(any(feature = \"sticks3_u173\", feature = \"atoms3lite_u173_bench\"))]\nmod scanner_u173;"
        ),
        "main.rs must declare mod scanner_u173 behind the two I2C U173 targets"
    );
    assert!(
        main_rs.contains(
            "#[cfg(feature = \"sticks3_u173\")]\n    let mut scanner = scanner_u173::ScannerU173::new("
        ),
        "main.rs must build the U173 reader behind sticks3_u173"
    );
    assert!(
        main_rs.contains(
            "#[cfg(not(any(\n        feature = \"sticks3_u173\",\n        feature = \"atoms3lite_u173_bench\",\n        feature = \"atoms3lite_symcode_hid\"\n    )))]\n    let mut scanner = serial_scanner;"
        ),
        "every other target keeps the serial stand-in"
    );
    for pin in [
        "peripherals.i2c0",
        "peripherals.pins.gpio9",
        "peripherals.pins.gpio10",
    ] {
        assert!(main_rs.contains(pin), "the Grove bus needs {pin}");
    }
    let scanner =
        fs::read_to_string(esp_root.join("src/scanner_u173.rs")).expect("scanner_u173.rs");
    assert!(
        scanner.contains("SerialScanner"),
        "the serial stand-in stays alongside the reader for bench input"
    );
}

/// Only the ESP crate and the USB host driver it depends on (#748) may name ESP-IDF crates.
#[test]
fn no_stray_esp_idf_manifests_outside_scanner_esp() {
    let allowed = [
        rust_tree().join("crates/stockbot-scanner-esp/Cargo.toml"),
        rust_tree().join(USB_HOST_CRATE).join("Cargo.toml"),
    ];
    let mut manifests = Vec::new();
    for entry in walkdir_light(&rust_tree(), "Cargo.toml") {
        manifests.push(entry);
    }
    for manifest in manifests {
        if allowed.contains(&manifest) {
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

/// #744 part 1: the StickS3 buttons are read in the main loop through the core debouncer.
#[test]
fn esp_crate_buttons_are_sticks3_only_and_use_the_core_debouncer() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let sticks3 = "#[cfg(any(feature = \"sticks3_u173\", feature = \"sticks3_u173_uart\"))]";
    for needle in [
        "buttons_sticks3::Sticks3Buttons::new(buttons_sticks3::ButtonPeripherals {",
        "let mut button_poller = ButtonPoller::new();",
        "button_poller.sample(now_ms, trigger_pressed, mode_pressed, &mut button_events);",
        "buttons::dispatch(&mut controller, event, now_ms);",
    ] {
        assert!(main_rs.contains(needle), "main.rs must contain {needle}");
    }
    assert!(
        main_rs.contains(&format!(
            "{sticks3}\n    let sticks3_buttons =\n        buttons_sticks3::Sticks3Buttons::new("
        )),
        "main.rs must build the buttons behind the StickS3 features"
    );
    let src = fs::read_to_string(esp_root.join("src/buttons_sticks3.rs")).expect("buttons_sticks3");
    assert!(src.contains("Gpio11") && src.contains("Gpio12"));
    assert!(src.contains("Pull::Up"));
    assert!(src.contains("is_low()"), "the buttons are active low");
}

/// #744 part 2: the speaker and IMU drivers sit behind the StickS3 features, the Symcode target
/// keeps the log feedback, the speaker never sleeps or blocks on I2S, and the main loop feeds the
/// accelerometer to the controller and ticks the speaker.
#[test]
fn esp_crate_speaker_and_imu_are_sticks3_only_and_never_block() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let sticks3 = "#[cfg(any(feature = \"sticks3_u173\", feature = \"sticks3_u173_uart\"))]";
    for module in ["speaker_sticks3", "imu_sticks3"] {
        assert!(
            main_rs.contains(&format!("{sticks3}\nmod {module};")),
            "main.rs must declare mod {module} behind the StickS3 features"
        );
    }
    for needle in [
        format!("{sticks3}\n    let mut feedback = speaker_sticks3::SpeakerFeedback(&speaker);"),
        "#[cfg(not(any(\n        feature = \"sticks3_u173\",\n        feature = \"sticks3_u173_uart\",\n        feature = \"atoms3lite_u173_bench\"\n    )))]\n    let mut feedback = bench::LogFeedback;".to_owned(),
        format!("{sticks3}\n        speaker.borrow_mut().tick();"),
        format!("{sticks3}\n    imu.begin();"),
        "controller.on_accel_sample(accel_mg, now_ms);".to_owned(),
        "peripherals.i2s0".to_owned(),
    ] {
        assert!(main_rs.contains(&needle), "main.rs must contain {needle:?}");
    }
    let speaker =
        fs::read_to_string(esp_root.join("src/speaker_sticks3.rs")).expect("speaker_sticks3");
    assert!(
        !speaker.contains("sleep") && !speaker.contains("delay"),
        "the speaker must not sleep; the core ToneSequencer times the cues"
    );
    assert!(
        speaker.contains("self.i2s.write(&self.pending[self.pending_at..], 0)"),
        "I2S writes must not wait"
    );
    assert!(speaker.contains("ToneSequencer"));
}

/// #747: the AtomS3 bench drivers sit behind `atoms3lite_u173_bench`; the U173 and the U120 share
/// one Grove bus (GPIO2/GPIO1); the screen reuses the core band renderer and U120 encoder; the one
/// button feeds both debouncer inputs.
#[test]
fn esp_crate_atoms3_bench_drivers_are_gated_and_share_the_grove_bus() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let atoms3 = "#[cfg(feature = \"atoms3lite_u173_bench\")]";
    for module in ["button_atoms3", "display_u120", "feedback_atoms3"] {
        assert!(
            main_rs.contains(&format!("{atoms3}\nmod {module};")),
            "main.rs must declare mod {module} behind atoms3lite_u173_bench"
        );
    }
    assert!(main_rs.contains(
        "#[cfg(any(\n    feature = \"sticks3_u173\",\n    feature = \"sticks3_u173_uart\",\n    feature = \"atoms3lite_u173_bench\"\n))]\nmod i2c_bus;"
    ));
    for needle in [
        "let grove_bus = stockbot_scanner_core::i2c::SharedI2cBus::new(i2c_bus::EspI2cBus::new(\n        peripherals.i2c0,\n        peripherals.pins.gpio2,\n        peripherals.pins.gpio1,\n    )?);",
        "let mut scanner = scanner_u173::ScannerU173::new(grove_bus.clone(), serial_scanner);",
        "let mut display = display_u120::U120Display::new(grove_bus);",
        "feedback_atoms3::LedFeedback::new(peripherals.rmt.channel0, peripherals.pins.gpio35);",
        "let atoms3_button = button_atoms3::AtomS3Button::new(peripherals.pins.gpio41)?;",
        "let pressed = atoms3_button.is_pressed();\n                (pressed, pressed)",
    ] {
        assert!(main_rs.contains(needle), "main.rs must contain {needle}");
    }
    let display = fs::read_to_string(esp_root.join("src/display_u120.rs")).expect("display_u120");
    assert!(display.contains("use stockbot_scanner_core::screen::draw_bands;"));
    assert!(display.contains("use stockbot_scanner_core::u120::"));
    let button = fs::read_to_string(esp_root.join("src/button_atoms3.rs")).expect("button_atoms3");
    assert!(
        button.contains("Gpio41") && button.contains("Pull::Up") && button.contains("is_low()")
    );
    let manifest = fs::read_to_string(esp_root.join("Cargo.toml")).expect("Cargo.toml");
    assert!(
        manifest.contains(
            "esp-idf-hal = { version = \"=0.45.2\", default-features = false, features = [\"rmt-legacy\"] }"
        ),
        "the LED needs the legacy RMT driver of the exact esp-idf-hal that esp-idf-svc uses"
    );
}

const USB_HOST_CRATE: &str = "crates/stockbot-usb-host";

/// Words of `line` outside a trailing `//` comment.
fn code_words(line: &str) -> Vec<&str> {
    line.split("//")
        .next()
        .unwrap_or("")
        .split(|c: char| !(c.is_alphanumeric() || c == '_'))
        .filter(|w| !w.is_empty())
        .collect()
}

fn rust_sources(dir: &Path) -> Vec<(PathBuf, String)> {
    let mut files = Vec::new();
    let mut stack = vec![dir.to_path_buf()];
    while let Some(dir) = stack.pop() {
        for entry in
            fs::read_dir(&dir).unwrap_or_else(|e| panic!("read_dir {}: {e}", dir.display()))
        {
            let path = entry.unwrap().path();
            if path.is_dir() {
                stack.push(path);
            } else if path.extension().is_some_and(|e| e == "rs") {
                files.push(path);
            }
        }
    }
    files
        .into_iter()
        .map(|p| {
            let body =
                fs::read_to_string(&p).unwrap_or_else(|e| panic!("read {}: {e}", p.display()));
            (p, body)
        })
        .collect()
}

/// CJ's ruling on #445 (2026-11-26): only the USB host crate may use `unsafe`. Every other
/// firmware source stays free of it, the app crates keep `forbid(unsafe_code)`, and inside the
/// USB host crate every `unsafe` block has a `// SAFETY:` comment and no `unsafe` item is public.
#[test]
fn unsafe_is_confined_to_the_usb_host_crate() {
    let usb_host = rust_tree().join(USB_HOST_CRATE);
    let crates = rust_tree().join("crates");
    let mut checked_outside = 0;
    for entry in fs::read_dir(&crates).expect("crates dir") {
        let dir = entry.unwrap().path();
        if dir == usb_host || !dir.is_dir() {
            continue;
        }
        for (file, body) in rust_sources(&dir.join("src")) {
            checked_outside += 1;
            for (n, line) in body.lines().enumerate() {
                assert!(
                    !code_words(line).contains(&"unsafe"),
                    "{}:{} uses unsafe outside {USB_HOST_CRATE}: {line}",
                    file.display(),
                    n + 1
                );
            }
        }
    }
    assert!(checked_outside > 0, "no app crate sources were checked");

    for (root, crate_dir) in [
        ("src/lib.rs", "crates/stockbot-scanner-core"),
        ("src/main.rs", "crates/stockbot-scanner-esp"),
    ] {
        let dir = rust_tree().join(crate_dir);
        let src = fs::read_to_string(dir.join(root)).expect("crate root");
        assert!(
            src.contains("#![forbid(unsafe_code)]"),
            "{crate_dir}/{root} must keep #![forbid(unsafe_code)]"
        );
        let manifest = fs::read_to_string(dir.join("Cargo.toml")).expect("Cargo.toml");
        assert!(
            manifest.contains("[lints.rust]\nunsafe_code = \"forbid\""),
            "{crate_dir}/Cargo.toml must keep unsafe_code = \"forbid\""
        );
    }

    let lib = fs::read_to_string(usb_host.join("src/lib.rs")).expect("usb host lib.rs");
    for attr in [
        "#![deny(unsafe_op_in_unsafe_fn)]",
        "#![deny(clippy::undocumented_unsafe_blocks)]",
    ] {
        assert!(lib.contains(attr), "{USB_HOST_CRATE} must keep {attr}");
    }
    let mut blocks = 0;
    for (file, body) in rust_sources(&usb_host.join("src")) {
        let lines: Vec<&str> = body.lines().collect();
        for (n, line) in lines.iter().enumerate() {
            let words = code_words(line);
            let Some(at) = words.iter().position(|w| *w == "unsafe") else {
                continue;
            };
            let place = format!("{}:{}", file.display(), n + 1);
            assert!(
                !words[..at].contains(&"pub"),
                "{place}: unsafe items must not be public: {line}"
            );
            assert!(
                !words.contains(&"impl"),
                "{place}: unsafe impl is not allowed: {line}"
            );
            if words.get(at + 1) == Some(&"extern") {
                continue;
            }
            blocks += 1;
            let documented = lines[n.saturating_sub(6)..n]
                .iter()
                .any(|l| l.trim_start().starts_with("// SAFETY:"));
            assert!(
                documented,
                "{place}: unsafe block needs a // SAFETY: comment just above it"
            );
        }
        let safety_comments = lines
            .iter()
            .filter(|l| l.trim_start().starts_with("// SAFETY:"))
            .count();
        assert!(
            safety_comments >= blocks,
            "{}: {blocks} unsafe blocks but only {safety_comments} SAFETY comments",
            file.display()
        );
    }
    assert!(
        blocks > 0,
        "the USB host crate has no unsafe blocks to check"
    );
}

/// #748: the Symcode target reads the dongle through the USB host crate, which is optional and
/// turned on only by that target; the scanner decodes with the core HID decoder.
#[test]
fn esp_crate_symcode_scanner_is_gated_and_uses_the_core_decoder() {
    let esp_root = rust_tree().join("crates/stockbot-scanner-esp");
    let main_rs = fs::read_to_string(esp_root.join("src/main.rs")).expect("main.rs");
    let symcode = "#[cfg(feature = \"atoms3lite_symcode_hid\")]";
    for needle in [
        format!("{symcode}\nmod scanner_symcode;"),
        format!(
            "{symcode}\n    let mut scanner = scanner_symcode::SymcodeScanner::new(serial_scanner);"
        ),
    ] {
        assert!(main_rs.contains(&needle), "main.rs must contain {needle:?}");
    }
    let manifest = fs::read_to_string(esp_root.join("Cargo.toml")).expect("Cargo.toml");
    assert!(manifest
        .contains("stockbot-usb-host = { path = \"../stockbot-usb-host\", optional = true }"));
    assert!(
        manifest.contains("atoms3lite_symcode_hid = [\"bench-serial\", \"dep:stockbot-usb-host\"]")
    );
    assert_eq!(
        manifest.matches("dep:stockbot-usb-host").count(),
        1,
        "only the Symcode target may turn on the USB host driver"
    );
    let scanner =
        fs::read_to_string(esp_root.join("src/scanner_symcode.rs")).expect("scanner_symcode.rs");
    assert!(scanner.contains("HidKeycodeDecoder"));
    assert!(scanner.contains("UsbKeyboard::start()"));
    let lib = fs::read_to_string(rust_tree().join(USB_HOST_CRATE).join("src/lib.rs"))
        .expect("usb host lib.rs");
    assert!(lib.contains("find_interrupt_in_endpoint(full, KEYBOARD_INTERFACE)"));
    assert!(lib.contains("boot_report_from(data)"));
    let workspace = fs::read_to_string(rust_tree().join("Cargo.toml")).expect("workspace");
    assert!(
        workspace
            .contains("exclude = [\"crates/stockbot-scanner-esp\", \"crates/stockbot-usb-host\"]"),
        "the USB host crate needs the ESP toolchain, so it stays out of the host workspace"
    );
}
