mod common;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{
    ap_suffix_from_mac_address, pairing_complete_failure_is_client_rejected,
    pairing_complete_failure_is_expired, pairing_complete_failure_is_rate_limited, Direction,
    HttpCallResult, OperationalState, PairCompleteParsed, ParsedDeviceScanResponse, RingBuffer,
    ScannerController, UiScreen,
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
