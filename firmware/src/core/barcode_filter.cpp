#include "barcode_filter.h"

#include <cstring>

#include "text_util.h"

namespace stockbot {

namespace {

constexpr const char* kPairingPayloadAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
constexpr const char* kPairingCheckAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ*~$=U";

bool looksLikeSetupJson(std::string_view trimmed) {
  if (trimmed.find("stockbot://pair") == 0) {
    return true;
  }
  const auto pos = trimmed.find("{\"v\":1");
  if (pos == std::string_view::npos) {
    return trimmed.size() >= 2 && trimmed.front() == '{' && trimmed.find("\"ssid\"") != std::string_view::npos;
  }
  return pos == 0 || (pos > 0 && trimmed[pos] == '{');
}

bool pairingGrammarMatch(const std::string& code) {
  if (code.size() != 15 || code.compare(0, 4, "SBP-") != 0 || code[8] != '-' || code[13] != '-') {
    return false;
  }
  auto validChar = [](char ch, bool checkPos) {
    if (checkPos && (ch == '*' || ch == '~' || ch == '$' || ch == '=' || ch == 'U')) {
      return true;
    }
    if (ch >= '0' && ch <= '9') {
      return true;
    }
    if (ch >= 'A' && ch <= 'Z') {
      if (ch == 'I' || ch == 'O' || ch == 'U') {
        return false;
      }
      return true;
    }
    return false;
  };
  for (int i = 4; i < 8; ++i) {
    if (!validChar(code[static_cast<size_t>(i)], false)) {
      return false;
    }
  }
  for (int i = 9; i < 13; ++i) {
    if (!validChar(code[static_cast<size_t>(i)], false)) {
      return false;
    }
  }
  return validChar(code[14], true);
}

}  // namespace

char pairingCheckCharacter(const std::string& payload8) {
  uint64_t value = 0;
  for (char ch : payload8) {
    const char* pos = strchr(kPairingPayloadAlphabet, ch);
    if (pos == nullptr) {
      return '\0';
    }
    value = value * 32 + static_cast<uint64_t>(pos - kPairingPayloadAlphabet);
  }
  return kPairingCheckAlphabet[value % 37];
}

bool looksLikePairingCode(const std::string& raw) {
  std::string code;
  if (!normalizeScan(raw, code)) {
    return false;
  }
  return pairingGrammarMatch(code);
}

bool isPairingCode(const std::string& raw) {
  std::string code;
  if (!normalizeScan(raw, code) || !pairingGrammarMatch(code)) {
    return false;
  }
  const std::string payload = code.substr(4, 4) + code.substr(9, 4);
  const char check = pairingCheckCharacter(payload);
  return check != '\0' && code[14] == check;
}

namespace {

std::string trimAndUpperAscii(std::string_view raw) {
  std::string trimmed = trimAscii(raw);
  for (char& ch : trimmed) {
    if (ch >= 'a' && ch <= 'z') {
      ch = static_cast<char>(ch - 'a' + 'A');
    }
  }
  return trimmed;
}

}  // namespace

BarcodeKind classifyBarcode(const std::string& raw, OperationalState state) {
  const std::string control_view = trimAndUpperAscii(raw);
  if (control_view == "STOCK-OUT" || control_view == "STOCK_OUT") {
    return BarcodeKind::ControlStockOut;
  }
  if (control_view == "STOCK-IN" || control_view == "STOCK_IN") {
    return BarcodeKind::ControlStockIn;
  }

  std::string norm;
  if (!normalizeScan(raw, norm)) {
    const std::string trimmed = trimAscii(raw);
    if (state == OperationalState::Unprovisioned && looksLikeSetupJson(trimmed)) {
      return BarcodeKind::SetupPayload;
    }
    return BarcodeKind::RejectedNon1D;
  }

  if (looksLikePairingCode(norm)) {
    if (state == OperationalState::Paired) {
      return BarcodeKind::RejectedNon1D;
    }
    if (isPairingCode(norm)) {
      return BarcodeKind::PairingCode;
    }
    return BarcodeKind::RejectedNon1D;
  }

  const std::string trimmed = trimAscii(raw);
  if (state == OperationalState::Unprovisioned) {
    if (looksLikeSetupJson(trimmed)) {
      return BarcodeKind::SetupPayload;
    }
  }

  if (state == OperationalState::Pairing || state == OperationalState::Paired) {
    if (looksLikeSetupJson(trimmed) || trimmed.find("://") != std::string::npos || trimmed.find('{') != std::string::npos) {
      return BarcodeKind::RejectedNon1D;
    }
  }

  if (state == OperationalState::Paired) {
    if (isAsciiDigitProductBarcode(raw)) {
      return BarcodeKind::Product;
    }
    return BarcodeKind::RejectedNon1D;
  }

  if (state == OperationalState::Unprovisioned && isPairingCode(norm)) {
    return BarcodeKind::PairingCode;
  }

  return BarcodeKind::RejectedNon1D;
}

bool acceptWithDuplicateCooldown(DuplicateCooldownState& state, const std::string& barcode,
                                 Direction direction, uint64_t now_ms, bool manual_retrigger) {
  if (manual_retrigger) {
    state.last_barcode = barcode;
    state.last_direction = direction;
    state.last_accept_ms = now_ms;
    return true;
  }
  if (barcode != state.last_barcode || direction != state.last_direction) {
    state.last_barcode = barcode;
    state.last_direction = direction;
    state.last_accept_ms = now_ms;
    return true;
  }
  if (state.last_accept_ms == 0 || now_ms - state.last_accept_ms >= RESCAN_COOLDOWN_MS) {
    state.last_accept_ms = now_ms;
    return true;
  }
  return false;
}

}  // namespace stockbot
