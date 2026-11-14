//! Pins the 60 s heartbeat interval (firmware-ota.md / api-contract heartbeatSeconds).

#[path = "common/mod.rs"]
mod common;

use std::rc::Rc;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
    SharedMockClient, SharedMockDisplay, SharedMockNvs, SharedMockPortal, SharedMockPower,
    SharedMockWifi,
};
use common::memory_ring_storage::MemoryRingStorage;

use stockbot_scanner_core::{RingBuffer, ScannerController};

const HEARTBEAT_INTERVAL_MS: u64 = 60_000;

#[test]
fn heartbeat_not_sent_before_60s_boundary() {
    let mut scanner = MockScanner::default();
    let display_rc = Rc::new(std::cell::RefCell::new(MockDisplay::default()));
    let mut display = SharedMockDisplay(display_rc);
    let mut feedback = MockFeedback::default();
    let power_rc = Rc::new(std::cell::RefCell::new(MockPower::default()));
    let mut power = SharedMockPower(power_rc);
    let nvs_rc = Rc::new(std::cell::RefCell::new(MockNvs::default()));
    let mut nvs = SharedMockNvs(nvs_rc.clone());
    let portal_rc = Rc::new(std::cell::RefCell::new(MockPortal::default()));
    let mut portal = SharedMockPortal(portal_rc);
    let client_rc = Rc::new(std::cell::RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(std::cell::RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    let mut ring = RingBuffer::new(MemoryRingStorage::default());

    nvs_rc.borrow_mut().cfg.wifi_ssid = "Home".into();
    nvs_rc.borrow_mut().cfg.device_token = "sbd_hb".into();
    wifi_rc.borrow_mut().connected = true;

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
    controller.begin(0, 0, "");
    client_rc.borrow_mut().now_ms = 1_000;
    controller.tick(1_000);
    client_rc.borrow_mut().now_ms = HEARTBEAT_INTERVAL_MS - 1;
    controller.tick(HEARTBEAT_INTERVAL_MS - 1);
    let calls_before = client_rc.borrow().send_heartbeat_calls;
    client_rc.borrow_mut().now_ms = HEARTBEAT_INTERVAL_MS;
    controller.tick(HEARTBEAT_INTERVAL_MS);
    assert!(
        client_rc.borrow().send_heartbeat_calls > calls_before,
        "heartbeat must fire at 60 s boundary"
    );
}
