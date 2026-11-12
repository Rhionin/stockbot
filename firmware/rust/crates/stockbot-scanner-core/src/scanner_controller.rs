//! Scanner orchestration (`firmware/src/core/scanner_controller.cpp`).

use crate::barcode::{
    accept_with_duplicate_cooldown, classify_barcode, is_pairing_code, BarcodeKind, Direction,
    DuplicateCooldownState, OperationalState,
};
use crate::button_hold_tracker::{ButtonHoldTracker, ModeButtonHoldPhase, ModeButtonReleaseAction};
use crate::display_model::{
    apply_display_band_colors, build_display_model, AudioCue, DisplayModelInput, ScanResultView,
    ScanStatusKind, UiScreen,
};
use crate::nvs_config::NvsConfig;
use crate::protocol_json::{
    http_failure_is_retryable, pairing_complete_failure_is_client_rejected,
    pairing_complete_failure_is_expired, pairing_complete_failure_is_rate_limited,
    parse_device_scan_batch_response_json, parse_setup_qr_payload, DeviceHubConfigParsed,
    PairCompleteParsed,
};
use crate::ring_buffer::{c_str_from_fixed, ring_scan_age_ms, RingBuffer, RingStorage};
use crate::scanner_ports::{
    CaptivePortalHal, CaptivePortalSubmission, DeviceClient, DisplayHal, FeedbackHal, NvsStore,
    PowerHal, ScannerHal, WifiStationHal,
};
use crate::scanner_state::{ButtonHoldGesture, CommitMode, ModeChime, ScannerState};
use crate::setup_network::ap_suffix_from_mac_address;
use crate::text::{normalize_scan, trim_ascii};

const HUB_VERSION: &str = "1.0.0-esp32";
const HEARTBEAT_INTERVAL_MS: u64 = 60_000;
const TRANSIENT_SCAN_MS: u64 = 4_000;
const TRANSIENT_LONG_MS: u64 = 5_000;
const DOCK_BANNER_MS: u64 = 2_000;
const BUFFER_FULL_FEEDBACK_MIN_INTERVAL_MS: u64 = 1_500;
const BUFFER_REFUSAL_TRANSIENT_MS: u64 = 4_000;

fn parse_scan_status_kind(scan_status: &str) -> ScanStatusKind {
    match scan_status {
        "queued" => ScanStatusKind::Queued,
        "committed" => ScanStatusKind::Committed,
        "flagged" => ScanStatusKind::Flagged,
        _ => ScanStatusKind::None,
    }
}

fn scan_delivery_status_should_ack(status: &str) -> bool {
    matches!(
        status,
        "created" | "accepted" | "duplicate" | "rejected" | "conflict"
    )
}

pub struct ScannerController<'a, RS: RingStorage> {
    scanner: &'a mut dyn ScannerHal,
    display: &'a mut dyn DisplayHal,
    feedback: &'a mut dyn FeedbackHal,
    power: &'a mut dyn PowerHal,
    nvs: &'a mut dyn NvsStore,
    portal: &'a mut dyn CaptivePortalHal,
    client: &'a mut dyn DeviceClient,
    wifi: &'a mut dyn WifiStationHal,
    ring: &'a mut RingBuffer<RS>,

    state: ScannerState,
    config: NvsConfig,
    cooldown: DuplicateCooldownState,
    ui_screen: UiScreen,
    last_scan: ScanResultView,
    boot_ms: u64,
    transient_until_ms: u64,
    last_heartbeat_ms: u64,
    last_vbus: bool,
    wifi_online: bool,
    device_revoked: bool,
    last_buffer_full_buzz_ms: Option<u64>,
    one_d_locked: bool,
    portal_active: bool,
    wifi_update_mode: bool,
    wifi_button_hold_active: bool,
    wifi_button_hold_erase: bool,
    mode_button_tracker: ButtonHoldTracker,
    mode_button_shown_phase: Option<ModeButtonHoldPhase>,
    manual_retrigger_pending: bool,
    trigger_continuous: bool,
    ap_suffix: String,
    connected_household_name: String,
    boot_id: String,
}

impl<'a, RS: RingStorage> ScannerController<'a, RS> {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        scanner: &'a mut dyn ScannerHal,
        display: &'a mut dyn DisplayHal,
        feedback: &'a mut dyn FeedbackHal,
        power: &'a mut dyn PowerHal,
        nvs: &'a mut dyn NvsStore,
        portal: &'a mut dyn CaptivePortalHal,
        client: &'a mut dyn DeviceClient,
        wifi: &'a mut dyn WifiStationHal,
        ring: &'a mut RingBuffer<RS>,
    ) -> Self {
        Self {
            scanner,
            display,
            feedback,
            power,
            nvs,
            portal,
            client,
            wifi,
            ring,
            state: ScannerState::default(),
            config: NvsConfig::default(),
            cooldown: DuplicateCooldownState::default(),
            ui_screen: UiScreen::S0Unprovisioned,
            last_scan: ScanResultView::default(),
            boot_ms: 0,
            transient_until_ms: 0,
            last_heartbeat_ms: 0,
            last_vbus: false,
            wifi_online: false,
            device_revoked: false,
            last_buffer_full_buzz_ms: None,
            one_d_locked: false,
            portal_active: false,
            wifi_update_mode: false,
            wifi_button_hold_active: false,
            wifi_button_hold_erase: false,
            mode_button_tracker: ButtonHoldTracker::new(),
            mode_button_shown_phase: None,
            manual_retrigger_pending: false,
            trigger_continuous: false,
            ap_suffix: String::new(),
            connected_household_name: String::new(),
            boot_id: String::new(),
        }
    }

    pub fn operational_state(&self) -> OperationalState {
        self.state.operational
    }

    pub fn one_d_locked(&self) -> bool {
        self.one_d_locked
    }

    pub fn ui_screen(&self) -> UiScreen {
        self.ui_screen
    }

    /// Offline queue depth for trace/scenario harnesses (mirrors C++ trace runner).
    pub fn ring_count(&self) -> usize {
        self.ring.count()
    }

    /// Barcodes in ring order when count ≤ 10; empty when capped (scenario golden format).
    pub fn ring_trace_barcodes(&self) -> Vec<String> {
        if self.ring.count() > 10 {
            return Vec::new();
        }
        self.ring
            .peek_batch(crate::ring_buffer::RING_CAPACITY)
            .iter()
            .map(|rec| c_str_from_fixed(&rec.barcode))
            .collect()
    }

    pub fn active_direction(&self) -> Direction {
        self.state.active_direction
    }

    pub fn manual_retrigger_pending(&self) -> bool {
        self.manual_retrigger_pending
    }

    pub fn trigger_continuous_mode(&self) -> bool {
        self.trigger_continuous
    }

    pub fn ap_suffix_for_test(&self) -> &str {
        &self.ap_suffix
    }

    fn apply_hub_config(&mut self, cfg: &DeviceHubConfigParsed) {
        if !cfg.label.is_empty() {
            self.config.label = cfg.label.clone();
        }
        self.config.default_direction = Self::parse_direction_string(&cfg.default_direction);
        self.config.inactivity_revert_s = cfg.inactivity_revert_seconds;
        self.config.commit_mode = Self::parse_commit_mode(&cfg.commit_mode);
        self.config.config_version = cfg.config_version;
        self.state.default_direction = self.config.default_direction;
        self.state.inactivity_revert_seconds = self.config.inactivity_revert_s;
        self.state.commit_mode = self.config.commit_mode;
        self.state.config_version = self.config.config_version;
    }

    fn parse_direction_string(dir: &str) -> Direction {
        if dir == "stock_in" {
            Direction::StockIn
        } else {
            Direction::StockOut
        }
    }

    fn parse_commit_mode(mode: &str) -> CommitMode {
        if mode == "immediate" {
            CommitMode::Immediate
        } else {
            CommitMode::Queue
        }
    }

    fn sync_wifi_online_from_station(&mut self) {
        if self.wifi.is_connected() {
            self.wifi_online = true;
        }
    }

    fn handle_portal_submission(&mut self, submission: &CaptivePortalSubmission, now_ms: u64) {
        self.config.wifi_ssid = submission.wifi_ssid.clone();
        self.config.wifi_pass = submission.wifi_pass.clone();
        if !submission.server_url.is_empty() {
            self.config.server_url = submission.server_url.clone();
        }
        self.nvs.save(&self.config);
        self.client.set_server_url(&self.config.server_url);

        self.portal.stop();
        self.portal_active = false;
        self.wifi_update_mode = false;
        self.wifi
            .connect(&self.config.wifi_ssid, &self.config.wifi_pass);

        if !submission.pairing_code.is_empty() && is_pairing_code(&submission.pairing_code) {
            let mut paired = PairCompleteParsed::default();
            let res = self.client.complete_pairing(
                &submission.pairing_code,
                &self.config.label,
                HUB_VERSION,
                &self.boot_id,
                &mut paired,
            );
            if res.http_status == 201 && !paired.device_token.is_empty() {
                self.finish_pairing_success(&paired, now_ms);
                return;
            }
            self.enter_pairing(now_ms);
            return;
        }

        if !self.config.device_token.is_empty() {
            self.enter_paired(now_ms);
            return;
        }
        self.enter_pairing(now_ms);
    }

    fn finish_pairing_success(&mut self, paired: &PairCompleteParsed, now_ms: u64) {
        if !self.ring.clear_all() {
            self.show_pairing_cant_connect(now_ms);
            return;
        }
        self.config.device_token = paired.device_token.clone();
        self.apply_hub_config(&paired.config);
        self.connected_household_name = paired.household_name.clone();
        self.nvs.save(&self.config);
        self.client.set_device_token(&self.config.device_token);
        self.device_revoked = false;
        self.state.operational = OperationalState::Paired;
        self.scanner.enable_1d_only(true);
        self.one_d_locked = true;
        self.state.active_direction = self.config.default_direction;
        self.state.default_direction = self.config.default_direction;
        self.state.on_scan_or_activity(now_ms);
        self.ui_screen = UiScreen::SConnected;
        self.transient_until_ms = now_ms + TRANSIENT_SCAN_MS;
        self.play(AudioCue::AscendingPairSuccess);
        self.render(now_ms);
    }

    fn show_pairing_code_expired(&mut self, now_ms: u64) {
        self.ui_screen = UiScreen::SPairingExpired;
        self.transient_until_ms = now_ms + TRANSIENT_SCAN_MS;
        self.play(AudioCue::TripleErrorBuzz);
        self.render(now_ms);
    }

    fn show_pairing_too_many_tries(&mut self, now_ms: u64) {
        self.ui_screen = UiScreen::SPairingTooManyTries;
        self.transient_until_ms = now_ms + TRANSIENT_SCAN_MS;
        self.play(AudioCue::ScanBeepLowTick);
        self.render(now_ms);
    }

    fn show_pairing_code_not_accepted(&mut self, now_ms: u64) {
        self.ui_screen = UiScreen::SPairingCodeNotAccepted;
        self.transient_until_ms = now_ms + TRANSIENT_SCAN_MS;
        self.play(AudioCue::TripleErrorBuzz);
        self.render(now_ms);
    }

    fn show_pairing_cant_connect(&mut self, now_ms: u64) {
        self.ui_screen = UiScreen::SPairingCantConnect;
        self.transient_until_ms = now_ms + TRANSIENT_SCAN_MS;
        self.play(AudioCue::TripleErrorBuzz);
        self.render(now_ms);
    }

    fn handle_device_disconnected(&mut self, now_ms: u64) {
        let _ = self.ring.clear_all();
        self.nvs.clear_device_token();
        self.config.device_token.clear();
        self.client.set_device_token("");
        self.device_revoked = true;
        self.connected_household_name.clear();
        self.ui_screen = UiScreen::S7HardError;
        self.play(AudioCue::TripleErrorBuzz);
        self.render(now_ms);
    }

    fn try_pairing_barcode(&mut self, raw_line: &str, now_ms: u64) {
        let mut paired = PairCompleteParsed::default();
        let norm = normalize_scan(raw_line).unwrap_or_else(|| trim_ascii(raw_line));
        let res = self.client.complete_pairing(
            &norm,
            &self.config.label,
            HUB_VERSION,
            &self.boot_id,
            &mut paired,
        );
        if res.http_status == 201 && !paired.device_token.is_empty() {
            self.finish_pairing_success(&paired, now_ms);
            return;
        }
        if pairing_complete_failure_is_expired(res.transport_ok, res.http_status, &res.body) {
            self.show_pairing_code_expired(now_ms);
            return;
        }
        if pairing_complete_failure_is_rate_limited(res.transport_ok, res.http_status, &res.body) {
            self.show_pairing_too_many_tries(now_ms);
            return;
        }
        if pairing_complete_failure_is_client_rejected(res.transport_ok, res.http_status, &res.body)
        {
            self.show_pairing_code_not_accepted(now_ms);
            return;
        }
        self.show_pairing_cant_connect(now_ms);
    }

    fn play(&mut self, cue: AudioCue) {
        if cue != AudioCue::Silent {
            self.feedback.play(cue);
        }
    }

    fn enter_unprovisioned(&mut self, now_ms: u64) {
        self.state.operational = OperationalState::Unprovisioned;
        self.ui_screen = UiScreen::S0Unprovisioned;
        self.one_d_locked = false;
        self.scanner.enable_1d_only(false);
        self.wifi.disconnect_station();
        if !self.portal_active {
            self.portal.start(&self.ap_suffix);
            self.portal_active = true;
        }
        self.state.wake_backlight(now_ms);
        self.render(now_ms);
    }

    fn enter_pairing(&mut self, now_ms: u64) {
        self.state.operational = OperationalState::Pairing;
        self.ui_screen = UiScreen::S1Pairing;
        self.scanner.enable_1d_only(false);
        self.one_d_locked = false;
        if self.portal_active {
            self.portal.stop();
            self.portal_active = false;
        }
        self.render(now_ms);
    }

    fn enter_paired(&mut self, now_ms: u64) {
        self.state.operational = OperationalState::Paired;
        self.ui_screen = UiScreen::S2Idle;
        self.scanner.enable_1d_only(true);
        self.one_d_locked = true;
        self.state.active_direction = self.config.default_direction;
        self.state.default_direction = self.config.default_direction;
        self.state.on_scan_or_activity(now_ms);
        self.render(now_ms);
    }

    pub fn begin(&mut self, now_ms: u64, hw_random_nonce: u32, mac_override: &str) {
        self.boot_ms = now_ms;
        let mac = if mac_override.is_empty() {
            "00:00:00:00:00:00"
        } else {
            mac_override
        };
        self.ap_suffix = ap_suffix_from_mac_address(mac);
        self.device_revoked = false;
        self.last_buffer_full_buzz_ms = None;
        self.wifi_button_hold_active = false;
        self.wifi_button_hold_erase = false;

        self.scanner.begin();
        self.display.begin();
        self.feedback.begin();
        self.power.begin();
        self.wifi.begin();

        if self.nvs.load(&mut self.config) {
            self.client.set_server_url(&self.config.server_url);
            self.client.set_device_token(&self.config.device_token);
        }

        self.config.boot_count += 1;
        self.nvs.save(&self.config);

        if hw_random_nonce != 0 {
            self.boot_id = format!(
                "b{}-{}-{:08x}",
                self.config.boot_count, now_ms, hw_random_nonce
            );
        } else {
            self.boot_id = format!("b{}-{}", self.config.boot_count, now_ms);
        }

        self.ring.recover_from_storage();
        self.ring
            .seed_id_counter_from_records(self.config.boot_count);

        if self.config.wifi_ssid.is_empty() {
            self.enter_unprovisioned(now_ms);
            return;
        }
        self.wifi
            .connect(&self.config.wifi_ssid, &self.config.wifi_pass);
        if self.config.device_token.is_empty() {
            self.enter_pairing(now_ms);
            return;
        }
        self.enter_paired(now_ms);
    }

    fn render(&mut self, now_ms: u64) {
        let transient = now_ms < self.transient_until_ms
            && matches!(
                self.ui_screen,
                UiScreen::S3ScanResult
                    | UiScreen::S4UnknownBarcode
                    | UiScreen::S5OfflineBuffered
                    | UiScreen::S6DockReminder
                    | UiScreen::S7OfflineBufferRefused
                    | UiScreen::SConnected
                    | UiScreen::SPairingExpired
                    | UiScreen::SPairingTooManyTries
                    | UiScreen::SPairingCodeNotAccepted
                    | UiScreen::SPairingCantConnect
            );

        if transient {
            // keep transient screen
        } else if self.wifi_button_hold_active {
            self.ui_screen = if self.wifi_button_hold_erase {
                UiScreen::SWifiButtonHoldErase
            } else {
                UiScreen::SWifiButtonHold
            };
        } else if self.wifi_update_mode && self.portal_active {
            self.ui_screen = UiScreen::SWifiSetup;
        } else if self.device_revoked || self.ring.is_full() {
            self.ui_screen = UiScreen::S7HardError;
        } else if self.state.operational == OperationalState::Unprovisioned {
            self.ui_screen = UiScreen::S0Unprovisioned;
        } else if self.state.operational == OperationalState::Pairing {
            self.ui_screen = UiScreen::S1Pairing;
        } else if self.ui_screen != UiScreen::S5OfflineBuffered
            && self.ui_screen != UiScreen::S3ScanResult
            && self.ui_screen != UiScreen::S4UnknownBarcode
        {
            self.ui_screen = UiScreen::S2Idle;
        }

        let mut input = DisplayModelInput {
            screen: self.ui_screen,
            commit_mode: self.state.commit_mode,
            active_direction: self.state.active_direction,
            default_direction: self.state.default_direction,
            inactivity_revert_seconds: self.state.inactivity_revert_seconds,
            last_activity_ms: self.state.last_activity_ms,
            now_ms,
            offline_queue_count: self.ring.count(),
            buffer_full: self.ring.is_full(),
            device_revoked: self.device_revoked,
            pairing_ap_suffix: self.ap_suffix.clone(),
            connected_label: self.config.label.clone(),
            household_name: self.connected_household_name.clone(),
            last_scan: self.last_scan.clone(),
            show_revert_countdown: self.state.active_direction != self.state.default_direction,
            vbus_present: false,
            battery_pct: None,
        };

        let ps = self.power.read();
        input.vbus_present = ps.vbus_present;
        input.battery_pct = ps.battery_pct;

        let mut bands = build_display_model(&input);

        if ps.vbus_present {
            let mut right = if self.wifi_online {
                "Wi-Fi".to_owned()
            } else {
                "OFFLINE".to_owned()
            };
            if let Some(pct) = ps.battery_pct {
                right.push(' ');
                right.push_str(&pct.to_string());
                right.push('%');
            }
            bands.band1_right = right;
        } else if let Some(pct) = ps.battery_pct {
            bands.band1_right = format!("{pct}%");
        }

        apply_display_band_colors(&mut bands);
        self.display.set_backlight(self.state.backlight);
        self.display.render(&bands);
    }

    fn handle_control_barcode(&mut self, kind: BarcodeKind, now_ms: u64) {
        if kind == BarcodeKind::ControlStockIn {
            self.state.set_direction(Direction::StockIn, now_ms, true);
            self.play(AudioCue::RisingTwoTone);
        } else if kind == BarcodeKind::ControlStockOut {
            self.state.set_direction(Direction::StockOut, now_ms, true);
            self.play(AudioCue::FallingTwoTone);
        }
        self.ui_screen = UiScreen::S2Idle;
        self.render(now_ms);
    }

    fn handle_product_scan(&mut self, barcode: &str, manual_retrigger: bool, now_ms: u64) -> bool {
        if !accept_with_duplicate_cooldown(
            &mut self.cooldown,
            barcode,
            self.state.active_direction,
            now_ms,
            manual_retrigger,
        ) {
            return true;
        }
        self.state.on_scan_or_activity(now_ms);

        if self
            .ring
            .enqueue(
                barcode,
                self.state.active_direction,
                now_ms,
                self.config.boot_count,
            )
            .is_err()
        {
            self.feedback_offline_buffer_refused(barcode, now_ms);
            return false;
        }

        if !self.wifi_online {
            self.last_scan.barcode = barcode.to_owned();
            self.last_scan.direction = self.state.active_direction;
            self.ui_screen = UiScreen::S5OfflineBuffered;
            self.transient_until_ms = now_ms + TRANSIENT_LONG_MS;
            self.play(AudioCue::ScanBeepLowTick);
            self.render(now_ms);
            return true;
        }

        while self.ring.count() > 0 {
            let batch = self.ring.peek_batch(1);
            let record = &batch[0];
            let show_ui = c_str_from_fixed(&record.barcode) == barcode;

            let mut parsed = crate::protocol_json::ParsedDeviceScanResponse::default();
            let age_ms = ring_scan_age_ms(record, now_ms);
            let time_approx = record.scanned_ms == 0;
            let http =
                self.client
                    .send_scan(record, &self.boot_id, age_ms, time_approx, &mut parsed);
            self.wifi_online = http.transport_ok && (200..500).contains(&http.http_status);

            if http.http_status == 401 || parsed.unauthorized {
                self.handle_device_disconnected(now_ms);
                return false;
            }

            if http_failure_is_retryable(http.http_status, http.transport_ok) {
                self.last_scan.barcode = barcode.to_owned();
                self.last_scan.direction = self.state.active_direction;
                self.ui_screen = UiScreen::S5OfflineBuffered;
                self.transient_until_ms = now_ms + TRANSIENT_LONG_MS;
                self.play(AudioCue::ScanBeepLowTick);
                self.render(now_ms);
                return true;
            }

            if scan_delivery_status_should_ack(&parsed.status) {
                self.ring
                    .acknowledge(&c_str_from_fixed(&record.client_scan_id));
            } else {
                break;
            }

            if !show_ui {
                continue;
            }

            self.last_scan.barcode = barcode.to_owned();
            self.last_scan.direction = self.state.active_direction;
            self.last_scan.known = parsed.known;
            self.last_scan.product_name = if parsed.product_name_is_null {
                String::new()
            } else {
                parsed.product_name.clone()
            };
            self.last_scan.status = parse_scan_status_kind(&parsed.scan_status);
            self.last_scan.queued_count = parsed.queued_count;
            self.last_scan.on_hand = parsed.on_hand;
            self.last_scan.delta = parsed.delta;
            self.last_scan.backfilled = parsed.backfilled;

            if !parsed.known || parsed.scan_status == "flagged" {
                self.ui_screen = UiScreen::S4UnknownBarcode;
                self.play(AudioCue::QuestionTone);
            } else {
                self.ui_screen = UiScreen::S3ScanResult;
                let preview = DisplayModelInput {
                    screen: UiScreen::S3ScanResult,
                    commit_mode: self.state.commit_mode,
                    last_scan: self.last_scan.clone(),
                    ..Default::default()
                };
                let bands = build_display_model(&preview);
                self.play(bands.audio);
                self.feedback
                    .flash_led_for_scan(self.state.active_direction == Direction::StockIn);
            }
            self.transient_until_ms = now_ms
                + if parsed.on_hand == 0 && self.state.active_direction == Direction::StockOut {
                    TRANSIENT_LONG_MS
                } else {
                    TRANSIENT_SCAN_MS
                };
            self.render(now_ms);
            return true;
        }

        true
    }

    fn feedback_offline_buffer_refused(&mut self, barcode: &str, now_ms: u64) {
        self.last_scan.barcode = barcode.to_owned();
        self.ui_screen = UiScreen::S7OfflineBufferRefused;
        self.transient_until_ms = now_ms + BUFFER_REFUSAL_TRANSIENT_MS;
        let buzz = self
            .last_buffer_full_buzz_ms
            .map(|last| now_ms - last >= BUFFER_FULL_FEEDBACK_MIN_INTERVAL_MS)
            .unwrap_or(true);
        if buzz {
            self.last_buffer_full_buzz_ms = Some(now_ms);
            self.play(AudioCue::TripleErrorBuzz);
        }
        self.render(now_ms);
    }

    pub fn on_barcode_line(&mut self, raw_line: &str, manual_retrigger: bool, now_ms: u64) {
        if self.state.is_scan_suppressed(now_ms) {
            return;
        }

        if self.device_revoked {
            let norm = normalize_scan(raw_line).unwrap_or_else(|| trim_ascii(raw_line));
            if is_pairing_code(&norm) {
                self.try_pairing_barcode(raw_line, now_ms);
            }
            return;
        }

        let kind = classify_barcode(raw_line, self.state.operational);

        if self.state.operational == OperationalState::Pairing && kind == BarcodeKind::PairingCode {
            self.try_pairing_barcode(raw_line, now_ms);
            return;
        }

        if self.ring.is_full() && self.state.operational == OperationalState::Paired {
            if matches!(
                kind,
                BarcodeKind::Product | BarcodeKind::ControlStockIn | BarcodeKind::ControlStockOut
            ) {
                self.feedback_offline_buffer_refused(&trim_ascii(raw_line), now_ms);
            }
            return;
        }

        if self.state.operational == OperationalState::Unprovisioned {
            let mut setup = crate::protocol_json::SetupQrParsed::default();
            if kind == BarcodeKind::SetupPayload && parse_setup_qr_payload(raw_line, &mut setup) {
                self.config.wifi_ssid = setup.ssid.clone();
                self.config.wifi_pass = setup.pass.clone();
                if !setup.url.is_empty() {
                    self.config.server_url = setup.url.clone();
                }
                self.nvs.save(&self.config);
                self.client.set_server_url(&self.config.server_url);
                self.wifi
                    .connect(&self.config.wifi_ssid, &self.config.wifi_pass);
                if !setup.code.is_empty() && is_pairing_code(&setup.code) {
                    let mut paired = PairCompleteParsed::default();
                    let res = self.client.complete_pairing(
                        &setup.code,
                        &self.config.label,
                        HUB_VERSION,
                        &self.boot_id,
                        &mut paired,
                    );
                    if res.http_status == 201 && !paired.device_token.is_empty() {
                        self.finish_pairing_success(&paired, now_ms);
                        return;
                    }
                }
                self.enter_pairing(now_ms);
            }
            return;
        }

        if self.state.operational == OperationalState::Pairing {
            return;
        }

        if matches!(
            kind,
            BarcodeKind::ControlStockIn | BarcodeKind::ControlStockOut
        ) {
            self.handle_control_barcode(kind, now_ms);
            return;
        }
        if kind == BarcodeKind::Product {
            let _ = self.handle_product_scan(&trim_ascii(raw_line), manual_retrigger, now_ms);
        }
    }

    fn apply_mode_button_hold_phase(&mut self, phase: ModeButtonHoldPhase, now_ms: u64) {
        self.wifi_button_hold_active = true;
        self.wifi_button_hold_erase = phase == ModeButtonHoldPhase::EraseWarning;
        self.state.wake_backlight(now_ms);
        self.render(now_ms);
    }

    fn clear_mode_button_hold_ui(&mut self) {
        self.wifi_button_hold_active = false;
        self.wifi_button_hold_erase = false;
        self.mode_button_shown_phase = None;
    }

    fn execute_factory_reset_from_hold(&mut self, now_ms: u64) {
        if !self.ring.clear_all() {
            return;
        }
        self.nvs.factory_reset();
        self.config = NvsConfig::default();
        self.device_revoked = false;
        self.last_buffer_full_buzz_ms = None;
        self.cooldown = DuplicateCooldownState::default();
        self.client.set_device_token("");
        self.enter_unprovisioned(now_ms);
    }

    fn execute_wifi_setup_from_hold(&mut self, now_ms: u64) {
        self.wifi_update_mode = true;
        self.portal.start(&self.ap_suffix);
        self.portal_active = true;
        self.ui_screen = UiScreen::SWifiSetup;
        self.state.wake_backlight(now_ms);
        self.render(now_ms);
    }

    pub fn on_mode_button_down(&mut self, now_ms: u64) {
        self.mode_button_tracker.on_press(now_ms);
        self.mode_button_shown_phase = None;
    }

    pub fn on_mode_button_hold_poll(&mut self, now_ms: u64) {
        if !self.mode_button_tracker.is_down() {
            return;
        }
        if self.mode_button_tracker.held_ms(now_ms) < 500 {
            return;
        }
        if let Some(phase) = self.mode_button_tracker.on_hold_progress(now_ms) {
            if self.mode_button_shown_phase != Some(phase) {
                self.mode_button_shown_phase = Some(phase);
                self.apply_mode_button_hold_phase(phase, now_ms);
            }
        }
    }

    pub fn on_mode_button_up(&mut self, now_ms: u64) {
        let action = self.mode_button_tracker.on_release(now_ms);
        self.clear_mode_button_hold_ui();
        match action {
            ModeButtonReleaseAction::ShortPress => self.on_mode_button_short_press(now_ms),
            ModeButtonReleaseAction::WifiSetup => self.execute_wifi_setup_from_hold(now_ms),
            ModeButtonReleaseAction::FactoryReset => self.execute_factory_reset_from_hold(now_ms),
            ModeButtonReleaseAction::None => {}
        }
    }

    pub fn on_mode_button_short_press(&mut self, now_ms: u64) {
        let result = self.state.on_mode_button_press(now_ms);
        if result.woke_backlight {
            self.render(now_ms);
            return;
        }
        if result.toggled_direction {
            let cue = match result.chime {
                ModeChime::RisingStockIn => AudioCue::RisingTwoTone,
                _ => AudioCue::FallingTwoTone,
            };
            self.play(cue);
            self.ui_screen = UiScreen::S2Idle;
            self.render(now_ms);
        }
    }

    pub fn on_mode_button_hold_tick(&mut self, hold_ms: u64, now_ms: u64) {
        let phase = if hold_ms >= 10_000 {
            ModeButtonHoldPhase::EraseWarning
        } else if hold_ms >= 5000 {
            ModeButtonHoldPhase::WifiSetupHold
        } else {
            ModeButtonHoldPhase::None
        };
        if phase == ModeButtonHoldPhase::None {
            if self.mode_button_shown_phase.is_some() {
                self.clear_mode_button_hold_ui();
                self.render(now_ms);
            }
            return;
        }
        if self.mode_button_shown_phase != Some(phase) {
            self.mode_button_shown_phase = Some(phase);
            self.apply_mode_button_hold_phase(phase, now_ms);
        }
    }

    pub fn on_mode_button_hold(&mut self, hold_ms: u64, now_ms: u64) {
        self.clear_mode_button_hold_ui();
        let gesture = ScannerState::classify_button_hold(hold_ms);
        if gesture == ButtonHoldGesture::FactoryReset {
            self.execute_factory_reset_from_hold(now_ms);
            return;
        }
        if gesture == ButtonHoldGesture::WifiUpdate {
            self.execute_wifi_setup_from_hold(now_ms);
        }
    }

    pub fn on_trigger_button_changed(&mut self, pressed: bool, now_ms: u64) {
        if pressed {
            self.state.wake_backlight(now_ms);
            self.manual_retrigger_pending = true;
            self.trigger_continuous = true;
            self.scanner.set_continuous_mode(true);
            self.render(now_ms);
            return;
        }
        self.trigger_continuous = false;
        let ps = self.power.read();
        if !ps.vbus_present {
            self.scanner.set_continuous_mode(false);
        }
    }

    fn flush_offline_batch(&mut self, now_ms: u64) {
        if !self.wifi_online
            || self.ring.count() == 0
            || self.state.operational != OperationalState::Paired
        {
            return;
        }
        let batch = self.ring.peek_batch(20);
        if batch.is_empty() {
            return;
        }
        let mut parsed = crate::protocol_json::ParsedDeviceScanResponse::default();
        let res = self
            .client
            .flush_batch(&batch, &self.boot_id, now_ms, &mut parsed);
        if res.http_status == 401 || parsed.unauthorized {
            self.handle_device_disconnected(now_ms);
            return;
        }
        if http_failure_is_retryable(res.http_status, res.transport_ok) {
            return;
        }
        if res.http_status < 200 || res.http_status >= 300 {
            return;
        }
        let mut items = Vec::new();
        if !parse_device_scan_batch_response_json(res.http_status, &res.body, &mut items) {
            return;
        }
        for item in items {
            if scan_delivery_status_should_ack(&item.status) {
                self.ring.acknowledge(&item.id);
            }
        }
    }

    fn maybe_heartbeat(&mut self, now_ms: u64) {
        if self.state.operational != OperationalState::Paired || self.config.device_token.is_empty()
        {
            return;
        }
        if now_ms - self.last_heartbeat_ms < HEARTBEAT_INTERVAL_MS {
            return;
        }
        self.last_heartbeat_ms = now_ms;
        let ps = self.power.read();
        let battery = ps.battery_pct.unwrap_or(-1);
        let mut hb = crate::protocol_json::HeartbeatParsed::default();
        let res = self.client.send_heartbeat(
            HUB_VERSION,
            &self.boot_id,
            ((now_ms - self.boot_ms) / 1000) as u32,
            self.ring.count(),
            battery,
            ps.charging,
            &mut hb,
        );
        self.wifi_online = res.transport_ok;
        if res.http_status == 401 {
            self.handle_device_disconnected(now_ms);
            return;
        }
        if hb.parse_ok && hb.config.config_version > self.config.config_version {
            self.apply_hub_config(&hb.config);
            self.nvs.save(&self.config);
        }
        if let Some(token) = &hb.rotated_device_token {
            if !token.is_empty() {
                self.config.device_token = token.clone();
                self.client.set_device_token(&self.config.device_token);
                self.nvs.save(&self.config);
            }
        }
    }

    pub fn tick(&mut self, now_ms: u64) {
        if self.portal_active {
            self.portal.loop_portal();
            let mut submission = CaptivePortalSubmission::default();
            if self.portal.take_submission(&mut submission) {
                self.handle_portal_submission(&submission, now_ms);
            }
        }

        self.wifi.tick();
        self.sync_wifi_online_from_station();

        let ps = self.power.read();
        if ps.vbus_present != self.last_vbus {
            self.state.on_vbus_transition(ps.vbus_present, now_ms);
            self.last_vbus = ps.vbus_present;
            self.ui_screen = UiScreen::S6DockReminder;
            self.transient_until_ms = now_ms + DOCK_BANNER_MS;
            self.play(if ps.vbus_present {
                AudioCue::DockTick
            } else {
                AudioCue::OffDockChirp
            });
            self.render(now_ms);
        }

        let mut scan_lines = Vec::new();
        self.scanner.poll(now_ms, &mut scan_lines);
        for line in scan_lines {
            let manual = self.manual_retrigger_pending;
            self.manual_retrigger_pending = false;
            self.on_barcode_line(&line.barcode, manual, now_ms);
        }

        if let Some(chime) = self.state.tick_inactivity_revert(now_ms) {
            if chime == ModeChime::ModeRevert {
                self.play(AudioCue::ModeRevertChime);
            }
            self.ui_screen = UiScreen::S2Idle;
            self.render(now_ms);
        }

        self.flush_offline_batch(now_ms);
        self.maybe_heartbeat(now_ms);

        if now_ms >= self.transient_until_ms {
            self.render(now_ms);
        }
    }
}
