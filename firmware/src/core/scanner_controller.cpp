#include "scanner_controller.h"

#include <cstring>

#include "barcode_filter.h"
#include "captive_portal.h"
#include "display_model.h"
#include "hal_display.h"
#include "hal_feedback.h"
#include "hal_power.h"
#include "hal_scanner.h"
#include "nvs_config.h"
#include "protocol_json.h"
#include "stockbot_client.h"
#include "text_util.h"

namespace stockbot {

namespace {

constexpr uint64_t kHeartbeatIntervalMs = 60000;
constexpr uint64_t kTransientScanMs = 4000;
constexpr uint64_t kTransientLongMs = 5000;
constexpr uint64_t kDockBannerMs = 2000;

ScanStatusKind parseScanStatusKind(const std::string& scan_status) {
  if (scan_status == "queued") {
    return ScanStatusKind::Queued;
  }
  if (scan_status == "committed") {
    return ScanStatusKind::Committed;
  }
  if (scan_status == "flagged") {
    return ScanStatusKind::Flagged;
  }
  return ScanStatusKind::None;
}

}  // namespace

ScannerController::ScannerController(HalScanner& scanner, HalDisplay& display, HalFeedback& feedback,
                                     HalPower& power, NvsConfigStore& nvs, CaptivePortal& portal,
                                     StockbotClient& client, RingBuffer& ring)
    : scanner_(scanner),
      display_(display),
      feedback_(feedback),
      power_(power),
      nvs_(nvs),
      portal_(portal),
      client_(client),
      ring_(ring) {}

void ScannerController::applyHubConfig(const DeviceHubConfigParsed& cfg) {
  if (!cfg.label.empty()) {
    config_.label = cfg.label;
  }
  config_.default_direction = parseDirectionString(cfg.default_direction);
  config_.inactivity_revert_s = cfg.inactivity_revert_seconds;
  config_.commit_mode = parseCommitMode(cfg.commit_mode);
  config_.config_version = cfg.config_version;
  state_.default_direction = config_.default_direction;
  state_.inactivity_revert_seconds = config_.inactivity_revert_s;
  state_.commit_mode = config_.commit_mode;
  state_.config_version = config_.config_version;
}

Direction ScannerController::parseDirectionString(const std::string& dir) const {
  return dir == "stock_in" ? Direction::StockIn : Direction::StockOut;
}

CommitMode ScannerController::parseCommitMode(const std::string& mode) const {
  return mode == "immediate" ? CommitMode::Immediate : CommitMode::Queue;
}

void ScannerController::play(AudioCue cue) {
  if (cue != AudioCue::Silent) {
    feedback_.play(cue);
  }
}

void ScannerController::enterUnprovisioned(uint64_t now_ms) {
  state_.operational = OperationalState::Unprovisioned;
  ui_screen_ = UiScreen::S0_Unprovisioned;
  one_d_locked_ = false;
  scanner_.enable1DOnly(false);
  if (!portal_active_) {
    portal_.start(ap_suffix_);
    portal_active_ = true;
  }
  state_.wakeBacklight(now_ms);
  render(now_ms);
}

void ScannerController::enterPairing(uint64_t now_ms) {
  state_.operational = OperationalState::Pairing;
  ui_screen_ = UiScreen::S1_Pairing;
  scanner_.enable1DOnly(false);
  one_d_locked_ = false;
  if (portal_active_) {
    portal_.stop();
    portal_active_ = false;
  }
  render(now_ms);
}

void ScannerController::enterPaired(uint64_t now_ms) {
  state_.operational = OperationalState::Paired;
  ui_screen_ = UiScreen::S2_Idle;
  scanner_.enable1DOnly(true);
  one_d_locked_ = true;
  state_.active_direction = config_.default_direction;
  state_.default_direction = config_.default_direction;
  state_.onScanOrActivity(now_ms);
  render(now_ms);
}

void ScannerController::begin(uint64_t now_ms) {
  boot_ms_ = now_ms;
  boot_id_ = "boot-" + std::to_string(now_ms);
  ap_suffix_ = "ABCD";
  device_revoked_ = false;
  buffer_full_ = false;

  scanner_.begin();
  display_.begin();
  feedback_.begin();
  power_.begin();
  ring_.recoverFromStorage();

  if (nvs_.load(config_)) {
    client_.setServerUrl(config_.server_url);
    client_.setDeviceToken(config_.device_token);
  }

  if (config_.wifi_ssid.empty()) {
    enterUnprovisioned(now_ms);
    return;
  }
  if (config_.device_token.empty()) {
    enterPairing(now_ms);
    return;
  }
  enterPaired(now_ms);
}

void ScannerController::render(uint64_t now_ms) {
  if (now_ms < transient_until_ms_ &&
      (ui_screen_ == UiScreen::S3_ScanResult || ui_screen_ == UiScreen::S4_UnknownBarcode ||
       ui_screen_ == UiScreen::S5_OfflineBuffered || ui_screen_ == UiScreen::S6_DockReminder)) {
    // keep transient screen
  } else if (device_revoked_ || buffer_full_) {
    ui_screen_ = UiScreen::S7_HardError;
  } else if (state_.operational == OperationalState::Unprovisioned) {
    ui_screen_ = UiScreen::S0_Unprovisioned;
  } else if (state_.operational == OperationalState::Pairing) {
    ui_screen_ = UiScreen::S1_Pairing;
  } else if (ui_screen_ != UiScreen::S5_OfflineBuffered && ui_screen_ != UiScreen::S3_ScanResult &&
             ui_screen_ != UiScreen::S4_UnknownBarcode) {
    ui_screen_ = UiScreen::S2_Idle;
  }

  DisplayModelInput in;
  in.screen = ui_screen_;
  in.operational = state_.operational;
  in.active_direction = state_.active_direction;
  in.default_direction = state_.default_direction;
  in.commit_mode = state_.commit_mode;
  in.inactivity_revert_seconds = state_.inactivity_revert_seconds;
  in.last_activity_ms = state_.last_activity_ms;
  in.now_ms = now_ms;
  in.wifi_online = wifi_online_;
  in.offline_queue_count = ring_.count();
  in.buffer_full = buffer_full_;
  in.device_revoked = device_revoked_;
  in.pairing_ap_suffix = ap_suffix_;
  in.connected_label = config_.label;
  in.last_scan = last_scan_;
  in.show_revert_countdown = state_.active_direction != state_.default_direction;

  const PowerStatus ps = power_.read();
  in.vbus_present = ps.vbus_present;
  in.battery_pct = ps.battery_pct;
  in.charging = ps.charging;

  DisplayBands bands = buildDisplayModel(in);

  if (ps.vbus_present) {
    std::string right = wifi_online_ ? "Wi-Fi" : "OFFLINE";
    if (ps.battery_pct.has_value()) {
      right += " " + std::to_string(*ps.battery_pct) + "%";
      if (ps.charging) {
        right += "⚡";
      }
    }
    bands.band1_right = right;
  } else if (ps.battery_pct.has_value()) {
    bands.band1_right = std::to_string(*ps.battery_pct) + "%";
  }

  applyDisplayBandColors(bands);
  display_.setBacklight(state_.backlight);
  display_.render(bands);
}

void ScannerController::handleControlBarcode(BarcodeKind kind, uint64_t now_ms) {
  if (kind == BarcodeKind::ControlStockIn) {
    state_.setDirection(Direction::StockIn, now_ms, true);
    play(AudioCue::RisingTwoTone);
  } else if (kind == BarcodeKind::ControlStockOut) {
    state_.setDirection(Direction::StockOut, now_ms, true);
    play(AudioCue::FallingTwoTone);
  }
  ui_screen_ = UiScreen::S2_Idle;
  render(now_ms);
}

bool ScannerController::handleProductScan(const std::string& barcode, bool manual_retrigger,
                                          uint64_t now_ms) {
  if (!acceptWithDuplicateCooldown(cooldown_, barcode, state_.active_direction, now_ms,
                                   manual_retrigger)) {
    return true;
  }
  state_.onScanOrActivity(now_ms);

  std::string client_scan_id;
  if (!ring_.enqueue(barcode, state_.active_direction, now_ms, client_scan_id)) {
    buffer_full_ = true;
    ui_screen_ = UiScreen::S7_HardError;
    play(AudioCue::TripleErrorBuzz);
    render(now_ms);
    return false;
  }

  const auto batch = ring_.peekBatch(ring_.count());
  if (batch.empty()) {
    return true;
  }
  const RingScanRecord record = batch.back();

  ParsedDeviceScanResponse parsed;
  const HttpCallResult http = client_.sendScan(record, boot_id_, now_ms - boot_ms_, parsed);
  wifi_online_ = http.transport_ok && http.http_status >= 200 && http.http_status < 500;

  if (http.http_status == 401 || parsed.unauthorized) {
    device_revoked_ = true;
    nvs_.clearDeviceToken();
    play(AudioCue::TripleErrorBuzz);
    render(now_ms);
    return false;
  }

  if (!http.transport_ok || http.http_status >= 500 || http.http_status == 429) {
    last_scan_.barcode = barcode;
    last_scan_.direction = state_.active_direction;
    ui_screen_ = UiScreen::S5_OfflineBuffered;
    transient_until_ms_ = now_ms + kTransientLongMs;
    play(AudioCue::ScanBeepLowTick);
    render(now_ms);
    return true;
  }

  ring_.acknowledge(record.client_scan_id);

  last_scan_.barcode = barcode;
  last_scan_.direction = state_.active_direction;
  last_scan_.known = parsed.known;
  last_scan_.product_name = parsed.product_name_is_null ? "" : parsed.product_name;
  last_scan_.status = parseScanStatusKind(parsed.scan_status);
  last_scan_.queued_count = parsed.queued_count;
  last_scan_.on_hand = parsed.on_hand;
  last_scan_.delta = parsed.delta;
  last_scan_.backfilled = parsed.backfilled;

  if (!parsed.known || parsed.scan_status == "flagged") {
    ui_screen_ = UiScreen::S4_UnknownBarcode;
    play(AudioCue::QuestionTone);
  } else {
    ui_screen_ = UiScreen::S3_ScanResult;
    if (state_.commit_mode == CommitMode::Immediate && parsed.backfilled) {
      play(AudioCue::SoftDoublePulse);
    } else if (parsed.on_hand == 0 && state_.active_direction == Direction::StockOut) {
      play(parsed.scan_status == "committed" ? AudioCue::DoubleBeep : AudioCue::SoftDoublePulse);
    } else {
      play(state_.active_direction == Direction::StockIn ? AudioCue::HighBeep : AudioCue::MediumBeep);
    }
    feedback_.flashLedForScan(state_.active_direction == Direction::StockIn);
  }
  transient_until_ms_ =
      now_ms + ((parsed.on_hand == 0 && state_.active_direction == Direction::StockOut) ? kTransientLongMs
                                                                                        : kTransientScanMs);
  render(now_ms);
  return true;
}

void ScannerController::onBarcodeLine(const std::string& raw_line, bool manual_retrigger,
                                      uint64_t now_ms) {
  if (device_revoked_ || buffer_full_) {
    return;
  }
  if (state_.isScanSuppressed(now_ms)) {
    return;
  }

  const BarcodeKind kind = classifyBarcode(raw_line, state_.operational);

  if (state_.operational == OperationalState::Unprovisioned) {
    SetupQrParsed setup;
    if (kind == BarcodeKind::SetupPayload && parseSetupQrPayload(raw_line, setup)) {
      config_.wifi_ssid = setup.ssid;
      config_.wifi_pass = setup.pass;
      if (!setup.url.empty()) {
        config_.server_url = setup.url;
      }
      nvs_.save(config_);
      client_.setServerUrl(config_.server_url);
      if (!setup.code.empty() && isPairingCode(setup.code)) {
        PairCompleteParsed paired;
        const HttpCallResult res =
            client_.completePairing(setup.code, config_.label, "1.0.0-esp32", boot_id_, paired);
        if (res.http_status == 201 && !paired.device_token.empty()) {
          config_.device_token = paired.device_token;
          applyHubConfig(paired.config);
          nvs_.save(config_);
          client_.setDeviceToken(config_.device_token);
          play(AudioCue::AscendingPairSuccess);
          enterPaired(now_ms);
          return;
        }
      }
      enterPairing(now_ms);
    }
    return;
  }

  if (state_.operational == OperationalState::Pairing) {
    if (kind == BarcodeKind::PairingCode) {
      PairCompleteParsed paired;
      std::string norm;
      normalizeScan(raw_line, norm);
      const HttpCallResult res =
          client_.completePairing(norm, config_.label, "1.0.0-esp32", boot_id_, paired);
      if (res.http_status == 201 && !paired.device_token.empty()) {
        config_.device_token = paired.device_token;
        applyHubConfig(paired.config);
        nvs_.save(config_);
        client_.setDeviceToken(config_.device_token);
        play(AudioCue::AscendingPairSuccess);
        enterPaired(now_ms);
      }
    }
    return;
  }

  if (kind == BarcodeKind::ControlStockIn || kind == BarcodeKind::ControlStockOut) {
    handleControlBarcode(kind, now_ms);
    return;
  }
  if (kind == BarcodeKind::Product) {
    handleProductScan(trimAscii(raw_line), manual_retrigger, now_ms);
  }
}

void ScannerController::onModeButtonShortPress(uint64_t now_ms) {
  const auto result = state_.onModeButtonPress(now_ms);
  if (result.woke_backlight) {
    render(now_ms);
    return;
  }
  if (result.toggled_direction) {
    play(result.chime == ModeChime::RisingStockIn ? AudioCue::RisingTwoTone : AudioCue::FallingTwoTone);
    ui_screen_ = UiScreen::S2_Idle;
    render(now_ms);
  }
}

void ScannerController::onModeButtonHold(uint64_t hold_ms, uint64_t now_ms) {
  const ButtonHoldGesture gesture = ScannerState::classifyButtonHold(hold_ms);
  if (gesture == ButtonHoldGesture::FactoryReset) {
    nvs_.factoryReset();
    config_ = NvsConfig{};
    device_revoked_ = false;
    buffer_full_ = false;
    cooldown_ = DuplicateCooldownState{};
    ring_.clearAll();
    client_.setDeviceToken("");
    enterUnprovisioned(now_ms);
    return;
  }
  if (gesture == ButtonHoldGesture::WifiUpdate) {
    portal_.start(ap_suffix_);
    portal_active_ = true;
    state_.wakeBacklight(now_ms);
    render(now_ms);
  }
}

void ScannerController::flushOfflineBatch(uint64_t now_ms) {
  if (!wifi_online_ || ring_.count() == 0 || state_.operational != OperationalState::Paired) {
    return;
  }
  const auto batch = ring_.peekBatch(20);
  if (batch.empty()) {
    return;
  }
  ParsedDeviceScanResponse parsed;
  const HttpCallResult res = client_.flushBatch(batch, boot_id_, parsed);
  if (res.http_status == 401 || parsed.unauthorized) {
    device_revoked_ = true;
    play(AudioCue::TripleErrorBuzz);
    render(now_ms);
    return;
  }
  if (!res.transport_ok) {
    return;
  }
  if (!res.transport_ok || res.http_status < 200 || res.http_status >= 300) {
    return;
  }
  for (const auto& item : batch) {
    ring_.acknowledge(item.client_scan_id);
  }
  (void)now_ms;
}

void ScannerController::maybeHeartbeat(uint64_t now_ms) {
  if (state_.operational != OperationalState::Paired || config_.device_token.empty()) {
    return;
  }
  if (now_ms - last_heartbeat_ms_ < kHeartbeatIntervalMs) {
    return;
  }
  last_heartbeat_ms_ = now_ms;
  const PowerStatus ps = power_.read();
  const int battery = ps.battery_pct.value_or(-1);
  HeartbeatParsed hb;
  const HttpCallResult res = client_.sendHeartbeat("1.0.0-esp32", boot_id_,
                                                   static_cast<uint32_t>((now_ms - boot_ms_) / 1000),
                                                   ring_.count(), battery, ps.charging, hb);
  wifi_online_ = res.transport_ok;
  if (res.http_status == 401) {
    device_revoked_ = true;
    play(AudioCue::TripleErrorBuzz);
    render(now_ms);
    return;
  }
  if (hb.parse_ok && hb.config.config_version > config_.config_version) {
    applyHubConfig(hb.config);
    nvs_.save(config_);
  }
  if (hb.rotated_device_token.has_value() && !hb.rotated_device_token->empty()) {
    config_.device_token = *hb.rotated_device_token;
    client_.setDeviceToken(config_.device_token);
    nvs_.save(config_);
  }
}

void ScannerController::tick(uint64_t now_ms) {
  portal_.loop();
  CaptivePortalSubmission submission;
  if (portal_.takeSubmission(submission)) {
    config_.wifi_ssid = submission.wifi_ssid;
    config_.wifi_pass = submission.wifi_pass;
    if (!submission.server_url.empty()) {
      config_.server_url = submission.server_url;
    }
    nvs_.save(config_);
    client_.setServerUrl(config_.server_url);
    if (!submission.pairing_code.empty() && isPairingCode(submission.pairing_code)) {
      onBarcodeLine(submission.pairing_code, true, now_ms);
    } else if (state_.operational == OperationalState::Unprovisioned) {
      enterPairing(now_ms);
    }
  }

  const PowerStatus ps = power_.read();
  if (ps.vbus_present != last_vbus_) {
    state_.onVbusTransition(ps.vbus_present, now_ms);
    last_vbus_ = ps.vbus_present;
    ui_screen_ = UiScreen::S6_DockReminder;
    transient_until_ms_ = now_ms + kDockBannerMs;
    play(ps.vbus_present ? AudioCue::DockTick : AudioCue::OffDockChirp);
    render(now_ms);
  }

  scanner_.poll(now_ms, [this, now_ms](const ScanLine& line) {
    onBarcodeLine(line.barcode, false, now_ms);
  });

  if (auto chime = state_.tickInactivityRevert(now_ms)) {
    if (*chime == ModeChime::ModeRevert) {
      play(AudioCue::ModeRevertChime);
    }
    ui_screen_ = UiScreen::S2_Idle;
    render(now_ms);
  }

  flushOfflineBatch(now_ms);
  maybeHeartbeat(now_ms);

  if (now_ms >= transient_until_ms_) {
    render(now_ms);
  }
}

}  // namespace stockbot
