//! Integration locks for retry policy wiring (#718 S-b).

mod common;

use std::cell::RefCell;
use std::rc::Rc;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
    SharedMockClient, SharedMockWifi,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{
    envelope_ms, DeviceClient, Direction, HttpCallResult, Op, Outcome, RetryState, RingBuffer,
    ScannerController, FLUSH, HEARTBEAT,
};

#[test]
fn envelope_k_min_64_u32_max_returns_cap_without_panic() {
    assert_eq!(envelope_ms(&FLUSH, u32::MAX), FLUSH.cap_ms);
}

#[test]
fn heartbeat_ok_clears_flush_backoff_immediately() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi {
        connected: true,
        ..MockWifi::default()
    }));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    client_rc.borrow_mut().set_device_token("sbd_x");
    let mut ring = RingBuffer::new(MemoryRingStorage::default());
    let _ = ring.enqueue("4011", Direction::StockOut, 0, 1);
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
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 503,
            body: String::new(),
            retry_after: None,
        };
    }
    controller.tick(0);
    assert_eq!(client_rc.borrow().flush_batch_calls, 1);
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_hb = HttpCallResult {
            transport_ok: true,
            http_status: 200,
            body: r#"{"config":{"configVersion":1}}"#.to_owned(),
            retry_after: None,
        };
        inner.hb_body.parse_ok = true;
    }
    controller.tick(60_000);
    assert_eq!(client_rc.borrow().flush_batch_calls, 2);
}

#[test]
fn scan_429_does_not_gate_heartbeat() {
    let mut state = RetryState::default();
    state = stockbot_scanner_core::on_outcome(
        state,
        Op::Flush,
        Outcome::RateLimited,
        Some(120_000),
        0,
        &mut || 1000,
    );
    assert_eq!(state.not_before_ms[Op::Heartbeat as usize], 0);
    assert_eq!(state.outage_streak, 0);
    assert_eq!(state.rl_until_ms[Op::Flush as usize], 120_000);
    assert!(stockbot_scanner_core::may_send(&state, Op::Heartbeat, 0));
    assert_eq!(envelope_ms(&HEARTBEAT, 3), 300_000);
}

#[test]
fn closed_gate_leaves_ring_unchanged_and_makes_no_http() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = String::new();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi {
        connected: true,
        ..MockWifi::default()
    }));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    let _ = ring.enqueue("4011", Direction::StockOut, 100, 1);
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
    assert_eq!(controller.ring_trace_barcodes(), vec!["4011".to_string()]);
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 503,
            body: String::new(),
            retry_after: None,
        };
    }
    controller.tick(0);
    controller.tick(500);
    assert_eq!(controller.ring_count(), 1);
    assert_eq!(controller.ring_trace_barcodes(), vec!["4011".to_string()]);
    assert_eq!(client_rc.borrow().http_log.len(), 0);
    assert_eq!(client_rc.borrow().flush_batch_calls, 0);
}
