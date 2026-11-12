//! Replays `firmware/test/scenarios/*.json` through `ScannerController` and builds trace JSON
//! matching the C++ `run_scenario_traces.cpp` harness.

use std::cell::RefCell;
use std::path::Path;
use std::rc::Rc;

use serde_json::{json, Value};
use stockbot_scanner_core::{
    CommitMode, Direction, HttpCallResult, OperationalState, RingBuffer,
    ScannerController, UiScreen, RING_CAPACITY,
};

use crate::common::controller_fakes::{
    HttpRequestRecord, MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower,
    MockScanner, MockWifi, SharedMockClient, SharedMockDisplay, SharedMockNvs, SharedMockPortal,
    SharedMockPower, SharedMockWifi,
};
use crate::common::memory_ring_storage::MemoryRingStorage;

pub const SCENARIO_NAMES: &[&str] = &[
    "device_revoked_scan_401",
    "dock_vbus_no_battery",
    "heartbeat_60s_interval",
    "scan_retry_503_buffered",
    "busy_body_scan_not_retried",
    "offline_buffer_full_refuse",
    "factory_reset_from_hold",
    "pairing_expired_transient",
    "inactivity_revert_5min",
];

fn is_controller_op(op: &str) -> bool {
    matches!(
        op,
        "tick"
            | "barcode"
            | "mode_button_down"
            | "mode_button_up"
            | "mode_button_short_press"
            | "mode_button_hold"
            | "mode_button_hold_tick"
            | "mode_button_hold_poll"
            | "trigger_button"
            | "fill_ring_offline"
    )
}

fn step_at_ms(step: &Value) -> u64 {
    step.get("at_ms").and_then(|v| v.as_u64()).unwrap_or(0)
}

fn operational_state_str(s: OperationalState) -> &'static str {
    match s {
        OperationalState::Unprovisioned => "Unprovisioned",
        OperationalState::Pairing => "Pairing",
        OperationalState::Paired => "Paired",
    }
}

fn ui_screen_str(s: UiScreen) -> &'static str {
    match s {
        UiScreen::S0Unprovisioned => "S0_Unprovisioned",
        UiScreen::S1Pairing => "S1_Pairing",
        UiScreen::S2Idle => "S2_Idle",
        UiScreen::S3ScanResult => "S3_ScanResult",
        UiScreen::S4UnknownBarcode => "S4_UnknownBarcode",
        UiScreen::S5OfflineBuffered => "S5_OfflineBuffered",
        UiScreen::S6DockReminder => "S6_DockReminder",
        UiScreen::S7HardError => "S7_HardError",
        UiScreen::S7OfflineBufferRefused => "S7_OfflineBufferRefused",
        UiScreen::SConnected => "S_Connected",
        UiScreen::SPairingExpired => "S_PairingExpired",
        UiScreen::SPairingTooManyTries => "S_PairingTooManyTries",
        UiScreen::SPairingCodeNotAccepted => "S_PairingCodeNotAccepted",
        UiScreen::SPairingCantConnect => "S_PairingCantConnect",
        UiScreen::SWifiButtonHold => "S_WifiButtonHold",
        UiScreen::SWifiButtonHoldErase => "S_WifiButtonHoldErase",
        UiScreen::SWifiSetup => "S_WifiSetup",
    }
}

fn apply_set_nvs(nvs: &Rc<RefCell<MockNvs>>, step: &Value) {
    let mut cfg = nvs.borrow().cfg.clone();
    if let Some(s) = step.get("wifi_ssid").and_then(|v| v.as_str()) {
        cfg.wifi_ssid = s.to_owned();
    }
    if let Some(s) = step.get("wifi_pass").and_then(|v| v.as_str()) {
        cfg.wifi_pass = s.to_owned();
    }
    if let Some(s) = step.get("server_url").and_then(|v| v.as_str()) {
        cfg.server_url = s.to_owned();
    }
    if let Some(s) = step.get("device_token").and_then(|v| v.as_str()) {
        cfg.device_token = s.to_owned();
    }
    if let Some(s) = step.get("label").and_then(|v| v.as_str()) {
        cfg.label = s.to_owned();
    }
    if let Some(n) = step.get("config_version").and_then(|v| v.as_u64()) {
        cfg.config_version = n as u32;
    }
    if let Some(n) = step
        .get("inactivity_revert_seconds")
        .and_then(|v| v.as_u64())
    {
        cfg.inactivity_revert_s = n as u32;
    }
    if let Some(s) = step.get("default_direction").and_then(|v| v.as_str()) {
        cfg.default_direction = if s == "stock_in" {
            Direction::StockIn
        } else {
            Direction::StockOut
        };
    }
    if let Some(s) = step.get("commit_mode").and_then(|v| v.as_str()) {
        cfg.commit_mode = if s == "queue" {
            CommitMode::Queue
        } else {
            CommitMode::Immediate
        };
    }
    nvs.borrow_mut().cfg = cfg;
}

fn apply_power(power: &Rc<RefCell<MockPower>>, step: &Value) {
    let mut st = power.borrow().status.clone();
    if let Some(b) = step.get("vbus").and_then(|v| v.as_bool()) {
        st.vbus_present = b;
    }
    if let Some(b) = step.get("charging").and_then(|v| v.as_bool()) {
        st.charging = b;
    }
    match step.get("battery_pct") {
        Some(Value::Null) => st.battery_pct = None,
        Some(v) => {
            if let Some(n) = v.as_i64() {
                st.battery_pct = Some(n as i32);
            }
        }
        None => {}
    }
    if step
        .get("clear_battery")
        .and_then(|v| v.as_bool())
        .unwrap_or(false)
    {
        st.battery_pct = None;
    }
    power.borrow_mut().status = st;
}

fn apply_portal_submit(portal: &Rc<RefCell<MockPortal>>, step: &Value) {
    let mut pending = portal.borrow().pending.clone();
    pending.ready = true;
    if let Some(s) = step.get("wifi_ssid").and_then(|v| v.as_str()) {
        pending.wifi_ssid = s.to_owned();
    }
    if let Some(s) = step.get("wifi_pass").and_then(|v| v.as_str()) {
        pending.wifi_pass = s.to_owned();
    }
    if let Some(s) = step.get("server_url").and_then(|v| v.as_str()) {
        pending.server_url = s.to_owned();
    }
    portal.borrow_mut().pending = pending;
}

fn http_requests_json(since: &[HttpRequestRecord]) -> Value {
    let items: Vec<Value> = since
        .iter()
        .map(|r| {
            json!({
                "kind": r.kind,
                "at_ms": r.at_ms,
                "http_status": r.http_status,
                "transport_ok": r.transport_ok,
            })
        })
        .collect();
    Value::Array(items)
}

fn make_trace_step(
    at_ms: u64,
    controller: &ScannerController<'_, MemoryRingStorage>,
    bands: &stockbot_scanner_core::DisplayBands,
    http_since: &[HttpRequestRecord],
) -> Value {
    let count = controller.ring_count();
    let barcodes: Vec<Value> = controller
        .ring_trace_barcodes()
        .into_iter()
        .map(Value::String)
        .collect();

    json!({
        "at_ms": at_ms,
        "operational_state": operational_state_str(controller.operational_state()),
        "ui_screen": ui_screen_str(controller.ui_screen()),
        "ring_count": count,
        "ring_barcodes": barcodes,
        "http_requests": http_requests_json(http_since),
        "band1": bands.band1,
        "band1_right": bands.band1_right,
        "band2_line1": bands.band2_line1,
        "band2_line2": bands.band2_line2,
        "band3": bands.band3,
    })
}

/// Run one scenario JSON value and return the trace root object (`name` + `steps`).
pub fn run_scenario(root: &Value, scenario_name: &str) -> Value {
    let steps = root
        .get("steps")
        .and_then(|v| v.as_array())
        .expect("scenario missing steps array");

    let mut scanner = MockScanner::default();
    let display_rc = Rc::new(RefCell::new(MockDisplay::default()));
    let mut display = SharedMockDisplay(display_rc.clone());
    let mut feedback = MockFeedback::default();
    let power_rc = Rc::new(RefCell::new(MockPower::default()));
    let mut power = SharedMockPower(power_rc.clone());
    let nvs_rc = Rc::new(RefCell::new(MockNvs::default()));
    let mut nvs = SharedMockNvs(nvs_rc.clone());
    let portal_rc = Rc::new(RefCell::new(MockPortal::default()));
    let mut portal = SharedMockPortal(portal_rc.clone());
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    let mut ring = RingBuffer::new(MemoryRingStorage::default());
    let mut controller = ScannerController::new(
        &mut scanner,
        &mut display,
        &mut feedback,
        &mut power,
        &mut nvs,
        &mut portal,
        &mut client,
        &mut wifi,
        &mut ring,
    );

    let mut trace_steps = Vec::with_capacity(steps.len());
    let mut begun = false;
    let mut http_mark = 0;

    for step in steps {
        let op = step.get("op").and_then(|v| v.as_str()).unwrap_or_default();
        let at_ms = step_at_ms(step);
        client_rc.borrow_mut().now_ms = at_ms;

        match op {
            "set_nvs" => apply_set_nvs(&nvs_rc, step),
            "http_queue" => client_rc.borrow_mut().push_http_queue(step),
            "wifi_connected" => wifi_rc.borrow_mut().connected = true,
            "wifi_disconnected" => wifi_rc.borrow_mut().connected = false,
            "power" => apply_power(&power_rc, step),
            "portal_submit" => apply_portal_submit(&portal_rc, step),
            _ => {}
        }

        let device_token = nvs_rc.borrow().cfg.device_token.clone();
        if op == "begin" {
            let nonce = step
                .get("hw_random_nonce")
                .and_then(|v| v.as_u64())
                .unwrap_or(0) as u32;
            if !device_token.is_empty() {
                client_rc.borrow_mut().token = device_token.clone();
            }
            controller.begin(at_ms, nonce, "");
            begun = true;
        } else if !begun && is_controller_op(op) {
            controller.begin(at_ms, 0, "");
            begun = true;
        }

        match op {
            "tick" => controller.tick(at_ms),
            "barcode" => {
                let manual = step
                    .get("manual")
                    .and_then(|v| v.as_bool())
                    .unwrap_or(false);
                let line = step
                    .get("line")
                    .and_then(|v| v.as_str())
                    .unwrap_or_default();
                controller.on_barcode_line(line, manual, at_ms);
            }
            "mode_button_down" => controller.on_mode_button_down(at_ms),
            "mode_button_up" => controller.on_mode_button_up(at_ms),
            "mode_button_short_press" => controller.on_mode_button_short_press(at_ms),
            "mode_button_hold" => {
                let hold_ms = step
                    .get("hold_ms")
                    .and_then(|v| v.as_u64())
                    .unwrap_or(10_000);
                controller.on_mode_button_hold(hold_ms, at_ms);
            }
            "mode_button_hold_tick" => {
                let hold_ms = step.get("hold_ms").and_then(|v| v.as_u64()).unwrap_or(0);
                controller.on_mode_button_hold_tick(hold_ms, at_ms);
            }
            "mode_button_hold_poll" => controller.on_mode_button_hold_poll(at_ms),
            "trigger_button" => {
                let pressed = step
                    .get("pressed")
                    .and_then(|v| v.as_bool())
                    .unwrap_or(true);
                controller.on_trigger_button_changed(pressed, at_ms);
            }
            "fill_ring_offline" => {
                let count = step
                    .get("count")
                    .and_then(|v| v.as_u64())
                    .unwrap_or(RING_CAPACITY as u64) as usize;
                {
                    let mut inner = client_rc.borrow_mut();
                    inner.next_scan = HttpCallResult {
                        transport_ok: false,
                        http_status: 0,
                        body: String::new(),
                    };
                    inner.scan_queue.clear();
                }
                let mut t = at_ms;
                for i in 0..count {
                    let bc = format!("{:012}", i + 1);
                    controller.on_barcode_line(&bc, false, t);
                    t += 1;
                }
                client_rc.borrow_mut().now_ms = t - 1;
            }
            _ => {}
        }

        let http_since: Vec<HttpRequestRecord> = client_rc.borrow().http_log[http_mark..].to_vec();
        let bands = display_rc.borrow().last.clone();
        trace_steps.push(make_trace_step(at_ms, &controller, &bands, &http_since));
        http_mark = client_rc.borrow().http_log.len();
    }

    json!({
        "name": scenario_name,
        "steps": trace_steps,
    })
}

pub fn run_scenario_json(scenario_json: &str, scenario_name: &str) -> Value {
    let root: Value = serde_json::from_str(scenario_json).expect("parse scenario JSON");
    run_scenario(&root, scenario_name)
}

pub fn load_and_run_scenario(scenario_path: &Path, scenario_name: &str) -> Value {
    let raw = std::fs::read_to_string(scenario_path)
        .unwrap_or_else(|e| panic!("read {}: {e}", scenario_path.display()));
    run_scenario_json(&raw, scenario_name)
}

pub fn trace_matches_golden(got: &Value, golden: &Value) -> Result<(), String> {
    if got == golden {
        return Ok(());
    }
    let got_str = serde_json::to_string_pretty(got).unwrap_or_default();
    let want_str = serde_json::to_string_pretty(golden).unwrap_or_default();
    Err(format!(
        "trace mismatch:\n--- golden\n+++ got\n{got_str}\n\n--- expected pretty ---\n{want_str}"
    ))
}

pub fn golden_trace_from_file(path: &Path) -> Value {
    let raw = std::fs::read_to_string(path)
        .unwrap_or_else(|e| panic!("read golden {}: {e}", path.display()));
    serde_json::from_str(&raw).expect("parse golden trace JSON")
}
