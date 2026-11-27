//! `rotateToken: true` in a heartbeat makes the controller call the rotate endpoint and keep the
//! new token only once it is saved (#457 item 1).

mod common;

use std::cell::RefCell;
use std::rc::Rc;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockHttpScriptEntry, MockNvs, MockPortal, MockPower,
    MockScanner, MockWifi, SharedMockClient, SharedMockNvs,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{HttpCallResult, RingBuffer, ScannerController, UiScreen};

const OLD: &str = "sbd_old";
const NEW: &str = "sbd_new";

struct Rig {
    client: Rc<RefCell<MockClient>>,
    nvs: Rc<RefCell<MockNvs>>,
}

fn response(status: i32, body: &str) -> HttpCallResult {
    HttpCallResult {
        transport_ok: true,
        http_status: status,
        body: body.to_owned(),
        retry_after: None,
    }
}

fn heartbeat(body: &str) -> MockHttpScriptEntry {
    MockHttpScriptEntry {
        result: response(200, body),
        ..MockHttpScriptEntry::default()
    }
}

impl Rig {
    fn new() -> Self {
        let mut nvs = MockNvs::default();
        nvs.cfg.wifi_ssid = "Home".to_owned();
        nvs.cfg.device_token = OLD.to_owned();
        Self {
            client: Rc::new(RefCell::new(MockClient::default())),
            nvs: Rc::new(RefCell::new(nvs)),
        }
    }

    fn queue_heartbeat(&self, body: &str) {
        self.client.borrow_mut().hb_queue.push_back(heartbeat(body));
    }

    fn queue_rotate(&self, status: i32, body: &str) {
        self.client
            .borrow_mut()
            .rotate_queue
            .push_back(response(status, body));
    }

    fn rotate_calls(&self) -> usize {
        self.client.borrow().rotate_call_tokens.len()
    }

    fn nvs_token(&self) -> String {
        self.nvs.borrow().cfg.device_token.clone()
    }

    fn client_token(&self) -> String {
        self.client.borrow().token.clone()
    }

    fn run(&self, drive: impl FnOnce(&mut ScannerController<'_, MemoryRingStorage>)) {
        let mut scanner = MockScanner::default();
        let mut display = MockDisplay::default();
        let mut feedback = MockFeedback::default();
        let mut power = MockPower::default();
        let mut portal = MockPortal::default();
        let mut wifi = MockWifi {
            connected: true,
            ..MockWifi::default()
        };
        let mut client = SharedMockClient(self.client.clone());
        let mut nvs = SharedMockNvs(self.nvs.clone());
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
        controller.begin(0, 0, "");
        drive(&mut controller);
    }
}

#[test]
fn rotate_true_fetches_and_saves_new_token() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(200, r#"{"token":"sbd_new"}"#);
    rig.queue_heartbeat(r#"{"rotateToken":false}"#);
    rig.run(|c| {
        c.tick(60_000);
        assert_eq!(c.ui_screen(), UiScreen::S2Idle);
    });
    assert_eq!(rig.rotate_calls(), 1);
    assert_eq!(rig.client.borrow().rotate_call_tokens[0], OLD);
    assert_eq!(rig.nvs_token(), NEW);
    assert_eq!(rig.client_token(), NEW);
}

#[test]
fn next_request_uses_the_new_token() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(200, r#"{"token":"sbd_new"}"#);
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(200, r#"{"token":"sbd_newer"}"#);
    rig.run(|c| {
        c.tick(60_000);
        c.tick(120_000);
    });
    // The second rotate call went out with the token from the first.
    assert_eq!(rig.client.borrow().rotate_call_tokens, vec![OLD, NEW]);
    assert_eq!(rig.nvs_token(), "sbd_newer");
    assert_eq!(rig.client_token(), "sbd_newer");
}

#[test]
fn rotate_500_keeps_old_token_then_retries_next_heartbeat() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(500, "");
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(200, r#"{"token":"sbd_new"}"#);
    rig.run(|c| {
        c.tick(60_000);
        assert_eq!(rig.nvs_token(), OLD);
        assert_eq!(rig.client_token(), OLD);
        assert_eq!(rig.rotate_calls(), 1);
        c.tick(120_000);
    });
    assert_eq!(rig.rotate_calls(), 2);
    assert_eq!(rig.nvs_token(), NEW);
    assert_eq!(rig.client_token(), NEW);
}

#[test]
fn rotate_transport_failure_keeps_old_token() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    // Empty rotate queue: the mock answers with a transport failure.
    rig.run(|c| c.tick(60_000));
    assert_eq!(rig.rotate_calls(), 1);
    assert_eq!(rig.nvs_token(), OLD);
    assert_eq!(rig.client_token(), OLD);
}

#[test]
fn rotate_200_without_usable_token_keeps_old_token() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(200, r#"{"token":""}"#);
    rig.run(|c| c.tick(60_000));
    assert_eq!(rig.rotate_calls(), 1);
    assert_eq!(rig.nvs_token(), OLD);
    assert_eq!(rig.client_token(), OLD);
}

#[test]
fn failed_nvs_save_keeps_old_token_in_use() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(200, r#"{"token":"sbd_new"}"#);
    rig.run(|c| {
        rig.nvs.borrow_mut().fail_saves = true;
        c.tick(60_000);
    });
    assert_eq!(rig.rotate_calls(), 1);
    assert_eq!(rig.nvs_token(), OLD);
    assert_eq!(rig.client_token(), OLD);
}

#[test]
fn rotate_401_device_revoked_matches_heartbeat_revoke() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":true}"#);
    rig.queue_rotate(401, r#"{"code":"device_revoked"}"#);
    rig.run(|c| {
        c.tick(60_000);
        // Same state the heartbeat device_revoked test asserts.
        assert_eq!(c.ui_screen(), UiScreen::S7HardError);
    });
    assert!(rig.nvs_token().is_empty());
    assert!(rig.client_token().is_empty());
}

#[test]
fn rotate_false_or_absent_makes_no_rotate_call() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"rotateToken":false}"#);
    rig.queue_heartbeat(r#"{"config":{"configVersion":1}}"#);
    rig.run(|c| {
        c.tick(60_000);
        c.tick(120_000);
    });
    assert_eq!(rig.client.borrow().send_heartbeat_calls, 2);
    assert_eq!(rig.rotate_calls(), 0);
    assert_eq!(rig.nvs_token(), OLD);
}

#[test]
fn legacy_token_in_heartbeat_applies_directly() {
    let rig = Rig::new();
    rig.queue_heartbeat(r#"{"token":"sbd_legacy"}"#);
    rig.run(|c| c.tick(60_000));
    assert_eq!(rig.rotate_calls(), 0);
    assert_eq!(rig.nvs_token(), "sbd_legacy");
    assert_eq!(rig.client_token(), "sbd_legacy");
}
