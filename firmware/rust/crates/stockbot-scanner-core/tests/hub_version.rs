//! The firmware version reported in pairing and heartbeat requests is settable.

use stockbot_scanner_core::{
    build_heartbeat_request_json, build_pair_complete_request_json, DeviceClient, HeartbeatParsed,
    HttpCallResult, MemoryRingStorage, NvsConfig, PairCompleteParsed, ParsedDeviceScanResponse,
    RingBuffer, RingScanRecord, ScannerController,
};

mod common;

use common::controller_fakes::{
    MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
};

/// Records the request bodies the controller would send.
#[derive(Default)]
struct BodyClient {
    pair_bodies: Vec<String>,
    heartbeat_bodies: Vec<String>,
}

fn ok(body: &str, status: i32) -> HttpCallResult {
    HttpCallResult {
        transport_ok: true,
        http_status: status,
        body: body.to_owned(),
        retry_after: None,
    }
}

impl DeviceClient for BodyClient {
    fn set_server_url(&mut self, _url: &str) {}
    fn set_device_token(&mut self, _token: &str) {}

    fn complete_pairing(
        &mut self,
        code: &str,
        label: &str,
        hub_version: &str,
        boot_id: &str,
        out: &mut PairCompleteParsed,
    ) -> HttpCallResult {
        self.pair_bodies.push(build_pair_complete_request_json(
            code,
            label,
            hub_version,
            boot_id,
        ));
        *out = PairCompleteParsed::default();
        ok("", 500)
    }

    fn send_scan(
        &mut self,
        _record: &RingScanRecord,
        _boot_id: &str,
        _age_ms: u32,
        _time_approx: bool,
        _out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        ok("", 500)
    }

    fn flush_batch(
        &mut self,
        _batch: &[RingScanRecord],
        _boot_id: &str,
        _now_ms: u64,
        _last_out: &mut ParsedDeviceScanResponse,
    ) -> HttpCallResult {
        ok("", 500)
    }

    fn send_heartbeat(
        &mut self,
        hub_version: &str,
        boot_id: &str,
        uptime_s: u32,
        queued: usize,
        battery_pct: i32,
        charging: bool,
        out: &mut HeartbeatParsed,
    ) -> HttpCallResult {
        self.heartbeat_bodies.push(build_heartbeat_request_json(
            hub_version,
            boot_id,
            uptime_s,
            queued,
            battery_pct,
            charging,
            false,
        ));
        *out = HeartbeatParsed::default();
        ok("{}", 200)
    }

    fn rotate_token(&mut self) -> HttpCallResult {
        ok("", 500)
    }
}

/// Boots a controller with `cfg`, optionally overriding the version, and runs `drive`.
fn run(
    cfg: NvsConfig,
    version: Option<&str>,
    drive: impl FnOnce(&mut ScannerController<'_, MemoryRingStorage>),
) -> BodyClient {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs {
        cfg,
        ..MockNvs::default()
    };
    let mut portal = MockPortal::default();
    let mut client = BodyClient::default();
    let mut wifi = MockWifi {
        connected: true,
        ..MockWifi::default()
    };
    let mut ring = RingBuffer::new(MemoryRingStorage::default());
    {
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
        if let Some(v) = version {
            controller.set_hub_version(v);
        }
        controller.begin(0, 0, "");
        drive(&mut controller);
    }
    client
}

fn paired_cfg() -> NvsConfig {
    NvsConfig {
        wifi_ssid: "Home".to_owned(),
        device_token: "sbd_x".to_owned(),
        ..NvsConfig::default()
    }
}

fn unpaired_cfg() -> NvsConfig {
    NvsConfig {
        wifi_ssid: "Home".to_owned(),
        ..NvsConfig::default()
    }
}

const PAIRING_CODE: &str = "SBP-0123-ABCD-G";

fn heartbeats(version: Option<&str>) -> Vec<String> {
    run(paired_cfg(), version, |c| c.tick(60_000)).heartbeat_bodies
}

fn pairings(version: Option<&str>) -> Vec<String> {
    run(unpaired_cfg(), version, |c| {
        c.on_barcode_line(PAIRING_CODE, true, 1_000)
    })
    .pair_bodies
}

#[test]
fn default_version_is_unchanged() {
    let hb = heartbeats(None);
    let pair = pairings(None);
    assert_eq!(hb.len(), 1, "{hb:?}");
    assert_eq!(pair.len(), 1, "{pair:?}");
    assert!(hb[0].contains("\"version\":\"1.0.0-esp32\""), "{}", hb[0]);
    assert!(
        pair[0].contains("\"version\":\"1.0.0-esp32\""),
        "{}",
        pair[0]
    );
}

#[test]
fn set_hub_version_changes_heartbeat_and_pairing() {
    let hb = heartbeats(Some("0.1.0"));
    let pair = pairings(Some("0.1.0"));
    assert_eq!(hb.len(), 1, "{hb:?}");
    assert_eq!(pair.len(), 1, "{pair:?}");
    assert!(hb[0].contains("\"version\":\"0.1.0\""), "{}", hb[0]);
    assert!(pair[0].contains("\"version\":\"0.1.0\""), "{}", pair[0]);
    assert!(!hb[0].contains("1.0.0-esp32"));
    assert!(!pair[0].contains("1.0.0-esp32"));
}
