#include "display_model.h"

#include <cstdio>
#include <cstdint>
#include <vector>

#include "ring_buffer.h"
#include "scanner_setup_network.h"

namespace stockbot {

namespace {

std::string directionBandLabel(Direction dir) {
  return dir == Direction::StockIn ? "STOCK IN" : "STOCK OUT";
}

std::string defaultDirectionPhrase(Direction dir) {
  return dir == Direction::StockIn ? "Stock in" : "Stock out";
}

void styleBand1(DisplayBands& out, const char* hex, uint16_t fg_rgb565) {
  out.band1_bg_rgb565 = rgb565FromHex(hex);
  out.band1_fg_rgb565 = fg_rgb565;
}

void finalizeBand3Color(DisplayBands& out) {
  if (out.band3.find("Queued +") != std::string::npos || out.band3.rfind("+1 -", 0) == 0) {
    out.band3_fg_rgb565 = 0x5FE0;
  } else if (out.band3.find("0 left") != std::string::npos ||
             out.band3.find("Added & used") != std::string::npos) {
    out.band3_fg_rgb565 = 0xFD20;
  } else {
    out.band3_fg_rgb565 = 0xFFE0;
  }
}

bool utf8DecodeAt(const std::string& text, size_t index, size_t& next_index, char32_t& codepoint) {
  if (index >= text.size()) {
    return false;
  }
  const unsigned char b0 = static_cast<unsigned char>(text[index]);
  if (b0 < 0x80) {
    codepoint = b0;
    next_index = index + 1;
    return true;
  }
  if ((b0 & 0xE0) == 0xC0 && index + 1 < text.size()) {
    const unsigned char b1 = static_cast<unsigned char>(text[index + 1]);
    if ((b1 & 0xC0) != 0x80) {
      return false;
    }
    codepoint = ((b0 & 0x1F) << 6) | (b1 & 0x3F);
    next_index = index + 2;
    return true;
  }
  if ((b0 & 0xF0) == 0xE0 && index + 2 < text.size()) {
    const unsigned char b1 = static_cast<unsigned char>(text[index + 1]);
    const unsigned char b2 = static_cast<unsigned char>(text[index + 2]);
    if ((b1 & 0xC0) != 0x80 || (b2 & 0xC0) != 0x80) {
      return false;
    }
    codepoint = ((b0 & 0x0F) << 12) | ((b1 & 0x3F) << 6) | (b2 & 0x3F);
    next_index = index + 3;
    return true;
  }
  if ((b0 & 0xF8) == 0xF0 && index + 3 < text.size()) {
    const unsigned char b1 = static_cast<unsigned char>(text[index + 1]);
    const unsigned char b2 = static_cast<unsigned char>(text[index + 2]);
    const unsigned char b3 = static_cast<unsigned char>(text[index + 3]);
    if ((b1 & 0xC0) != 0x80 || (b2 & 0xC0) != 0x80 || (b3 & 0xC0) != 0x80) {
      return false;
    }
    codepoint = ((b0 & 0x07) << 18) | ((b1 & 0x3F) << 12) | ((b2 & 0x3F) << 6) | (b3 & 0x3F);
    next_index = index + 4;
    return true;
  }
  return false;
}

std::string utf8PrefixDisplayChars(const std::string& text, size_t max_chars) {
  if (max_chars == 0) {
    return "";
  }
  size_t index = 0;
  size_t count = 0;
  while (index < text.size() && count < max_chars) {
    size_t next = index;
    char32_t cp = 0;
    if (!utf8DecodeAt(text, index, next, cp)) {
      break;
    }
    index = next;
    ++count;
  }
  return text.substr(0, index);
}

std::string utf8SuffixDisplayChars(const std::string& text, size_t max_chars) {
  if (max_chars == 0 || text.empty()) {
    return "";
  }
  struct Span {
    size_t start;
    size_t end;
  };
  std::vector<Span> spans;
  spans.reserve(text.size());
  size_t index = 0;
  while (index < text.size()) {
    const size_t start = index;
    size_t next = index;
    char32_t cp = 0;
    if (!utf8DecodeAt(text, index, next, cp)) {
      break;
    }
    spans.push_back({start, next});
    index = next;
  }
  if (spans.size() <= max_chars) {
    return text;
  }
  const size_t first = spans.size() - max_chars;
  return text.substr(spans[first].start, spans.back().end - spans[first].start);
}

size_t displayUtf8CharCountImpl(const std::string& text) {
  size_t count = 0;
  size_t index = 0;
  while (index < text.size()) {
    size_t next = index;
    char32_t cp = 0;
    if (!utf8DecodeAt(text, index, next, cp)) {
      break;
    }
    index = next;
    ++count;
  }
  return count;
}

std::pair<std::string, std::string> wrapBand2TwoLines(const std::string& name, size_t max_chars_per_line) {
  if (name.empty()) {
    return {"", ""};
  }
  if (displayUtf8CharCountImpl(name) < max_chars_per_line + 1) {
    return {name, ""};
  }

  size_t break_byte = name.size();
  size_t display_upto = 0;
  size_t last_space_byte = std::string::npos;
  size_t index = 0;
  while (index < name.size() && display_upto < max_chars_per_line) {
    const size_t start = index;
    size_t next = index;
    char32_t cp = 0;
    if (!utf8DecodeAt(name, index, next, cp)) {
      break;
    }
    if (cp == U' ') {
      last_space_byte = start;
    }
    index = next;
    ++display_upto;
    break_byte = index;
  }

  if (last_space_byte != std::string::npos && last_space_byte > max_chars_per_line / 2) {
    break_byte = last_space_byte;
  }

  std::string line1 = name.substr(0, break_byte);
  std::string rest = name.substr(break_byte);
  while (!rest.empty() && rest.front() == ' ') {
    rest.erase(rest.begin());
  }
  if (rest.empty()) {
    return {line1, ""};
  }
  if (displayUtf8CharCountImpl(rest) < max_chars_per_line + 1) {
    return {line1, rest};
  }
  const size_t keep = max_chars_per_line - 3;
  return {line1, utf8PrefixDisplayChars(rest, keep) + kEllipsis};
}

}  // namespace

size_t displayUtf8CharCount(const std::string& text) {
  return displayUtf8CharCountImpl(text);
}

uint16_t rgb565FromHex(const std::string& hex) {
  std::string h = hex;
  if (!h.empty() && h[0] == '#') {
    h.erase(h.begin());
  }
  if (h.size() != 6) {
    return 0;
  }
  auto nibble = [](char c) -> int {
    if (c >= '0' && c <= '9') {
      return c - '0';
    }
    if (c >= 'a' && c <= 'f') {
      return c - 'a' + 10;
    }
    if (c >= 'A' && c <= 'F') {
      return c - 'A' + 10;
    }
    return 0;
  };
  const int r = (nibble(h[0]) << 4) | nibble(h[1]);
  const int g = (nibble(h[2]) << 4) | nibble(h[3]);
  const int b = (nibble(h[4]) << 4) | nibble(h[5]);
  return static_cast<uint16_t>(((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3));
}

void applyDisplayBandColors(DisplayBands& bands) {
  finalizeBand3Color(bands);
}

std::string formatBand2BarcodeKeepEnd(const std::string& barcode) {
  if (displayUtf8CharCountImpl(barcode) < kBand2CharsPerLine + 1) {
    return barcode;
  }
  return std::string(kEllipsis) + utf8SuffixDisplayChars(barcode, kBand2CharsPerLine - 3);
}

std::pair<std::string, std::string> wrapProductNameTwoLines(const std::string& name,
                                                            size_t max_chars_per_line) {
  return wrapBand2TwoLines(name, max_chars_per_line);
}

std::string formatRevertCountdown(uint32_t inactivity_revert_seconds, uint64_t last_activity_ms,
                                  uint64_t now_ms, Direction default_direction) {
  const std::string mode = defaultDirectionPhrase(default_direction);
  if (last_activity_ms == 0 || now_ms <= last_activity_ms) {
    return "Back to " + mode + ": " + std::to_string(inactivity_revert_seconds / 60) + ":00";
  }
  const uint64_t elapsed = now_ms - last_activity_ms;
  const uint64_t total = static_cast<uint64_t>(inactivity_revert_seconds) * 1000ULL;
  if (elapsed >= total) {
    return "Back to " + mode + ": 0:00";
  }
  const uint64_t remaining_ms = total - elapsed;
  const uint64_t rem_sec = remaining_ms / 1000;
  const uint64_t minutes = rem_sec / 60;
  const uint64_t seconds = rem_sec % 60;
  char buf[64];
  std::snprintf(buf, sizeof(buf), "Back to %s: %llu:%02llu", mode.c_str(),
                static_cast<unsigned long long>(minutes), static_cast<unsigned long long>(seconds));
  return buf;
}

DisplayBands buildDisplayModel(const DisplayModelInput& in) {
  DisplayBands out;
  out.band2_line2.clear();

  if (in.screen == UiScreen::S0_Unprovisioned) {
    out.band1 = "READY TO SET UP";
    styleBand1(out, "#334155", 0xFFFF);
    out.band2_line1 = "Join Wi-Fi:";
    out.band2_line2 = scannerSetupSsid(in.pairing_ap_suffix);
    out.band3 = "Then follow the steps on your phone";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S_WifiSetup) {
    out.band1 = "WI-FI SETUP";
    styleBand1(out, "#334155", 0xFFFF);
    out.band2_line1 = "Join Wi-Fi:";
    out.band2_line2 = scannerSetupSsid(in.pairing_ap_suffix);
    out.band3 = "Then follow the steps on your phone";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S_WifiButtonHold) {
    out.band1 = "HOLDING";
    styleBand1(out, "#334155", 0xFFFF);
    out.band2_line1 = "Let go now to";
    out.band2_line2 = "update Wi-Fi";
    out.band3 = "Keep holding to erase everything";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S_WifiButtonHoldErase) {
    out.band1 = "LET GO TO ERASE";
    styleBand1(out, "#DC2626", 0xFFFF);
    out.band2_line1 = "Erases Wi-Fi,";
    out.band2_line2 = "pairing & scans";
    out.band3 = "Then use a setup sheet";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S_Connected) {
    out.band1 = "CONNECTED";
    styleBand1(out, "#0D9488", 0xFFFF);
    const std::string name = in.household_name.empty() ? in.connected_label : in.household_name;
    const auto wrapped = wrapProductNameTwoLines(name);
    out.band2_line1 = wrapped.first;
    out.band2_line2 = wrapped.second;
    out.band3 = "Ready to scan";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S_PairingExpired) {
    out.band1 = "PAIRING CODE EXPIRED";
    styleBand1(out, "#DC2626", 0xFFFF);
    out.band2_line1 = "Print a new";
    out.band2_line2 = "setup sheet";
    out.band3.clear();
    out.audio = AudioCue::TripleErrorBuzz;
    return out;
  }

  if (in.screen == UiScreen::S_PairingTooManyTries) {
    out.band1 = "TOO MANY TRIES";
    out.band1_bg_rgb565 = rgb565FromHex("#F59E0B");
    out.band1_fg_rgb565 = rgb565FromHex("#111827");
    out.band2_line1 = "Wait a minute,";
    out.band2_line2 = "then scan again";
    out.band3 = "Then scan the setup sheet again";
    out.audio = AudioCue::ScanBeepLowTick;
    return out;
  }

  if (in.screen == UiScreen::S_PairingCodeNotAccepted) {
    out.band1 = "CODE NOT ACCEPTED";
    styleBand1(out, "#DC2626", 0xFFFF);
    out.band2_line1 = "This setup sheet";
    out.band2_line2 = "didn't work";
    out.band3 = "Print a new setup sheet";
    out.audio = AudioCue::TripleErrorBuzz;
    return out;
  }

  if (in.screen == UiScreen::S_PairingCantConnect) {
    out.band1 = "CAN'T CONNECT";
    styleBand1(out, "#DC2626", 0xFFFF);
    out.band2_line1 = "Couldn't reach";
    out.band2_line2 = "the server";
    out.band3 = "Check Wi-Fi, then scan again";
    out.audio = AudioCue::TripleErrorBuzz;
    return out;
  }

  if (in.screen == UiScreen::S1_Pairing) {
    out.band1 = "CONNECT HOUSEHOLD";
    styleBand1(out, "#0D9488", 0xFFFF);
    out.band2_line1 = "Scan the pairing";
    out.band2_line2 = "barcode to connect";
    out.band3 = "Wi-Fi connected - Ready";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S7_OfflineBufferRefused) {
    out.band1 = "NOT SAVED";
    styleBand1(out, "#DC2626", 0xFFFF);
    out.band2_line1 = "Memory is full";
    out.band2_line2 = formatBand2BarcodeKeepEnd(in.last_scan.barcode);
    out.band3 = "Connect Wi-Fi to send";
    out.audio = AudioCue::Silent;
    return out;
  }

  if (in.screen == UiScreen::S7_HardError) {
    if (in.device_revoked) {
      out.band1 = "SCANNER ALERT";
      styleBand1(out, "#DC2626", 0xFFFF);
      out.band2_line1 = "Disconnected";
      out.band2_line2 = "from household";
      out.band3 = "Needs a new setup sheet";
      out.audio = AudioCue::TripleErrorBuzz;
    } else if (in.buffer_full) {
      out.band1 = "MEMORY FULL";
      styleBand1(out, "#DC2626", 0xFFFF);
      out.band2_line1 = std::to_string(RingBuffer::kCapacity) + " scans waiting";
      out.band2_line2 = "Scans won't save";
      out.band3 = "Connect Wi-Fi to send";
      out.audio = AudioCue::Silent;
    }
    return out;
  }

  if (in.screen == UiScreen::S5_OfflineBuffered) {
    out.band1 = directionBandLabel(in.active_direction);
    styleBand1(out, in.active_direction == Direction::StockIn ? "#0D9488" : "#F59E0B",
               in.active_direction == Direction::StockIn ? 0xFFFF : 0x2104);
    out.band2_line1 = "Saved offline";
    out.band2_line2 = formatBand2BarcodeKeepEnd(in.last_scan.barcode);
    out.band3 = std::to_string(in.offline_queue_count) + " waiting to send";
    out.audio = AudioCue::ScanBeepLowTick;
    return out;
  }

  if (in.screen == UiScreen::S4_UnknownBarcode) {
    out.band1 = "NEW BARCODE";
    styleBand1(out, "#D97706", 0xFFFF);
    out.band2_line1 = "Unknown item";
    out.band2_line2 = formatBand2BarcodeKeepEnd(in.last_scan.barcode);
    out.band3 = "In Scan Queue to name";
    out.audio = AudioCue::QuestionTone;
    return out;
  }

  if (in.screen == UiScreen::S6_DockReminder) {
    out.band1 = in.vbus_present ? "CHARGING" : "OFF DOCK";
    styleBand1(out, in.vbus_present ? "#0D9488" : "#F59E0B", in.vbus_present ? 0xFFFF : 0x2104);
    if (in.vbus_present && in.battery_pct.has_value()) {
      out.band2_line1 = "Charging " + std::to_string(*in.battery_pct) + "%";
      out.band3 = "Hands-free ready";
      out.audio = AudioCue::DockTick;
    } else {
      out.band2_line1 = "Put me back on";
      out.band2_line2 = "the dock to charge";
      out.band3 = in.battery_pct.has_value() ? "Battery " + std::to_string(*in.battery_pct) + "%"
                                             : "Battery: no reading";
      out.audio = AudioCue::OffDockChirp;
    }
    return out;
  }

  if (in.screen == UiScreen::S3_ScanResult) {
    const auto& scan = in.last_scan;
    out.band1 = directionBandLabel(scan.direction);
    if (scan.direction == Direction::StockIn) {
      styleBand1(out, "#0D9488", 0xFFFF);
    } else if (scan.known && scan.on_hand <= 0 && scan.direction == Direction::StockOut) {
      styleBand1(out, "#D97706", 0xFFFF);
    } else {
      styleBand1(out, "#F59E0B", 0x2104);
    }
    auto wrapped = wrapProductNameTwoLines(scan.product_name);
    out.band2_line1 = wrapped.first;
    out.band2_line2 = wrapped.second;

    if (!scan.known || scan.status == ScanStatusKind::Flagged) {
      out.band1 = "NEW BARCODE";
      styleBand1(out, "#D97706", 0xFFFF);
      out.band2_line1 = "Unknown item";
      out.band2_line2 = formatBand2BarcodeKeepEnd(scan.barcode);
      out.band3 = "In Scan Queue to name";
      out.audio = AudioCue::QuestionTone;
      return out;
    }

    if (in.commit_mode == CommitMode::Queue) {
      const int q = scan.queued_count > 0 ? scan.queued_count : 1;
      if (scan.direction == Direction::StockOut) {
        if (scan.on_hand == 0) {
          out.band3 = "Queued -1 - 0 on hand - Check app";
          out.audio = AudioCue::SoftDoublePulse;
        } else if (scan.on_hand == 1 && q == 1) {
          out.band3 = "Queued -1 - Last one (1 on hand)";
          out.audio = AudioCue::DoubleBeep;
        } else {
          out.band3 = "Queued -" + std::to_string(q) + " - " + std::to_string(scan.on_hand) + " on hand";
          out.audio = AudioCue::MediumBeep;
        }
      } else {
        out.band3 = "Queued +" + std::to_string(q) + " - " + std::to_string(scan.on_hand) + " on hand";
        out.audio = AudioCue::HighBeep;
      }
    } else {
      if (scan.direction == Direction::StockIn) {
        out.band3 = "+1 - " + std::to_string(scan.on_hand) + " on hand";
        out.audio = AudioCue::HighBeep;
      } else if (scan.backfilled) {
        out.band3 = "0 on hand - Added & used 1";
        out.audio = AudioCue::SoftDoublePulse;
      } else if (scan.on_hand == 0) {
        out.band3 = "-1 - 0 left (Out of stock)";
        out.audio = AudioCue::DoubleBeep;
      } else {
        out.band3 = "-1 - " + std::to_string(scan.on_hand) + " left";
        out.audio = AudioCue::MediumBeep;
      }
    }
    return out;
  }

  // S2 Idle
  out.band1 = directionBandLabel(in.active_direction);
  styleBand1(out, in.active_direction == Direction::StockIn ? "#0D9488" : "#F59E0B",
             in.active_direction == Direction::StockIn ? 0xFFFF : 0x2104);
  out.band2_line1 = "Ready to scan";
  if (in.show_revert_countdown && in.active_direction != in.default_direction) {
    out.band3 = formatRevertCountdown(in.inactivity_revert_seconds, in.last_activity_ms, in.now_ms,
                                      in.default_direction);
  } else if (in.commit_mode == CommitMode::Queue) {
    out.band3 = "Queues for review";
  } else {
    out.band3 = "Updates stock now";
  }
  out.audio = AudioCue::Silent;
  return out;
}

}  // namespace stockbot
