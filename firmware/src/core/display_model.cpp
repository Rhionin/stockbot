#include "display_model.h"

#include <algorithm>
#include <cstdio>
#include <sstream>

namespace stockbot {

namespace {

std::string directionBandLabel(Direction dir) {
  return dir == Direction::StockIn ? "STOCK IN" : "STOCK OUT";
}

}  // namespace

std::pair<std::string, std::string> wrapProductNameTwoLines(const std::string& name,
                                                            size_t max_chars_per_line) {
  if (name.empty()) {
    return {"", ""};
  }
  if (name.size() <= max_chars_per_line) {
    return {name, ""};
  }
  size_t break_at = max_chars_per_line;
  if (break_at < name.size()) {
    const size_t space = name.rfind(' ', break_at);
    if (space != std::string::npos && space > max_chars_per_line / 2) {
      break_at = space;
    }
  }
  std::string line1 = name.substr(0, break_at);
  std::string rest = name.substr(break_at);
  while (!rest.empty() && rest.front() == ' ') {
    rest.erase(rest.begin());
  }
  if (rest.size() > max_chars_per_line) {
    rest = rest.substr(0, max_chars_per_line - 1) + "…";
  }
  return {line1, rest};
}

std::string formatRevertCountdown(uint32_t inactivity_revert_seconds, uint64_t last_activity_ms,
                                  uint64_t now_ms) {
  if (last_activity_ms == 0 || now_ms <= last_activity_ms) {
    return "Reverts to Stock out in " + std::to_string(inactivity_revert_seconds / 60) + ":00";
  }
  const uint64_t elapsed = now_ms - last_activity_ms;
  const uint64_t total = static_cast<uint64_t>(inactivity_revert_seconds) * 1000ULL;
  if (elapsed >= total) {
    return "Reverts to Stock out in 0:00";
  }
  const uint64_t remaining_ms = total - elapsed;
  const uint64_t rem_sec = remaining_ms / 1000;
  const uint64_t minutes = rem_sec / 60;
  const uint64_t seconds = rem_sec % 60;
  char buf[64];
  std::snprintf(buf, sizeof(buf), "Reverts to Stock out in %llu:%02llu",
                static_cast<unsigned long long>(minutes), static_cast<unsigned long long>(seconds));
  return buf;
}

DisplayBands buildDisplayModel(const DisplayModelInput& in) {
  DisplayBands out;
  out.band2_line2.clear();

  if (in.screen == UiScreen::S0_Unprovisioned) {
    out.band1 = "READY TO SET UP";
    out.band1_bg = "#334155";
    out.band2_line1 = "Join Wi-Fi:";
    out.band2_line2 = "Stockbot-Scanner-" + in.pairing_ap_suffix;
    out.band3 = "Or scan Setup QR code";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S1_Pairing) {
    out.band1 = "CONNECT HOUSEHOLD";
    out.band1_bg = "#0D9488";
    out.band2_line1 = "Scan 1D pairing code";
    out.band2_line2 = "in Stockbot → Scanners";
    out.band3 = "Wi-Fi connected · Ready";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S7_HardError) {
    out.band1 = "SCANNER ALERT";
    out.band1_bg = "#DC2626";
    if (in.device_revoked) {
      out.band2_line1 = "Scanner disconnected";
      out.band3 = "Re-pair in Stockbot";
    } else {
      out.band2_line1 = "Offline memory full (500)";
      out.band3 = "Connect Wi-Fi to sync";
    }
    out.audio = AudioCue::TripleErrorBuzz;
    return out;
  }

  if (in.screen == UiScreen::S5_OfflineBuffered) {
    out.band1 = directionBandLabel(in.active_direction);
    out.band1_bg = in.active_direction == Direction::StockIn ? "#0D9488" : "#F59E0B";
    out.band2_line1 = "Saved offline";
    out.band2_line2 = in.last_scan.barcode;
    out.band3 = std::to_string(in.offline_queue_count) + " waiting to send";
    out.audio = AudioCue::ScanBeepLowTick;
    return out;
  }

  if (in.screen == UiScreen::S4_UnknownBarcode) {
    out.band1 = "NEW BARCODE";
    out.band1_bg = "#D97706";
    out.band2_line1 = "Unknown item";
    out.band2_line2 = in.last_scan.barcode;
    out.band3 = "In Scan Queue to name";
    out.audio = AudioCue::QuestionTone;
    return out;
  }

  if (in.screen == UiScreen::S6_DockReminder) {
    out.band1 = in.vbus_present ? "CHARGING" : "OFF DOCK";
    out.band1_bg = in.vbus_present ? "#0D9488" : "#F59E0B";
    if (in.vbus_present && in.battery_pct.has_value()) {
      out.band2_line1 = "Docked · Charging (" + std::to_string(*in.battery_pct) + "%)";
      out.band3 = "Hands-free ready";
      out.audio = AudioCue::DockTick;
    } else {
      out.band2_line1 = "Put me back on the dock to charge";
      out.band3 = in.battery_pct.has_value() ? "Battery " + std::to_string(*in.battery_pct) + "%" : "Battery —";
      out.audio = AudioCue::OffDockChirp;
    }
    return out;
  }

  if (in.screen == UiScreen::S3_ScanResult) {
    const auto& scan = in.last_scan;
    out.band1 = directionBandLabel(scan.direction);
    if (scan.direction == Direction::StockIn) {
      out.band1_bg = "#0D9488";
    } else if (scan.known && scan.on_hand <= 0 && scan.direction == Direction::StockOut) {
      out.band1_bg = "#D97706";
    } else {
      out.band1_bg = "#F59E0B";
    }
    auto wrapped = wrapProductNameTwoLines(scan.product_name);
    out.band2_line1 = wrapped.first;
    out.band2_line2 = wrapped.second;

    if (!scan.known || scan.status == ScanStatusKind::Flagged) {
      out.band1 = "NEW BARCODE";
      out.band1_bg = "#D97706";
      out.band2_line1 = "Unknown item";
      out.band2_line2 = scan.barcode;
      out.band3 = "In Scan Queue to name";
      out.audio = AudioCue::QuestionTone;
      return out;
    }

    if (in.commit_mode == CommitMode::Queue) {
      const int q = scan.queued_count > 0 ? scan.queued_count : 1;
      if (scan.direction == Direction::StockOut && scan.on_hand == 1 && q == 1) {
        out.band3 = "Queued -1 · Last one (1 on hand)";
        out.audio = AudioCue::DoubleBeep;
      } else if (scan.direction == Direction::StockOut && scan.on_hand == 0 && q == 1) {
        out.band3 = "Queued -1 · 0 on hand (Confirm in app)";
        out.audio = AudioCue::SoftDoublePulse;
      } else if (scan.direction == Direction::StockIn) {
        out.band3 = "Queued +" + std::to_string(q) + " · " + std::to_string(scan.on_hand) + " on hand";
        out.audio = AudioCue::HighBeep;
      } else {
        out.band3 = "Queued -" + std::to_string(q) + " · " + std::to_string(scan.on_hand) + " on hand";
        out.audio = AudioCue::MediumBeep;
      }
    } else {
      if (scan.direction == Direction::StockIn) {
        out.band3 = "+1 · " + std::to_string(scan.on_hand) + " on hand";
        out.audio = AudioCue::HighBeep;
      } else if (scan.backfilled || (scan.on_hand == 0 && scan.delta != 0)) {
        out.band3 = "0 on hand · Added & used 1";
        out.audio = AudioCue::SoftDoublePulse;
      } else if (scan.on_hand == 0) {
        out.band3 = "-1 · 0 left (Out of stock)";
        out.audio = AudioCue::DoubleBeep;
      } else {
        out.band3 = "-1 · " + std::to_string(scan.on_hand) + " left";
        out.audio = AudioCue::MediumBeep;
      }
    }
    return out;
  }

  // S2 Idle
  out.band1 = directionBandLabel(in.active_direction);
  out.band1_bg = in.active_direction == Direction::StockIn ? "#0D9488" : "#F59E0B";
  out.band2_line1 = "Ready to scan";
  if (in.show_revert_countdown && in.active_direction != in.default_direction) {
    out.band3 = formatRevertCountdown(in.inactivity_revert_seconds, in.last_activity_ms, in.now_ms);
  } else if (in.commit_mode == CommitMode::Queue) {
    out.band3 = "Queues for review";
  } else {
    out.band3 = "Updates stock now";
  }
  out.audio = AudioCue::Silent;
  return out;
}

}  // namespace stockbot
