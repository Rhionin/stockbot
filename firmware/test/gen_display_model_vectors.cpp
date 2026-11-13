// TEST-ONLY: writes testdata/display-model/*.json from real C++ display_model.
//
// Usage: gen_display_model_vectors <out-dir>   (default: ../testdata/display-model)

#include <cstdio>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "display_model.h"

namespace {

std::string jsonEscape(const std::string& in) {
  std::string out;
  out.reserve(in.size() + 8);
  for (char ch : in) {
    switch (ch) {
      case '"':
        out += "\\\"";
        break;
      case '\\':
        out += "\\\\";
        break;
      case '\n':
        out += "\\n";
        break;
      case '\r':
        out += "\\r";
        break;
      default:
        out.push_back(ch);
        break;
    }
  }
  return out;
}

const char* uiScreenName(stockbot::UiScreen s) {
  switch (s) {
    case stockbot::UiScreen::S0_Unprovisioned:
      return "S0_Unprovisioned";
    case stockbot::UiScreen::S1_Pairing:
      return "S1_Pairing";
    case stockbot::UiScreen::S2_Idle:
      return "S2_Idle";
    case stockbot::UiScreen::S3_ScanResult:
      return "S3_ScanResult";
    case stockbot::UiScreen::S4_UnknownBarcode:
      return "S4_UnknownBarcode";
    case stockbot::UiScreen::S5_OfflineBuffered:
      return "S5_OfflineBuffered";
    case stockbot::UiScreen::S6_DockReminder:
      return "S6_DockReminder";
    case stockbot::UiScreen::S7_HardError:
      return "S7_HardError";
    case stockbot::UiScreen::S7_OfflineBufferRefused:
      return "S7_OfflineBufferRefused";
    case stockbot::UiScreen::S_Connected:
      return "S_Connected";
    case stockbot::UiScreen::S_PairingExpired:
      return "S_PairingExpired";
    case stockbot::UiScreen::S_PairingTooManyTries:
      return "S_PairingTooManyTries";
    case stockbot::UiScreen::S_PairingCodeNotAccepted:
      return "S_PairingCodeNotAccepted";
    case stockbot::UiScreen::S_PairingCantConnect:
      return "S_PairingCantConnect";
    case stockbot::UiScreen::S_WifiButtonHold:
      return "S_WifiButtonHold";
    case stockbot::UiScreen::S_WifiButtonHoldErase:
      return "S_WifiButtonHoldErase";
    case stockbot::UiScreen::S_WifiSetup:
      return "S_WifiSetup";
  }
  return "unknown";
}

const char* audioCueName(stockbot::AudioCue c) {
  switch (c) {
    case stockbot::AudioCue::Silent:
      return "Silent";
    case stockbot::AudioCue::AscendingPairSuccess:
      return "AscendingPairSuccess";
    case stockbot::AudioCue::RisingTwoTone:
      return "RisingTwoTone";
    case stockbot::AudioCue::FallingTwoTone:
      return "FallingTwoTone";
    case stockbot::AudioCue::HighBeep:
      return "HighBeep";
    case stockbot::AudioCue::MediumBeep:
      return "MediumBeep";
    case stockbot::AudioCue::DoubleBeep:
      return "DoubleBeep";
    case stockbot::AudioCue::SoftDoublePulse:
      return "SoftDoublePulse";
    case stockbot::AudioCue::QuestionTone:
      return "QuestionTone";
    case stockbot::AudioCue::ScanBeepLowTick:
      return "ScanBeepLowTick";
    case stockbot::AudioCue::TripleErrorBuzz:
      return "TripleErrorBuzz";
    case stockbot::AudioCue::ModeRevertChime:
      return "ModeRevertChime";
    case stockbot::AudioCue::DockTick:
      return "DockTick";
    case stockbot::AudioCue::OffDockChirp:
      return "OffDockChirp";
  }
  return "unknown";
}

const char* directionName(stockbot::Direction d) {
  return d == stockbot::Direction::StockIn ? "stock_in" : "stock_out";
}

const char* commitModeName(stockbot::CommitMode m) {
  return m == stockbot::CommitMode::Queue ? "queue" : "immediate";
}

const char* scanStatusName(stockbot::ScanStatusKind s) {
  switch (s) {
    case stockbot::ScanStatusKind::None:
      return "none";
    case stockbot::ScanStatusKind::Queued:
      return "queued";
    case stockbot::ScanStatusKind::Committed:
      return "committed";
    case stockbot::ScanStatusKind::Flagged:
      return "flagged";
  }
  return "none";
}

std::string bandsJson(const stockbot::DisplayBands& b) {
  std::ostringstream oss;
  oss << "{"
      << "\"band1\":\"" << jsonEscape(b.band1) << "\","
      << "\"band1Right\":\"" << jsonEscape(b.band1_right) << "\","
      << "\"band1Bg\":\"" << jsonEscape(b.band1_bg) << "\","
      << "\"band1BgRgb565\":" << b.band1_bg_rgb565 << ","
      << "\"band1FgRgb565\":" << b.band1_fg_rgb565 << ","
      << "\"band2Line1\":\"" << jsonEscape(b.band2_line1) << "\","
      << "\"band2Line2\":\"" << jsonEscape(b.band2_line2) << "\","
      << "\"band3\":\"" << jsonEscape(b.band3) << "\","
      << "\"band3FgRgb565\":" << b.band3_fg_rgb565 << ","
      << "\"audio\":\"" << audioCueName(b.audio) << "\""
      << "}";
  return oss.str();
}

std::string inputJson(const stockbot::DisplayModelInput& in) {
  std::ostringstream oss;
  oss << "{"
      << "\"screen\":\"" << uiScreenName(in.screen) << "\","
      << "\"commitMode\":\"" << commitModeName(in.commit_mode) << "\","
      << "\"activeDirection\":\"" << directionName(in.active_direction) << "\","
      << "\"defaultDirection\":\"" << directionName(in.default_direction) << "\","
      << "\"inactivityRevertSeconds\":" << in.inactivity_revert_seconds << ","
      << "\"lastActivityMs\":" << in.last_activity_ms << ","
      << "\"nowMs\":" << in.now_ms << ","
      << "\"offlineQueueCount\":" << in.offline_queue_count << ","
      << "\"bufferFull\":" << (in.buffer_full ? "true" : "false") << ","
      << "\"deviceRevoked\":" << (in.device_revoked ? "true" : "false") << ","
      << "\"pairingApSuffix\":\"" << jsonEscape(in.pairing_ap_suffix) << "\","
      << "\"connectedLabel\":\"" << jsonEscape(in.connected_label) << "\","
      << "\"householdName\":\"" << jsonEscape(in.household_name) << "\","
      << "\"showRevertCountdown\":" << (in.show_revert_countdown ? "true" : "false") << ","
      << "\"vbusPresent\":" << (in.vbus_present ? "true" : "false") << ",";
  if (in.battery_pct.has_value()) {
    oss << "\"batteryPct\":" << *in.battery_pct << ",";
  } else {
    oss << "\"batteryPct\":null,";
  }
  const auto& s = in.last_scan;
  oss << "\"lastScan\":{"
      << "\"status\":\"" << scanStatusName(s.status) << "\","
      << "\"known\":" << (s.known ? "true" : "false") << ","
      << "\"productName\":\"" << jsonEscape(s.product_name) << "\","
      << "\"barcode\":\"" << jsonEscape(s.barcode) << "\","
      << "\"direction\":\"" << directionName(s.direction) << "\","
      << "\"queuedCount\":" << s.queued_count << ","
      << "\"onHand\":" << s.on_hand << ","
      << "\"delta\":" << s.delta << ","
      << "\"backfilled\":" << (s.backfilled ? "true" : "false")
      << "}";
  oss << "}";
  return oss.str();
}

struct ScreenCase {
  const char* name;
  stockbot::DisplayModelInput in;
};

stockbot::DisplayModelInput baseIn(stockbot::UiScreen screen) {
  stockbot::DisplayModelInput in;
  in.screen = screen;
  return in;
}

void writeFile(const std::string& dir, const char* name, const std::string& body) {
  const std::string path = dir + "/" + name;
  std::ofstream out(path);
  if (!out) {
    std::cerr << "Failed to open " << path << "\n";
    std::exit(1);
  }
  out << body;
  if (!body.empty() && body.back() != '\n') {
    out << "\n";
  }
}

std::vector<ScreenCase> buildScreenCases() {
  std::vector<ScreenCase> cases;

  {
    auto in = baseIn(stockbot::UiScreen::S2_Idle);
    in.commit_mode = stockbot::CommitMode::Queue;
    cases.push_back({"s2-idle-queue", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S2_Idle);
    in.commit_mode = stockbot::CommitMode::Immediate;
    cases.push_back({"s2-idle-immediate", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S2_Idle);
    in.default_direction = stockbot::Direction::StockIn;
    in.active_direction = stockbot::Direction::StockOut;
    in.show_revert_countdown = true;
    in.inactivity_revert_seconds = 180;
    in.last_activity_ms = 1000;
    in.now_ms = 1000 + 60000;
    cases.push_back({"s2-idle-revert-countdown", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S2_Idle);
    in.active_direction = stockbot::Direction::StockIn;
    cases.push_back({"s2-idle-stock-in", in});
  }

  auto scanBase = [&]() {
    auto in = baseIn(stockbot::UiScreen::S3_ScanResult);
    in.commit_mode = stockbot::CommitMode::Queue;
    in.last_scan.known = true;
    in.last_scan.status = stockbot::ScanStatusKind::Queued;
    in.last_scan.product_name = "Kirkland Organic Diced Tomatoes 14.5 oz";
    in.last_scan.direction = stockbot::Direction::StockOut;
    in.last_scan.on_hand = 6;
    in.last_scan.queued_count = 1;
  return in;
  };

  cases.push_back({"s3-queue-stock-out-normal", scanBase()});
  {
    auto in = scanBase();
    in.last_scan.on_hand = 0;
    cases.push_back({"s3-queue-stock-out-zero-on-hand", in});
  }
  {
    auto in = scanBase();
    in.last_scan.on_hand = 1;
    in.last_scan.queued_count = 1;
    cases.push_back({"s3-queue-stock-out-last-one", in});
  }
  {
    auto in = scanBase();
    in.last_scan.direction = stockbot::Direction::StockIn;
    in.last_scan.on_hand = 4;
    in.last_scan.queued_count = 2;
    cases.push_back({"s3-queue-stock-in", in});
  }
  {
    auto in = scanBase();
    in.commit_mode = stockbot::CommitMode::Immediate;
    in.last_scan.direction = stockbot::Direction::StockIn;
    in.last_scan.on_hand = 5;
    cases.push_back({"s3-immediate-stock-in", in});
  }
  {
    auto in = scanBase();
    in.commit_mode = stockbot::CommitMode::Immediate;
    in.last_scan.direction = stockbot::Direction::StockOut;
    in.last_scan.on_hand = 3;
    in.last_scan.backfilled = false;
    cases.push_back({"s3-immediate-stock-out", in});
  }
  {
    auto in = scanBase();
    in.commit_mode = stockbot::CommitMode::Immediate;
    in.last_scan.on_hand = 0;
    cases.push_back({"s3-immediate-out-of-stock", in});
  }
  {
    auto in = scanBase();
    in.commit_mode = stockbot::CommitMode::Immediate;
    in.last_scan.backfilled = true;
    cases.push_back({"s3-immediate-backfilled", in});
  }
  {
    auto in = scanBase();
    in.last_scan.known = false;
    in.last_scan.barcode = "999888777";
    cases.push_back({"s3-unknown-known-false", in});
  }
  {
    auto in = scanBase();
    in.last_scan.status = stockbot::ScanStatusKind::Flagged;
    in.last_scan.barcode = "888777666";
    cases.push_back({"s3-unknown-flagged", in});
  }
  {
    auto in = scanBase();
    in.last_scan.known = true;
    in.last_scan.on_hand = 0;
    in.last_scan.queued_count = 2;
    cases.push_back({"s3-known-zero-on-hand-band-color", in});
  }

  {
    auto in = baseIn(stockbot::UiScreen::S4_UnknownBarcode);
    in.last_scan.barcode = "999888777";
    cases.push_back({"s4-unknown-barcode", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S5_OfflineBuffered);
    in.offline_queue_count = 7;
    in.last_scan.barcode = "4011";
    cases.push_back({"s5-offline-stock-out", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S5_OfflineBuffered);
    in.active_direction = stockbot::Direction::StockIn;
    in.offline_queue_count = 2;
    in.last_scan.barcode = "0123456789012345678";
    cases.push_back({"s5-offline-long-barcode", in});
  }

  {
    auto in = baseIn(stockbot::UiScreen::S6_DockReminder);
    in.vbus_present = true;
    in.battery_pct = 87;
    cases.push_back({"s6-charging", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S6_DockReminder);
    in.vbus_present = false;
    in.battery_pct = 42;
    cases.push_back({"s6-off-dock-battery", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S6_DockReminder);
    in.vbus_present = false;
    cases.push_back({"s6-off-dock-no-reading", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S6_DockReminder);
    in.vbus_present = true;
    cases.push_back({"s6-charging-no-battery-reading", in});
  }

  {
    auto in = baseIn(stockbot::UiScreen::S7_HardError);
    in.device_revoked = true;
    cases.push_back({"s7-revoked", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S7_HardError);
    in.buffer_full = true;
    cases.push_back({"s7-buffer-full", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S7_HardError);
    cases.push_back({"s7-hard-error-no-branch", in});
  }

  {
    auto in = baseIn(stockbot::UiScreen::S7_OfflineBufferRefused);
    in.last_scan.barcode = "4011";
    cases.push_back({"s7-refused-short-barcode", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S7_OfflineBufferRefused);
    in.last_scan.barcode = "4000000000011";
    cases.push_back({"s7-refused-long-barcode", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S7_OfflineBufferRefused);
    in.last_scan.barcode = "";
    cases.push_back({"s7-refused-empty-barcode", in});
  }

  {
    auto in = baseIn(stockbot::UiScreen::S0_Unprovisioned);
    in.pairing_ap_suffix = "ABCD";
    cases.push_back({"s0-unprovisioned", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S1_Pairing);
    cases.push_back({"s1-pairing", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S_Connected);
    in.household_name = "Test Household";
    cases.push_back({"s-connected-household", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S_Connected);
    in.connected_label = "Fallback Label";
    cases.push_back({"s-connected-fallback-label", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S_Connected);
    in.household_name = std::string(18, 'A') + std::string(19, 'B');
    cases.push_back({"s-connected-overflow-wrap", in});
  }
  {
    auto in = baseIn(stockbot::UiScreen::S_Connected);
    in.household_name = "Ch\u00e2teau du Supermarch\u00e9 du Nord";
    cases.push_back({"s-connected-utf8-wrap", in});
  }

  cases.push_back({"s-pairing-expired", baseIn(stockbot::UiScreen::S_PairingExpired)});
  cases.push_back({"s-pairing-too-many-tries", baseIn(stockbot::UiScreen::S_PairingTooManyTries)});
  cases.push_back({"s-pairing-code-not-accepted", baseIn(stockbot::UiScreen::S_PairingCodeNotAccepted)});
  cases.push_back({"s-pairing-cant-connect", baseIn(stockbot::UiScreen::S_PairingCantConnect)});
  cases.push_back({"s-wifi-button-hold", baseIn(stockbot::UiScreen::S_WifiButtonHold)});
  cases.push_back({"s-wifi-button-hold-erase", baseIn(stockbot::UiScreen::S_WifiButtonHoldErase)});
  {
    auto in = baseIn(stockbot::UiScreen::S_WifiSetup);
    in.pairing_ap_suffix = "WXYZ";
    cases.push_back({"s-wifi-setup", in});
  }

  return cases;
}

struct HelperCase {
  const char* name;
  std::string kind;
  std::string a;
  std::string b;
  uint32_t n;
  uint64_t u0;
  uint64_t u1;
  stockbot::Direction dir;
};

std::vector<HelperCase> buildHelperCases() {
  return {
      {"band2-barcode-18", "formatBand2BarcodeKeepEnd", "012345678901234567", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"band2-barcode-19", "formatBand2BarcodeKeepEnd", "0123456789012345678", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"wrap-product-long", "wrapProductNameTwoLines",
       "Kirkland Organic Diced Tomatoes Extra Long Product Name Here That Keeps Going", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"wrap-product-utf8", "wrapProductNameTwoLines", "Ch\u00e2teau du Supermarch\u00e9 du Nord", "",
       0, 0, 0, stockbot::Direction::StockOut},
      {"wrap-product-empty", "wrapProductNameTwoLines", "", "", 0, 0, 0, stockbot::Direction::StockOut},
      {"revert-initial", "formatRevertCountdown", "", "", 300, 0, 1000, stockbot::Direction::StockIn},
      {"revert-elapsed", "formatRevertCountdown", "", "", 180, 1000, 1000 + 60000,
       stockbot::Direction::StockIn},
      {"revert-expired", "formatRevertCountdown", "", "", 60, 1000, 1000 + 120000,
       stockbot::Direction::StockOut},
      {"rgb-teal", "rgb565FromHex", "#0D9488", "", 0, 0, 0, stockbot::Direction::StockOut},
      {"rgb-amber", "rgb565FromHex", "#F59E0B", "", 0, 0, 0, stockbot::Direction::StockOut},
      {"rgb-red", "rgb565FromHex", "#DC2626", "", 0, 0, 0, stockbot::Direction::StockOut},
      {"band3-color-queued-plus", "applyDisplayBandColors", "Queued +2 - 4 on hand", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"band3-color-plus-one", "applyDisplayBandColors", "+1 - 5 on hand", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"band3-color-zero-left", "applyDisplayBandColors", "-1 - 0 left (Out of stock)", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"band3-color-added-used", "applyDisplayBandColors", "0 on hand - Added & used 1", "", 0, 0, 0,
       stockbot::Direction::StockOut},
      {"band3-color-default", "applyDisplayBandColors", "Queues for review", "", 0, 0, 0,
       stockbot::Direction::StockOut},
  };
}

std::string helperOutput(const HelperCase& c) {
  if (c.kind == "formatBand2BarcodeKeepEnd") {
  return "\"" + jsonEscape(stockbot::formatBand2BarcodeKeepEnd(c.a)) + "\"";
  }
  if (c.kind == "wrapProductNameTwoLines") {
    const auto w = stockbot::wrapProductNameTwoLines(c.a);
    return "{\"line1\":\"" + jsonEscape(w.first) + "\",\"line2\":\"" + jsonEscape(w.second) + "\"}";
  }
  if (c.kind == "formatRevertCountdown") {
    return "\"" + jsonEscape(stockbot::formatRevertCountdown(c.n, c.u0, c.u1, c.dir)) + "\"";
  }
  if (c.kind == "rgb565FromHex") {
    return std::to_string(stockbot::rgb565FromHex(c.a));
  }
  if (c.kind == "applyDisplayBandColors") {
    stockbot::DisplayBands b;
    b.band3 = c.a;
    stockbot::applyDisplayBandColors(b);
    return std::to_string(b.band3_fg_rgb565);
  }
  return "\"\"";
}

}  // namespace

int main(int argc, char** argv) {
  std::string dir = "../testdata/display-model";
  if (argc >= 2) {
    dir = argv[1];
  }

  const auto screens = buildScreenCases();
  std::ostringstream screens_oss;
  screens_oss << "[\n";
  for (size_t i = 0; i < screens.size(); ++i) {
    const auto& c = screens[i];
    stockbot::DisplayBands bands = stockbot::buildDisplayModel(c.in);
    stockbot::applyDisplayBandColors(bands);
    screens_oss << "  {\"name\":\"" << c.name << "\",\"input\":" << inputJson(c.in) << ",\"bands\":"
                << bandsJson(bands) << "}";
    if (i + 1 < screens.size()) {
      screens_oss << ",";
    }
    screens_oss << "\n";
  }
  screens_oss << "]\n";
  writeFile(dir, "screens.json", screens_oss.str());

  const auto helpers = buildHelperCases();
  std::ostringstream helpers_oss;
  helpers_oss << "[\n";
  for (size_t i = 0; i < helpers.size(); ++i) {
    const auto& c = helpers[i];
    helpers_oss << "  {\"name\":\"" << c.name << "\",\"kind\":\"" << c.kind << "\",\"a\":\""
                << jsonEscape(c.a) << "\",\"n\":" << c.n << ",\"u0\":" << c.u0 << ",\"u1\":" << c.u1
                << ",\"direction\":\"" << directionName(c.dir) << "\",\"output\":" << helperOutput(c)
                << "}";
    if (i + 1 < helpers.size()) {
      helpers_oss << ",";
    }
    helpers_oss << "\n";
  }
  helpers_oss << "]\n";
  writeFile(dir, "helpers.json", helpers_oss.str());

  return 0;
}
