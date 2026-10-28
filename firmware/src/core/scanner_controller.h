#pragma once

#include <cstdint>
#include <functional>
#include <string>

#include "barcode_filter.h"
#include "display_model.h"
#include "protocol_json.h"
#include "ring_buffer.h"
#include "scanner_state.h"
#include "captive_portal.h"
#include "nvs_config.h"

namespace stockbot {

class HalScanner;
class HalDisplay;
class HalFeedback;
class HalPower;
class NvsConfigStore;
class CaptivePortal;
class StockbotClient;
class WifiStation;

class ScannerController {
 public:
  ScannerController(HalScanner& scanner, HalDisplay& display, HalFeedback& feedback, HalPower& power,
                    NvsConfigStore& nvs, CaptivePortal& portal, StockbotClient& client, WifiStation& wifi,
                    RingBuffer& ring);

  void begin(uint64_t now_ms);
  void tick(uint64_t now_ms);
  void onBarcodeLine(const std::string& raw_line, bool manual_retrigger, uint64_t now_ms);
  void onModeButtonShortPress(uint64_t now_ms);
  void onModeButtonHold(uint64_t hold_ms, uint64_t now_ms);
  void onTriggerButtonChanged(bool pressed, uint64_t now_ms);

  OperationalState operationalState() const { return state_.operational; }
  bool oneDLocked() const { return one_d_locked_; }
  UiScreen uiScreen() const { return ui_screen_; }
  Direction activeDirection() const { return state_.active_direction; }
  bool manualRetriggerPending() const { return manual_retrigger_pending_; }
  bool triggerContinuousMode() const { return trigger_continuous_; }

 private:
  HalScanner& scanner_;
  HalDisplay& display_;
  HalFeedback& feedback_;
  HalPower& power_;
  NvsConfigStore& nvs_;
  CaptivePortal& portal_;
  StockbotClient& client_;
  WifiStation& wifi_;
  RingBuffer& ring_;

  ScannerState state_;
  NvsConfig config_;
  DuplicateCooldownState cooldown_;
  UiScreen ui_screen_ = UiScreen::S0_Unprovisioned;
  ScanResultView last_scan_;
  uint64_t boot_ms_ = 0;
  uint64_t transient_until_ms_ = 0;
  uint64_t last_heartbeat_ms_ = 0;
  uint64_t last_vbus_ = false;
  bool wifi_online_ = false;
  bool device_revoked_ = false;
  bool buffer_full_ = false;
  bool one_d_locked_ = false;
  bool portal_active_ = false;
  bool wifi_update_mode_ = false;
  bool manual_retrigger_pending_ = false;
  bool trigger_continuous_ = false;
  std::string ap_suffix_;
  std::string boot_id_;

  void handlePortalSubmission(const CaptivePortalSubmission& submission, uint64_t now_ms);
  void syncWifiOnlineFromStation();

  void applyHubConfig(const DeviceHubConfigParsed& cfg);
  void enterUnprovisioned(uint64_t now_ms);
  void enterPairing(uint64_t now_ms);
  void enterPaired(uint64_t now_ms);
  void render(uint64_t now_ms);
  void play(AudioCue cue);
  void handleControlBarcode(BarcodeKind kind, uint64_t now_ms);
  bool handleProductScan(const std::string& barcode, bool manual_retrigger, uint64_t now_ms);
  void flushOfflineBatch(uint64_t now_ms);
  void maybeHeartbeat(uint64_t now_ms);
  Direction parseDirectionString(const std::string& dir) const;
  CommitMode parseCommitMode(const std::string& mode) const;
};

}  // namespace stockbot
