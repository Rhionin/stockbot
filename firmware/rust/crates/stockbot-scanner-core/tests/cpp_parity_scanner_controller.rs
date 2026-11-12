//! C++ `firmware/test/test_scanner_core/test_main.cpp` controller coverage map (52 tests):
//!
//! | C++ test | Rust coverage |
//! |----------|----------------|
//! | test_barcode_filter | `scanner_state` / barcode unit tests in lib |
//! | test_duplicate_cooldown | lib `duplicate_cooldown` tests |
//! | test_scanner_state | `cpp_parity_scanner_state` |
//! | test_ring_buffer | `ring_buffer` unit tests |
//! | test_display_model | `cpp_parity_display_model` |
//! | test_display_band2_text | `display_model_vectors` |
//! | test_protocol_json | `device_protocol_vectors` |
//! | test_protocol_json_int_parse_nonthrowing | `device_protocol_vectors` |
//! | test_hid_keycode | `hid_keycode` tests |
//! | test_scanner_controller_flow | `test_scanner_controller_flow` |
//! | test_portal_submission_handoff | `test_portal_submission_handoff` |
//! | test_trigger_button_wiring | `test_trigger_button_wiring` |
//! | test_offline_fifo_flush_order | `test_offline_fifo_flush_order` |
//! | test_client_scan_id_unique_across_reboot | `test_client_scan_id_unique_across_reboot` |
//! | test_offline_buffer_full_refuses_without_eviction | scenario `offline_buffer_full_refuse` |
//! | test_offline_buffer_full_clears_after_flush_ack | `test_offline_buffer_full_clears_after_flush_ack` |
//! | test_offline_buffer_full_persists_across_reboot_until_flush | `test_offline_buffer_full_persists_across_reboot_until_flush` |
//! | test_heartbeat_dropped_count_zero | scenario `heartbeat_60s_interval` |
//! | test_offline_buffer_refusal_buzz_immediately_after_boot | scenario `offline_buffer_full_refuse` |
//! | test_offline_buffer_control_barcodes_refused_while_full | `test_offline_buffer_control_barcodes_refused_while_full` |
//! | test_offline_buffer_refusal_feedback_rate_limited | `test_offline_buffer_refusal_feedback_rate_limited` |
//! | test_offline_buffer_refusal_rate_limit_boundary | `test_offline_buffer_refusal_rate_limit_boundary` |
//! | test_offline_buffer_refused_transient_timeout | `test_offline_buffer_refused_transient_timeout` |
//! | test_offline_buffer_refused_transient_restarts | `test_offline_buffer_refused_transient_restarts` |
//! | test_repair_after_disconnect | `test_repair_after_disconnect` |
//! | test_ap_suffix_from_mac | `test_ap_suffix_from_mac` |
//! | test_wifi_button_hold_feedback | `test_wifi_button_hold_feedback` |
//! | test_pairing_code_expired_screen | scenario `pairing_expired_transient` |
//! | test_pairing_cant_connect_screen | `test_pairing_cant_connect_screen` |
//! | test_disconnect_clears_persisted_ring | `test_disconnect_clears_persisted_ring` |
//! | test_pairing_rate_limited_and_rejected_screens | `test_pairing_rate_limited_and_rejected_screens` |
//! | test_pairing_error_transient_returns_to_pairing | `test_pairing_error_transient_returns_to_pairing` |
//! | test_device_revoked_flush_and_heartbeat_401 | `test_device_revoked_flush_and_heartbeat_401` |
//! | test_finish_pairing_clears_ring_before_token | `test_finish_pairing_clears_ring_before_token` |
//! | test_wifi_button_hold_tick_at_5000_boundary | `test_wifi_button_hold_tick_at_5000_boundary` |
//! | test_protocol_json_pairing_failures | `test_protocol_json_pairing_failures` |
//! | test_pairing_failure_display_copy_and_colors | `cpp_parity_display_model` |
//! | test_pairing_rate_limited_status_and_body_variants | `test_pairing_rate_limited_status_and_body_variants` |
//! | test_pairing_transient_screens_return_to_pairing | `test_pairing_transient_screens_return_to_pairing` |
//! | test_finish_pairing_fails_closed_when_ring_clear_fails | `test_finish_pairing_fails_closed_when_ring_clear_fails` |
//! | test_disconnect_clears_config_device_token | `test_disconnect_clears_config_device_token` |
//! | test_factory_reset_wipes_nvs_wifi_token_and_ring | scenario `factory_reset_from_hold` |
//! | test_mode_button_hold_poll_renders_once_per_phase | `test_mode_button_hold_poll_renders_once_per_phase` |
//! | test_scanner_setup_network_and_portal_copy | setup portal copy tests |
//! | test_display_all_screens_within_band_limits | `display_model_vectors` |
//! | test_firmware_strings_no_unicode_ellipsis | display vectors |
//! | test_button_hold_tracker | `button_hold` / millis wrap |
//! | test_wifi_button_hold_no_erase_while_held | `test_wifi_button_hold_no_erase_while_held` |
//! | test_wifi_button_hold_release_at_9999_no_factory_reset | `test_wifi_button_hold_release_at_9999_no_factory_reset` |
//! | test_display_strings_printable_ascii | display tests |
//! | test_docked_band1_right_no_charging_suffix | scenario `dock_vbus_no_battery` |
//! | test_ota_install_gate_placeholder_until_firmware_452 | documented placeholder |

mod common;

use std::cell::RefCell;
use std::rc::Rc;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
    SharedMockClient, SharedMockDisplay, SharedMockFeedback, SharedMockWifi,
};
use common::memory_ring_storage::{FailClearRingStorage, MemoryRingStorage};
use stockbot_scanner_core::DeviceClient;
use stockbot_scanner_core::{
    ap_suffix_from_mac_address, c_str_from_fixed, pairing_complete_failure_is_client_rejected,
    pairing_complete_failure_is_expired, pairing_complete_failure_is_rate_limited, AudioCue,
    CommitMode, Direction, HttpCallResult, OperationalState, PairCompleteParsed,
    ParsedDeviceScanResponse, RingBuffer, ScannerController, UiScreen, RING_CAPACITY,
};

#[test]
fn test_ap_suffix_from_mac() {
    assert_eq!(ap_suffix_from_mac_address("AA:BB:CC:DD:EE:FF"), "EEFF");
    assert_eq!(ap_suffix_from_mac_address("aa-bb-cc-dd-ee-ff"), "EEFF");

    let suffix_a = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
        let mut scanner = MockScanner::default();
        let mut display = MockDisplay::default();
        let mut feedback = MockFeedback::default();
        let mut power = MockPower::default();
        let mut nvs = MockNvs::default();
        let mut portal = MockPortal::default();
        let mut client = MockClient::default();
        let mut wifi = MockWifi::default();
        let mut a = ScannerController::new(
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
        a.begin(0, 0, "AA:BB:CC:DD:11:22");
        a.ap_suffix_for_test().to_owned()
    };

    let suffix_b = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
        let mut scanner = MockScanner::default();
        let mut display = MockDisplay::default();
        let mut feedback = MockFeedback::default();
        let mut power = MockPower::default();
        let mut nvs = MockNvs::default();
        let mut portal = MockPortal::default();
        let mut client = MockClient::default();
        let mut wifi = MockWifi::default();
        let mut b = ScannerController::new(
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
        b.begin(0, 0, "AA:BB:CC:DD:33:44");
        b.ap_suffix_for_test().to_owned()
    };

    assert_eq!(suffix_a, "1122");
    assert_eq!(suffix_b, "3344");
    assert_ne!(suffix_a, suffix_b);
}

#[test]
fn test_scanner_controller_flow() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

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
        controller.begin(1000, 0, "");
        assert_eq!(
            controller.operational_state(),
            OperationalState::Unprovisioned
        );
    }

    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.server_url = "https://stockbot.test".to_owned();
    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 201,
        body: String::new(),
    };
    client.pair_body = PairCompleteParsed::default();
    client.pair_body.device_token = "sbd_abc".to_owned();
    client.pair_body.config.commit_mode = "queue".to_owned();

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
        controller.begin(2000, 0, "");
        assert_eq!(controller.operational_state(), OperationalState::Pairing);
        controller.on_barcode_line("SBP-0123-ABCD-G", true, 3000);
        assert_eq!(controller.operational_state(), OperationalState::Paired);
    }
    assert!(scanner.one_d_enabled);

    client.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 200,
        body: String::new(),
    };
    client.scan_body = ParsedDeviceScanResponse::default();
    client.scan_body.status = "created".to_owned();
    client.scan_body.scan_status = "queued".to_owned();
    client.scan_body.known = true;
    client.scan_body.product_name = "Milk".to_owned();
    client.scan_body.product_name_is_null = false;
    client.scan_body.on_hand = 6;
    client.scan_body.parse_ok = true;
    wifi.connected = true;

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
        controller.begin(2000, 0, "");
        controller.tick(3500);
        controller.on_barcode_line("4011", true, 4000);
        assert!(display.last.band3.contains("Queued -1"));
    }

    nvs.cfg.device_token = "sbd_abc".to_owned();
    let storage2 = MemoryRingStorage::default();
    let mut ring2 = RingBuffer::new(storage2);
    wifi.connected = true;
    client.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 401,
        body: String::new(),
    };
    client.scan_body = ParsedDeviceScanResponse::default();
    client.scan_body.unauthorized = true;
    client.scan_body.parse_ok = true;

    {
        let mut revoked = ScannerController::new(
            &mut scanner,
            &mut display,
            &mut feedback,
            &mut power,
            &mut nvs,
            &mut portal,
            &mut client,
            &mut wifi,
            &mut ring2,
        );
        revoked.begin(5000, 0, "");
        revoked.tick(5050);
        revoked.on_barcode_line("4012", true, 5100);
        assert_eq!(revoked.operational_state(), OperationalState::Paired);
        assert_eq!(revoked.ui_screen(), UiScreen::S7HardError);
        assert_eq!(display.last.band1, "SCANNER ALERT");
        assert_eq!(ring2.count(), 0);
    }

    let storage3 = MemoryRingStorage::default();
    let mut ring3 = RingBuffer::new(storage3);
    {
        let mut mode = ScannerController::new(
            &mut scanner,
            &mut display,
            &mut feedback,
            &mut power,
            &mut nvs,
            &mut portal,
            &mut client,
            &mut wifi,
            &mut ring3,
        );
        mode.begin(6000, 0, "");
        mode.on_mode_button_short_press(6100);
        assert_eq!(mode.active_direction(), Direction::StockIn);
    }

    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_abc".to_owned();
    client.next_hb = HttpCallResult {
        transport_ok: true,
        http_status: 200,
        body: r#"{"config":{"defaultDirection":"stock_in","configVersion":99}}"#.to_owned(),
    };
    client.hb_body.parse_ok = true;
    client.hb_body.config.default_direction = "stock_in".to_owned();
    client.hb_body.config.config_version = 99;

    let storage4 = MemoryRingStorage::default();
    let mut ring4 = RingBuffer::new(storage4);
    {
        let mut hb_ctl = ScannerController::new(
            &mut scanner,
            &mut display,
            &mut feedback,
            &mut power,
            &mut nvs,
            &mut portal,
            &mut client,
            &mut wifi,
            &mut ring4,
        );
        hb_ctl.begin(0, 0, "");
        hb_ctl.tick(70_000);
        assert_eq!(nvs.cfg.config_version, 99);
    }
}

#[test]
fn test_portal_submission_handoff() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();

    let portal_running_after_begin = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
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
        controller.begin(1000, 0, "");
        assert_eq!(
            controller.operational_state(),
            OperationalState::Unprovisioned
        );
        portal.running
    };
    assert!(portal_running_after_begin);

    portal.pending.ready = true;
    portal.pending.wifi_ssid = "PantryWiFi".to_owned();
    portal.pending.wifi_pass = "secret".to_owned();
    portal.pending.server_url = "https://stockbot.test".to_owned();

    let op_after_portal = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
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
        controller.begin(1000, 0, "");
        controller.tick(2000);
        controller.operational_state()
    };

    assert_eq!(nvs.cfg.wifi_ssid, "PantryWiFi");
    assert_eq!(wifi.last_ssid, "PantryWiFi");
    assert_eq!(op_after_portal, OperationalState::Pairing);
    assert!(!portal.running);

    nvs.cfg.wifi_ssid = "PantryWiFi".to_owned();
    nvs.cfg.device_token = "sbd_preserve".to_owned();
    portal.pending.ready = true;
    portal.pending.wifi_ssid = "NewWiFi".to_owned();
    portal.pending.wifi_pass = "newsecret".to_owned();

    {
        let storage2 = MemoryRingStorage::default();
        let mut ring2 = RingBuffer::new(storage2);
        let mut update = ScannerController::new(
            &mut scanner,
            &mut display,
            &mut feedback,
            &mut power,
            &mut nvs,
            &mut portal,
            &mut client,
            &mut wifi,
            &mut ring2,
        );
        update.begin(3000, 0, "");
        assert_eq!(update.operational_state(), OperationalState::Paired);
        update.on_mode_button_hold(5500, 6000);
        update.tick(7000);
        assert_eq!(update.operational_state(), OperationalState::Paired);
    }
    assert!(!portal.running);

    assert_eq!(nvs.cfg.device_token, "sbd_preserve");
    assert_eq!(nvs.cfg.wifi_ssid, "NewWiFi");
}

#[test]
fn test_trigger_button_wiring() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    power.status.vbus_present = false;
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

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
        controller.begin(1000, 0, "");

        controller.on_trigger_button_changed(true, 2000);
        assert!(controller.manual_retrigger_pending());
        assert!(controller.trigger_continuous_mode());
        controller.on_trigger_button_changed(false, 2100);
    }
    assert!(!scanner.continuous);
}

#[test]
fn test_protocol_json_pairing_failures() {
    assert!(pairing_complete_failure_is_expired(
        true,
        404,
        r#"{"code":"not_found"}"#
    ));
    assert!(!pairing_complete_failure_is_expired(
        true,
        403,
        r#"{"code":"not_found"}"#
    ));
    assert!(!pairing_complete_failure_is_expired(
        true,
        404,
        r#"{"code":"busy"}"#
    ));

    assert!(pairing_complete_failure_is_rate_limited(
        true,
        429,
        r#"{"code":"busy"}"#
    ));
    assert!(pairing_complete_failure_is_rate_limited(
        true,
        403,
        r#"{"code":"rate_limited"}"#
    ));
    assert!(!pairing_complete_failure_is_rate_limited(
        true,
        403,
        r#"{"code":"busy"}"#
    ));

    assert!(pairing_complete_failure_is_client_rejected(
        true,
        410,
        r#"{"code":"reset_expired"}"#
    ));
    assert!(pairing_complete_failure_is_client_rejected(
        true,
        404,
        r#"{"code":"busy"}"#
    ));
    assert!(!pairing_complete_failure_is_client_rejected(
        true,
        429,
        r#"{"code":"rate_limited"}"#
    ));
}

fn setup_paired_nvs(nvs: &mut MockNvs) {
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_offline".to_owned();
    nvs.cfg.commit_mode = CommitMode::Immediate;
}

#[test]
fn test_offline_fifo_flush_order() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    nvs.cfg.commit_mode = CommitMode::Immediate;

    client_rc.borrow_mut().next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
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
    controller.on_barcode_line("011111111111", false, 1000);
    controller.on_barcode_line("022222222222", false, 2000);

    let id_a = "s-1-1-1000";
    let id_b = "s-1-2-2000";
    wifi_rc.borrow_mut().connected = true;
    let batch_body = format!(
        r#"{{"results":[{{"id":"{id_a}","status":"created"}},{{"id":"{id_b}","status":"created"}}]}}"#
    );
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 200,
            body: batch_body,
        };
        inner.scan_body.status = "created".to_owned();
    }
    controller.tick(5000);

    let inner = client_rc.borrow();
    assert_eq!(inner.last_flush_batch.len(), 2);
    assert_eq!(
        c_str_from_fixed(&inner.last_flush_batch[0].barcode),
        "011111111111"
    );
    assert_eq!(
        c_str_from_fixed(&inner.last_flush_batch[1].barcode),
        "022222222222"
    );
    assert!(inner.last_flush_body.contains("\"ageMs\":4000"));
    assert!(inner.last_flush_body.contains("\"ageMs\":3000"));
    drop(controller);
    assert_eq!(ring.count(), 0);
}

#[test]
fn test_client_scan_id_unique_across_reboot() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi {
        connected: true,
        ..MockWifi::default()
    };
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_test".to_owned();

    client.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 200,
        body: r#"{"id":"x","status":"created","scanStatus":"committed","known":true}"#.to_owned(),
    };
    client.scan_body.status = "created".to_owned();
    client.scan_body.scan_status = "committed".to_owned();
    client.scan_body.known = true;
    client.scan_body.parse_ok = true;

    let id_first = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
        let mut first = ScannerController::new(
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
        first.begin(100, 0, "");
        first.tick(150);
        first.on_barcode_line("4011", false, 500);
        let id = client.last_send_scan_id.clone();
        assert!(!id.is_empty());
        assert_eq!(ring.count(), 0);
        id
    };

    let id_second = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
        let mut second = ScannerController::new(
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
        second.begin(100, 0, "");
        second.tick(150);
        second.on_barcode_line("4011", false, 500);
        client.last_send_scan_id.clone()
    };

    assert!(!id_second.is_empty());
    assert_ne!(id_first, id_second);
}

#[test]
fn test_offline_buffer_full_clears_after_flush_ack() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

    setup_paired_nvs(&mut nvs);
    client_rc.borrow_mut().next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
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
    controller.tick(10);
    for i in 0..RING_CAPACITY {
        let bc = format!("{:012}", i + 1);
        controller.on_barcode_line(&bc, false, 1000 + i as u64);
    }

    drop(controller);
    let pending = ring.peek_batch(50);
    assert_eq!(pending.len(), 50);
    let mut batch_body = String::from(r#"{"results":["#);
    for (i, rec) in pending.iter().enumerate() {
        if i > 0 {
            batch_body.push(',');
        }
        let id = c_str_from_fixed(&rec.client_scan_id);
        batch_body.push_str(&format!(r#"{{"id":"{id}","status":"created"}}"#));
    }
    batch_body.push_str("]}");
    wifi_rc.borrow_mut().connected = true;
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 200,
            body: batch_body,
        };
        inner.scan_body.status = "created".to_owned();
    }

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
    controller.tick(20_000);
    assert_ne!(controller.ui_screen(), UiScreen::S7HardError);

    client_rc.borrow_mut().next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    controller.on_barcode_line("4011", false, 21_000);
    drop(controller);

    assert!(!ring.is_full());
    assert_eq!(ring.count(), RING_CAPACITY - 49);
}

#[test]
fn test_disconnect_clears_persisted_ring() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();

    client.next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    let storage = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
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
        controller.on_barcode_line("4011", false, 1000);
        assert_eq!(ring.count(), 1);
        ring.into_storage()
    };
    assert!(!storage.records.is_empty());

    wifi.connected = true;
    client.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 401,
        body: String::new(),
    };
    client.scan_body.unauthorized = true;
    client.scan_body.parse_ok = true;

    let storage = {
        let mut ring = RingBuffer::new(storage);
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
        controller.tick(1500);
        controller.on_barcode_line("4012", false, 2000);
        assert_eq!(ring.count(), 0);
        ring.into_storage()
    };
    assert!(storage.records.is_empty());
}

#[test]
fn test_device_revoked_flush_and_heartbeat_401() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();

    client.next_scan = HttpCallResult {
        transport_ok: true,
        http_status: 401,
        body: r#"{"code":"device_revoked"}"#.to_owned(),
    };
    client.scan_body.unauthorized = true;
    client.scan_body.parse_ok = true;
    wifi.connected = true;

    {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
        let _ = ring.enqueue("4011", Direction::StockOut, 1100, 1);
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
        controller.begin(1000, 0, "");
        controller.tick(1200);
        assert_eq!(controller.ui_screen(), UiScreen::S7HardError);
    }
    assert!(nvs.cfg.device_token.is_empty());

    nvs.cfg.device_token = "sbd_y".to_owned();
    stockbot_scanner_core::DeviceClient::set_device_token(&mut client, "sbd_y");
    client.next_hb = HttpCallResult {
        transport_ok: true,
        http_status: 401,
        body: r#"{"code":"device_revoked"}"#.to_owned(),
    };
    wifi.connected = true;

    {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
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
        controller.begin(5000, 0, "");
        controller.tick(70_000);
        assert_eq!(controller.ui_screen(), UiScreen::S7HardError);
    }
    assert!(nvs.cfg.device_token.is_empty());
}

#[test]
fn test_mode_button_hold_poll_renders_once_per_phase() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

    let render_calls = display.render_calls.clone();
    render_calls.set(0);
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
    controller.begin(1000, 0, "");
    controller.on_mode_button_down(2000);
    controller.on_mode_button_hold_poll(7600);
    let after_wifi_phase = render_calls.get();
    assert!(after_wifi_phase >= 1);
    controller.on_mode_button_hold_poll(8100);
    controller.on_mode_button_hold_poll(8600);
    assert_eq!(render_calls.get(), after_wifi_phase);
    controller.on_mode_button_hold_poll(12600);
    assert!(render_calls.get() > after_wifi_phase);
}

#[test]
fn test_wifi_button_hold_release_at_9999_no_factory_reset() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

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
    controller.begin(1000, 0, "");
    controller.on_mode_button_down(9000);
    controller.on_mode_button_hold_poll(18999);
    controller.on_mode_button_up(18999);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiSetup);
    assert_eq!(nvs.cfg.device_token, "sbd_x");
}

fn barcode_at(index: usize) -> String {
    format!("{:012}", index + 1)
}

fn triple_error_buzz_count(feedback: &MockFeedback) -> usize {
    feedback
        .cues
        .iter()
        .filter(|c| **c == AudioCue::TripleErrorBuzz)
        .count()
}

fn triple_error_buzz_count_rc(feedback: &RefCell<MockFeedback>) -> usize {
    triple_error_buzz_count(&feedback.borrow())
}

fn expect_memory_full_idle_screen(display: &MockDisplay) {
    assert_eq!(display.last.band1, "MEMORY FULL");
    assert_eq!(
        display.last.band2_line1,
        format!("{RING_CAPACITY} scans waiting")
    );
    assert_eq!(display.last.band2_line2, "Scans won't save");
    assert_eq!(display.last.band3, "Connect Wi-Fi to send");
    assert_eq!(display.last.audio, AudioCue::Silent);
}

fn expect_buffer_refused_screen(display: &MockDisplay, barcode_line: &str) {
    assert_eq!(display.last.band1, "NOT SAVED");
    assert_eq!(display.last.band2_line1, "Memory is full");
    assert_eq!(display.last.band2_line2, barcode_line);
    assert_eq!(display.last.band3, "Connect Wi-Fi to send");
    assert_eq!(display.last.audio, AudioCue::Silent);
}

fn fill_ring_to_capacity(controller: &mut ScannerController<'_, MemoryRingStorage>, start_ms: u64) {
    for i in 0..RING_CAPACITY {
        controller.on_barcode_line(&barcode_at(i), false, start_ms + i as u64);
    }
}

#[test]
fn test_offline_buffer_full_persists_across_reboot_until_flush() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());

    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_persist".to_owned();
    client_rc.borrow_mut().next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };

    let storage = {
        let storage = MemoryRingStorage::default();
        let mut ring = RingBuffer::new(storage);
        let mut first = ScannerController::new(
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
        first.begin(0, 0, "");
        for i in 0..RING_CAPACITY {
            first.on_barcode_line(&barcode_at(i), false, 1000 + i as u64);
        }
        assert!(ring.is_full());
        ring.into_storage()
    };

    let mut ring_b = RingBuffer::new(storage);
    let mut second = ScannerController::new(
        &mut scanner,
        &mut display,
        &mut feedback,
        &mut power,
        &mut nvs,
        &mut portal,
        &mut client,
        &mut wifi,
        &mut ring_b,
    );
    second.begin(50_000, 0, "");
    second.tick(50_010);
    let reboot_screen = second.ui_screen();
    drop(second);
    assert!(ring_b.is_full());
    assert_eq!(reboot_screen, UiScreen::S7HardError);

    let pending = ring_b.peek_batch(20);
    let mut batch_body = String::from(r#"{"results":["#);
    for (i, rec) in pending.iter().enumerate() {
        if i > 0 {
            batch_body.push(',');
        }
        let id = c_str_from_fixed(&rec.client_scan_id);
        batch_body.push_str(&format!(r#"{{"id":"{id}","status":"created"}}"#));
    }
    batch_body.push_str("]}");
    wifi_rc.borrow_mut().connected = true;
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 200,
            body: batch_body,
        };
        inner.scan_body.status = "created".to_owned();
    }
    let mut second = ScannerController::new(
        &mut scanner,
        &mut display,
        &mut feedback,
        &mut power,
        &mut nvs,
        &mut portal,
        &mut client,
        &mut wifi,
        &mut ring_b,
    );
    second.begin(50_000, 0, "");
    second.tick(60_000);
    assert!(!ring_b.is_full());
}

#[test]
fn test_offline_buffer_control_barcodes_refused_while_full() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let feedback_rc = Rc::new(RefCell::new(MockFeedback::default()));
    let mut feedback = SharedMockFeedback(feedback_rc.clone());
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    setup_paired_nvs(&mut nvs);
    client.next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
        controller.tick(10);
        fill_ring_to_capacity(&mut controller, 1000);
        controller.tick(8000);
    }
    feedback_rc.borrow_mut().cues.clear();
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
        controller.tick(8000);
        controller.on_barcode_line("STOCK-IN", false, 9000);
        assert_eq!(triple_error_buzz_count_rc(&feedback_rc), 1);
        assert_eq!(controller.ui_screen(), UiScreen::S7OfflineBufferRefused);

        controller.on_barcode_line("STOCK-OUT", false, 9100);
        assert_eq!(triple_error_buzz_count_rc(&feedback_rc), 1);
        let ui = controller.ui_screen();
        drop(controller);
        assert_eq!(ui, UiScreen::S7OfflineBufferRefused);
        expect_buffer_refused_screen(&display, "STOCK-OUT");
    }
}

#[test]
fn test_offline_buffer_refusal_feedback_rate_limited() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    setup_paired_nvs(&mut nvs);
    client.next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
        controller.tick(10);
        fill_ring_to_capacity(&mut controller, 1000);
    }
    feedback.cues.clear();
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
        controller.tick(10);
        controller.on_barcode_line("999999999999", false, 5000);
        controller.on_barcode_line("888888888888", false, 5200);
        controller.on_barcode_line("777777777777", false, 7000);
        let ui = controller.ui_screen();
        drop(controller);
        assert_eq!(triple_error_buzz_count(&feedback), 2);
        assert_eq!(ui, UiScreen::S7OfflineBufferRefused);
        expect_buffer_refused_screen(&display, "777777777777");
    }
}

#[test]
fn test_offline_buffer_refusal_rate_limit_boundary() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let feedback_rc = Rc::new(RefCell::new(MockFeedback::default()));
    let mut feedback = SharedMockFeedback(feedback_rc.clone());
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    setup_paired_nvs(&mut nvs);
    client.next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
        controller.tick(10);
        fill_ring_to_capacity(&mut controller, 1000);
        controller.tick(8000);
    }
    feedback_rc.borrow_mut().cues.clear();
    let t0 = 10_000_u64;
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
    controller.tick(8000);
    controller.on_barcode_line("999999999999", false, t0);
    assert_eq!(triple_error_buzz_count_rc(&feedback_rc), 1);
    controller.on_barcode_line("888888888888", false, t0 + 1499);
    assert_eq!(triple_error_buzz_count_rc(&feedback_rc), 1);
    controller.on_barcode_line("777777777777", false, t0 + 1500);
    assert_eq!(triple_error_buzz_count_rc(&feedback_rc), 2);
}

#[test]
fn test_offline_buffer_refused_transient_timeout() {
    let mut scanner = MockScanner::default();
    let display_rc = Rc::new(RefCell::new(MockDisplay::default()));
    let mut display = SharedMockDisplay(display_rc.clone());
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    setup_paired_nvs(&mut nvs);
    client.next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.tick(10);
    fill_ring_to_capacity(&mut controller, 1000);
    controller.tick(8000);
    controller.on_barcode_line("999999999999", false, 10_000);
    assert_eq!(controller.ui_screen(), UiScreen::S7OfflineBufferRefused);
    expect_buffer_refused_screen(&display_rc.borrow(), "999999999999");
    controller.tick(13_999);
    assert_eq!(controller.ui_screen(), UiScreen::S7OfflineBufferRefused);
    controller.tick(14_000);
    assert_eq!(controller.ui_screen(), UiScreen::S7HardError);
    drop(controller);
    expect_memory_full_idle_screen(&display_rc.borrow());
}

#[test]
fn test_offline_buffer_refused_transient_restarts() {
    let mut scanner = MockScanner::default();
    let display_rc = Rc::new(RefCell::new(MockDisplay::default()));
    let mut display = SharedMockDisplay(display_rc.clone());
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    setup_paired_nvs(&mut nvs);
    client.next_scan = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.tick(10);
    fill_ring_to_capacity(&mut controller, 1000);
    controller.tick(8000);
    controller.on_barcode_line("999999999999", false, 10_000);
    controller.on_barcode_line("888888888888", false, 12_000);
    assert_eq!(controller.ui_screen(), UiScreen::S7OfflineBufferRefused);
    expect_buffer_refused_screen(&display_rc.borrow(), "888888888888");
    controller.tick(15_999);
    assert_eq!(controller.ui_screen(), UiScreen::S7OfflineBufferRefused);
    controller.tick(16_000);
    assert_eq!(controller.ui_screen(), UiScreen::S7HardError);
    drop(controller);
    expect_memory_full_idle_screen(&display_rc.borrow());
}

#[test]
fn test_repair_after_disconnect() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let feedback_rc = Rc::new(RefCell::new(MockFeedback::default()));
    let mut feedback = SharedMockFeedback(feedback_rc.clone());
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_abc".to_owned();
    client_rc.borrow_mut().set_device_token("sbd_abc");
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.begin(1000, 0, "");
    wifi_rc.borrow_mut().connected = true;
    controller.tick(1100);
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 401,
            body: String::new(),
        };
        inner.scan_body.unauthorized = true;
        inner.scan_body.parse_ok = true;
    }
    feedback_rc.borrow_mut().cues.clear();
    controller.on_barcode_line("4011", false, 2000);
    assert_eq!(controller.operational_state(), OperationalState::Paired);
    assert_eq!(controller.ui_screen(), UiScreen::S7HardError);
    drop(controller);
    assert_eq!(display.last.band1, "SCANNER ALERT");
    assert_eq!(display.last.band2_line1, "Disconnected");
    let last_cue = feedback_rc.borrow().cues.last().copied();
    assert_eq!(last_cue, Some(AudioCue::TripleErrorBuzz));
    assert!(nvs.cfg.device_token.is_empty());
    assert!(client_rc.borrow().token.is_empty());

    {
        let mut inner = client_rc.borrow_mut();
        inner.next_pair = HttpCallResult {
            transport_ok: true,
            http_status: 201,
            body: String::new(),
        };
        inner.pair_body = PairCompleteParsed::default();
        inner.pair_body.device_token = "sbd_new".to_owned();
        inner.pair_body.household_name = "Test Household".to_owned();
    }
    feedback_rc.borrow_mut().cues.clear();
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
    controller.begin(1000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 3000);
    assert_eq!(controller.operational_state(), OperationalState::Paired);
    drop(controller);
    assert_eq!(display.last.band1, "CONNECTED");
    assert_eq!(display.last.band2_line1, "Test Household");
    assert_eq!(
        feedback_rc.borrow().cues.last(),
        Some(&AudioCue::AscendingPairSuccess)
    );
}

#[test]
fn test_wifi_button_hold_feedback() {
    let mut scanner = MockScanner::default();
    let display_rc = Rc::new(RefCell::new(MockDisplay::default()));
    let mut display = SharedMockDisplay(display_rc.clone());
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.begin(1000, 0, "");
    controller.on_mode_button_hold_tick(5500, 2000);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiButtonHold);
    let bands = display_rc.borrow().last.clone();
    assert_eq!(bands.band1, "HOLDING");
    assert_eq!(bands.band2_line1, "Let go now to");
    assert_eq!(bands.band2_line2, "update Wi-Fi");
    assert_eq!(bands.band3, "Keep holding to erase everything");

    controller.on_mode_button_hold_tick(9999, 2100);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiButtonHold);

    controller.on_mode_button_hold_tick(10_000, 2200);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiButtonHoldErase);
    assert_eq!(display_rc.borrow().last.band1, "LET GO TO ERASE");

    controller.on_mode_button_hold(5500, 2300);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiSetup);
    drop(controller);
    assert_eq!(display_rc.borrow().last.band1, "WI-FI SETUP");
    assert_eq!(display_rc.borrow().last.band2_line2, "Scanner-Setup-0000");

    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut erase_ctl = ScannerController::new(
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
    erase_ctl.begin(3000, 0, "");
    erase_ctl.on_mode_button_hold_tick(10_000, 4000);
    assert_eq!(erase_ctl.ui_screen(), UiScreen::SWifiButtonHoldErase);
    erase_ctl.on_mode_button_hold(10_000, 4100);
    assert_eq!(
        erase_ctl.operational_state(),
        OperationalState::Unprovisioned
    );
}

#[test]
fn test_pairing_cant_connect_screen() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

    client.next_pair = HttpCallResult {
        transport_ok: false,
        http_status: 0,
        body: String::new(),
    };
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
        controller.begin(1000, 0, "");
        controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
        assert_eq!(controller.ui_screen(), UiScreen::SPairingCantConnect);
    }
    assert_eq!(display.last.band1, "CAN'T CONNECT");
    assert_eq!(display.last.band2_line1, "Couldn't reach");
    assert_eq!(display.last.band2_line2, "the server");
    assert_eq!(display.last.band3, "Check Wi-Fi, then scan again");

    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 503,
        body: r#"{"code":"busy","error":"busy"}"#.to_owned(),
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
    controller.begin(3000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 3100);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingCantConnect);
}

#[test]
fn test_pairing_rate_limited_and_rejected_screens() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let feedback_rc = Rc::new(RefCell::new(MockFeedback::default()));
    let mut feedback = SharedMockFeedback(feedback_rc.clone());
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);

    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 429,
        body: r#"{"code":"rate_limited"}"#.to_owned(),
    };
    feedback_rc.borrow_mut().cues.clear();
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
        controller.begin(1000, 0, "");
        controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
        assert_eq!(controller.ui_screen(), UiScreen::SPairingTooManyTries);
    }
    assert_eq!(display.last.band1, "TOO MANY TRIES");
    assert_eq!(
        feedback_rc.borrow().cues.last(),
        Some(&AudioCue::ScanBeepLowTick)
    );

    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 400,
        body: r#"{"code":"validation"}"#.to_owned(),
    };
    feedback_rc.borrow_mut().cues.clear();
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
        controller.begin(3000, 0, "");
        controller.on_barcode_line("SBP-0123-ABCD-G", false, 3100);
        assert_eq!(controller.ui_screen(), UiScreen::SPairingCodeNotAccepted);
    }
    assert_eq!(display.last.band1, "CODE NOT ACCEPTED");
    assert_eq!(
        feedback_rc.borrow().cues.last(),
        Some(&AudioCue::TripleErrorBuzz)
    );

    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 404,
        body: r#"{"code":"busy"}"#.to_owned(),
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
    controller.begin(4000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 4100);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingCodeNotAccepted);
}

#[test]
fn test_pairing_error_transient_returns_to_pairing() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 404,
        body: r#"{"code":"not_found"}"#.to_owned(),
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
    controller.begin(1000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingExpired);
    controller.tick(5999);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingExpired);
    controller.tick(6000);
    assert_eq!(controller.ui_screen(), UiScreen::S1Pairing);
}

#[test]
fn test_finish_pairing_clears_ring_before_token() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    let _ = ring.enqueue("4011", Direction::StockOut, 1100, 1);
    assert_eq!(ring.count(), 1);
    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 201,
        body: String::new(),
    };
    client.pair_body.device_token = "sbd_new".to_owned();
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
    controller.begin(1000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
    drop(controller);
    assert_eq!(ring.count(), 0);
    assert_eq!(nvs.cfg.device_token, "sbd_new");
}

#[test]
fn test_wifi_button_hold_tick_at_5000_boundary() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.begin(1000, 0, "");
    controller.on_mode_button_hold_tick(4999, 2000);
    assert_ne!(controller.ui_screen(), UiScreen::SWifiButtonHold);
    controller.on_mode_button_hold_tick(5000, 2100);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiButtonHold);
}

#[test]
fn test_pairing_rate_limited_status_and_body_variants() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 429,
        body: r#"{"code":"busy"}"#.to_owned(),
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
    controller.begin(1000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingTooManyTries);
    drop(controller);

    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 403,
        body: r#"{"code":"rate_limited"}"#.to_owned(),
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
    controller.begin(3000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 3100);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingTooManyTries);
}

#[test]
fn test_pairing_transient_screens_return_to_pairing() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 429,
        body: r#"{"code":"rate_limited"}"#.to_owned(),
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
    controller.begin(1000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingTooManyTries);
    controller.tick(5999);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingTooManyTries);
    controller.tick(6000);
    assert_eq!(controller.ui_screen(), UiScreen::S1Pairing);
    drop(controller);

    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 400,
        body: r#"{"code":"validation"}"#.to_owned(),
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
    controller.begin(7000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 7100);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingCodeNotAccepted);
    controller.tick(11_099);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingCodeNotAccepted);
    controller.tick(11_100);
    assert_eq!(controller.ui_screen(), UiScreen::S1Pairing);
}

#[test]
fn test_finish_pairing_fails_closed_when_ring_clear_fails() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    let storage = FailClearRingStorage {
        fail_empty_write: true,
        ..FailClearRingStorage::default()
    };
    let mut ring = RingBuffer::new(storage);
    let _ = ring.enqueue("4011", Direction::StockOut, 1100, 1);
    client.next_pair = HttpCallResult {
        transport_ok: true,
        http_status: 201,
        body: String::new(),
    };
    client.pair_body.device_token = "sbd_new".to_owned();
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
    controller.begin(1000, 0, "");
    controller.on_barcode_line("SBP-0123-ABCD-G", false, 2000);
    assert_eq!(controller.ui_screen(), UiScreen::SPairingCantConnect);
    drop(controller);
    assert!(nvs.cfg.device_token.is_empty());
    assert_eq!(ring.count(), 1);
}

#[test]
fn test_disconnect_clears_config_device_token() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    let mut portal = MockPortal::default();
    let client_rc = Rc::new(RefCell::new(MockClient::default()));
    let wifi_rc = Rc::new(RefCell::new(MockWifi::default()));
    let mut client = SharedMockClient(client_rc.clone());
    let mut wifi = SharedMockWifi(wifi_rc.clone());
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_abc".to_owned();
    client_rc.borrow_mut().set_device_token("sbd_abc");
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.begin(1000, 0, "");
    wifi_rc.borrow_mut().connected = true;
    controller.tick(1500);
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_scan = HttpCallResult {
            transport_ok: true,
            http_status: 401,
            body: r#"{"code":"device_revoked"}"#.to_owned(),
        };
        inner.scan_body.unauthorized = true;
        inner.scan_body.parse_ok = true;
    }
    controller.on_barcode_line("4011", false, 2000);
    assert!(client_rc.borrow().token.is_empty());
    client_rc.borrow_mut().send_heartbeat_calls = 0;
    {
        let mut inner = client_rc.borrow_mut();
        inner.next_hb = HttpCallResult {
            transport_ok: true,
            http_status: 200,
            body: r#"{"config":{"configVersion":1}}"#.to_owned(),
        };
        inner.hb_body.parse_ok = true;
    }
    controller.tick(70_000);
    assert_eq!(client_rc.borrow().send_heartbeat_calls, 0);
}

#[test]
fn test_wifi_button_hold_no_erase_while_held() {
    let mut scanner = MockScanner::default();
    let mut display = MockDisplay::default();
    let mut feedback = MockFeedback::default();
    let mut power = MockPower::default();
    let mut nvs = MockNvs::default();
    nvs.cfg.wifi_ssid = "Home".to_owned();
    nvs.cfg.device_token = "sbd_x".to_owned();
    let mut portal = MockPortal::default();
    let mut client = MockClient::default();
    let mut wifi = MockWifi::default();
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
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
    controller.begin(1000, 0, "");
    controller.on_mode_button_down(2000);
    controller.on_mode_button_hold_poll(12_000);
    assert_eq!(controller.ui_screen(), UiScreen::SWifiButtonHoldErase);
    assert_eq!(controller.operational_state(), OperationalState::Paired);
    controller.on_mode_button_hold_poll(50_000);
    assert_eq!(controller.operational_state(), OperationalState::Paired);
    controller.on_mode_button_up(50_000);
    assert_eq!(
        controller.operational_state(),
        OperationalState::Unprovisioned
    );
}
