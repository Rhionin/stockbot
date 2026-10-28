#include "hid_keycode.h"

namespace stockbot {

namespace {

constexpr uint8_t kUsageA = 0x04;
constexpr uint8_t kUsageZ = 0x1D;
constexpr uint8_t kUsage1 = 0x1E;
constexpr uint8_t kUsage9 = 0x26;
constexpr uint8_t kUsage0 = 0x27;
constexpr uint8_t kUsageMinus = 0x2D;
constexpr uint8_t kUsageEqual = 0x2E;
constexpr uint8_t kUsageLBracket = 0x2F;
constexpr uint8_t kUsageRBracket = 0x30;
constexpr uint8_t kUsageBackslash = 0x31;

}  // namespace

void HidKeycodeDecoder::resetLine() {
  line_.clear();
}

char HidKeycodeDecoder::hidUsageToAscii(uint8_t usage, bool shift) {
  if (usage >= kUsageA && usage <= kUsageZ) {
    char base = static_cast<char>('a' + (usage - kUsageA));
    return shift ? static_cast<char>(base - 'a' + 'A') : base;
  }
  if (usage >= kUsage1 && usage <= kUsage9) {
    return static_cast<char>('1' + (usage - kUsage1));
  }
  if (usage == kUsage0) {
    return '0';
  }
  if (usage == kUsageMinus) {
    return shift ? '_' : '-';
  }
  if (usage == kUsageEqual) {
    return shift ? '+' : '=';
  }
  if (usage == kUsageBackslash) {
    return shift ? '|' : '\\';
  }
  if (usage == kUsageLBracket) {
    return shift ? '{' : '[';
  }
  if (usage == kUsageRBracket) {
    return shift ? '}' : ']';
  }
  return '\0';
}

std::optional<std::string> HidKeycodeDecoder::consumeReport(const uint8_t report[8]) {
  const uint8_t modifier = report[0];
  const bool shift = (modifier & 0x22) != 0;
  bool saw_enter = false;
  uint8_t current[6] = {0, 0, 0, 0, 0, 0};

  for (int i = 0; i < 6; ++i) {
    const uint8_t key = report[2 + i];
    current[i] = key;
    if (key == 0 || keyWasDown(key, last_keys_)) {
      continue;
    }
    if (key == HID_KEY_ENTER) {
      saw_enter = true;
      continue;
    }
    if (key == HID_KEY_BACKSPACE && !line_.empty()) {
      line_.pop_back();
      continue;
    }
    const char ch = hidUsageToAscii(key, shift);
    if (ch != '\0') {
      line_.push_back(ch);
    }
  }
  for (int i = 0; i < 6; ++i) {
    last_keys_[i] = current[i];
  }
  if (saw_enter && !line_.empty()) {
    std::string completed = line_;
    line_.clear();
    return completed;
  }
  return std::nullopt;
}

}  // namespace stockbot
