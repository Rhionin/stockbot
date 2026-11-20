//! Suggested tests for PR #713 (Architect review of dc546707). Add as firmware/rust/crates/stockbot-scanner-core/tests/suggested_713.rs
mod common;

use std::cell::RefCell;
use std::rc::Rc;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
    SharedMockClient, SharedMockDisplay, SharedMockWifi,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::DeviceClient;
use stockbot_scanner_core::{
    classify_device_auth_401, DeviceAuth401Kind, Direction, HttpCallResult, OperationalState,
    RingBuffer, ScannerController,
};

type Client = Rc<RefCell<MockClient>>;
type Wifi = Rc<RefCell<MockWifi>>;

struct Out {
    nvs_token: String,
}

fn with_fx(
    prefill: &[&str],
    portal_pending_wifi: Option<&str>,
    f: impl FnOnce(&mut ScannerController<'_, MemoryRingStorage>, &Client, &Wifi),
) -> Out {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    if let Some(ssid) = portal_pending_wifi {
        portal.pending.ready = true;
        portal.pending.wifi_ssid = ssid.to_owned();
        portal.pending.wifi_pass = "pw".to_owned();
    }
    let client_rc: Client = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc: Wifi = Rc::new(RefCell::new(MockWifi {
        connected: true,
        ..MockWifi::default()
    }));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    client_rc.borrow_mut().set_device_token("sbd_x");
    let mut ring = RingBuffer::new(MemoryRingStorage::default());
    for (i, b) in prefill.iter().enumerate() {
        let _ = ring.enqueue(b, Direction::StockOut, i as u64, 1);
    }
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
        controller.begin(0, 0, "");
        f(&mut controller, &client_rc, &wifi_rc);
    }
    Out {
        nvs_token: nvs.cfg.device_token.clone(),
    }
}

fn opaque_401(c: &Client) {
    let mut i = c.borrow_mut();
    i.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 401,
        body: String::new(),
    };
    i.scan_body.unauthorized = true;
    i.scan_body.parse_ok = true;
}

fn ok_200(c: &Client) {
    let mut i = c.borrow_mut();
    i.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 200,
        body: r#"{"scanStatus":"created","known":true}"#.to_owned(),
    };
    i.scan_body.unauthorized = false;
    i.scan_body.status = "created".to_owned();
    i.scan_body.parse_ok = true;
}

fn flush_calls(c: &Client) -> u32 {
    c.borrow().flush_batch_calls
}

#[test]
fn test_s713_classify_vectors() {
    use DeviceAuth401Kind::{DeviceRevoked, NoParseableCode, ServerCoded};
    let vectors: &[(&str, DeviceAuth401Kind)] = &[
        (r#"{"code":"device_revoked"}"#, DeviceRevoked),
        (r#"{"code":"unauthorized"}"#, ServerCoded),
        ("  \n{\"code\":\"device_revoked\"}\n", DeviceRevoked),
        (r#"{"code":"device_revoked_x"}"#, NoParseableCode),
        (r#"{"code":"revoked"}"#, NoParseableCode),
        (r#"{"code":"Device_Revoked"}"#, NoParseableCode),
        (r#"{"code":" device_revoked"}"#, NoParseableCode),
        (r#"{"code":"device_revoked "}"#, NoParseableCode),
        (r#"{"code":"unauthorized_x"}"#, NoParseableCode),
        (r#"{"code":"unauth"}"#, NoParseableCode),
        (r#"{"code":"UNAUTHORIZED"}"#, NoParseableCode),
        (r#"{"code":"unauthorized "}"#, NoParseableCode),
        (r#"<p>error "code": "device_revoked"</p>"#, NoParseableCode),
        (r#""code":"device_revoked""#, NoParseableCode),
        (r#"proxy said: {"code":"device_revoked"}"#, NoParseableCode),
        (r#"{"code":"device_revoked"#, NoParseableCode),
        (r#"{"code":"unauthorized"#, NoParseableCode),
        (r#"{"code":"device_revoked""#, NoParseableCode),
        (r#"{"code":"unauthorized","#, NoParseableCode),
        (r#"{"code":null}"#, NoParseableCode),
        (r#"{"code":["device_revoked"]}"#, NoParseableCode),
        (r#"{"code":true}"#, NoParseableCode),
        (r#"{"code":"unauthorized"}}"#, NoParseableCode),
        (r#"{"code":"unauthorized"} garbage}"#, NoParseableCode),
        (r#"{"error":{"code":"unauthorized"}}"#, NoParseableCode),
        (
            r#"{"code":"unauthorized","code":"device_revoked"}"#,
            NoParseableCode,
        ),
        (
            r#"{"code":"device_revoked","code":"unauthorized"}"#,
            NoParseableCode,
        ),
        (r#"{"code":"\u0075nauthorized"}"#, NoParseableCode),
        (r#"{"code":"unauthorized\n"}"#, NoParseableCode),
        ("\t{\"code\":\"device_revoked\"}", DeviceRevoked),
        ("\r{\"code\":\"device_revoked\"}", DeviceRevoked),
        (" {\"code\":\"unauthorized\"}", ServerCoded),
        ("{\"code\":\"device_revoked\"}\t", DeviceRevoked),
        ("{\"code\":\"unauthorized\"}\r", ServerCoded),
        ("{\"code\":\"unauthorized\"}\n", ServerCoded),
        ("\u{000b}{\"code\":\"device_revoked\"}", NoParseableCode),
        (r#"{"code":"device_revoked"}\u{000b}"#, NoParseableCode),
        ("\u{00a0}{\"code\":\"device_revoked\"}", NoParseableCode),
        (r#"{"code":"device_revoked"}\u{00a0}"#, NoParseableCode),
        ("\u{0085}{\"code\":\"unauthorized\"}", NoParseableCode),
        (r#"{"code":"unauthorized"}\u{0085}"#, NoParseableCode),
        ("\u{2028}{\"code\":\"device_revoked\"}", NoParseableCode),
        (r#"{"code":"device_revoked"}\u{2028}"#, NoParseableCode),
        ("\u{3000}{\"code\":\"unauthorized\"}", NoParseableCode),
        (r#"{"code":"unauthorized"}\u{3000}"#, NoParseableCode),
    ];
    for (body, want) in vectors {
        assert_eq!(classify_device_auth_401(body), *want, "body: {body}");
    }
}

#[test]
fn test_s713_inline_401_arms_flush_gate() {
    with_fx(&[], None, |c, client, _| {
        c.tick(0);
        opaque_401(client);
        c.on_barcode_line("4012", false, 100); // inline 401: streak 1, flush gate 1100
        let base = flush_calls(client);
        c.tick(600);
        assert_eq!(flush_calls(client), base);
        c.tick(1100);
        assert_eq!(flush_calls(client), base + 1);
    });
}

#[test]
fn test_s713_flush_200_resets_streak() {
    with_fx(&["4011"], None, |c, client, _| {
        opaque_401(client);
        c.tick(0);
        assert_eq!(flush_calls(client), 1);
        let ack_id = stockbot_scanner_core::c_str_from_fixed(
            &client.borrow().last_flush_batch[0].client_scan_id,
        );
        {
            let mut i = client.borrow_mut();
            i.next_scan = HttpCallResult {
                transport_ok: true,
                http_status: 200,
                body: format!(r#"{{"results":[{{"id":"{ack_id}","status":"created"}}]}}"#),
            };
            i.scan_body.unauthorized = false;
            i.scan_body.status = "created".to_owned();
            i.scan_body.parse_ok = true;
        }
        c.tick(1000);
        assert_eq!(flush_calls(client), 2);
        opaque_401(client);
        c.on_barcode_line("4012", false, 1000);
        let base = flush_calls(client);
        c.tick(1500);
        assert_eq!(flush_calls(client), base);
        c.tick(2000);
        assert_eq!(flush_calls(client), base + 1);
    });
}

#[test]
fn test_s713_inline_200_resets_streak() {
    with_fx(&[], None, |c, client, _| {
        c.tick(0);
        opaque_401(client);
        c.on_barcode_line("4011", false, 0); // streak 1, gate 1000
        c.tick(1000); // streak 2, gate 3000
        c.tick(3000); // streak 3, gate 7000
        ok_200(client);
        c.on_barcode_line("4012", false, 7000); // inline 200 drains the ring and must reset the streak
        opaque_401(client);
        c.on_barcode_line("4013", false, 8000); // streak 0 -> wait 1000 -> gate 9000
        let base = flush_calls(client);
        c.tick(9000);
        assert_eq!(flush_calls(client), base + 1);
    });
}

#[test]
fn test_s713_heartbeat_401_shares_streak() {
    with_fx(&["4011"], None, |c, client, _| {
        opaque_401(client);
        c.tick(0); // flush 401: streak 1, gate 1000
        client.borrow_mut().next_hb = HttpCallResult {
            transport_ok: true,
            http_status: 401,
            body: String::new(),
        };
        c.tick(61_000); // flush 401 (wait 2000, streak 2), then heartbeat 401 (wait 4000, streak 3)
        c.tick(63_000); // flush 401 with streak 3: wait 8000 -> gate 71000
        let base = flush_calls(client);
        c.tick(67_000);
        assert_eq!(flush_calls(client), base);
        c.tick(71_000);
        assert_eq!(flush_calls(client), base + 1);
    });
}

#[test]
fn test_s713_heartbeat_200_clears_flush_gate() {
    with_fx(&["4011"], None, |c, client, _| {
        opaque_401(client);
        for t in [0u64, 1000, 3000, 7000, 15_000, 31_000] {
            c.tick(t); // streak 6, flush gate 61000
        }
        {
            let mut i = client.borrow_mut();
            i.next_hb = HttpCallResult {
                transport_ok: true,
                http_status: 200,
                body: r#"{"config":{"configVersion":1}}"#.to_owned(),
            };
            i.hb_body.parse_ok = true;
        }
        c.tick(60_500); // flush gate closed; heartbeat 200 resets streak and both gates
        let base = flush_calls(client);
        c.tick(60_600);
        assert_eq!(flush_calls(client), base + 1);
    });
}

#[test]
fn test_s713_server_coded_repair_resets_backoff_and_in_memory_token() {
    let out = with_fx(&["4011"], Some("NewWiFi"), |c, client, _| {
        opaque_401(client);
        c.tick(0);
        c.tick(1000);
        c.tick(3000); // streak 3, gate 7000
        client.borrow_mut().next_hb = HttpCallResult {
            transport_ok: true,
            http_status: 401,
            body: r#"{"code":"unauthorized"}"#.to_owned(),
        };
        c.tick(61_000); // flush opaque 401 (streak 4, gate 69000), then heartbeat coded 401 -> wipe
        assert_eq!(c.operational_state(), OperationalState::Pairing);
        // a Wi-Fi portal submission saves the in-memory config: it must not resurrect the wiped token
        c.on_mode_button_hold(5500, 62_000);
        c.tick(63_000);
        assert_eq!(c.operational_state(), OperationalState::Pairing);
        // re-pair, then the first scan must send at once (no stale streak or gate)
        {
            let mut i = client.borrow_mut();
            i.next_pair = HttpCallResult {
                transport_ok: true,
                http_status: 201,
                body: String::new(),
            };
            i.pair_body.device_token = "sbd_new".to_owned();
            i.pair_body.config.commit_mode = "queue".to_owned();
        }
        c.on_barcode_line("SBP-0123-ABCD-G", true, 64_000);
        assert_eq!(c.operational_state(), OperationalState::Paired);
        ok_200(client);
        client.borrow_mut().send_order.clear();
        c.tick(64_100);
        c.on_barcode_line("4020", false, 64_200);
        assert_eq!(client.borrow().send_order.len(), 1);
    });
    assert_eq!(out.nvs_token, "sbd_new");
}

#[test]
fn test_s713_no_code_401_keeps_household_name_on_display() {
    let mut scanner = MockScanner::default();
    let display_rc = Rc::new(RefCell::new(MockDisplay::default()));
    let mut display = SharedMockDisplay(display_rc.clone());
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc: Client = Rc::new(RefCell::new(MockClient::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = MockWifi {
        connected: true,
        ..MockWifi::default()
    };
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let mut ring = RingBuffer::new(MemoryRingStorage::default());
    let shows = |d: &Rc<RefCell<MockDisplay>>| {
        let d = d.borrow();
        let l = &d.last;
        format!(
            "{}|{}|{}|{}|{}",
            l.band1, l.band1_right, l.band2_line1, l.band2_line2, l.band3
        )
        .contains("Smith House")
    };
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
        let mut i = client_rc.borrow_mut();
        i.next_pair = HttpCallResult {
            transport_ok: true,
            http_status: 201,
            body: String::new(),
        };
        i.pair_body.device_token = "sbd_new".to_owned();
        i.pair_body.household_name = "Smith House".to_owned();
        i.pair_body.config.commit_mode = "queue".to_owned();
    }
    controller.on_barcode_line("SBP-0123-ABCD-G", true, 1000);
    assert_eq!(controller.operational_state(), OperationalState::Paired);
    controller.tick(1100);
    assert!(shows(&display_rc));
    // a scan buffered offline, then an opaque flush 401 re-renders the screen
    opaque_401(&client_rc);
    controller.on_barcode_line("4011", false, 1200); // inline opaque 401, scan kept
    controller.tick(2300);
    assert_eq!(controller.operational_state(), OperationalState::Paired);
    assert!(shows(&display_rc));
}
