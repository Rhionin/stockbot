#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace stockbot {

constexpr uint8_t HID_KEY_ENTER = 0x28;
constexpr uint8_t HID_KEY_BACKSPACE = 0x2A;

class HidKeycodeDecoder {
 public:
  void resetLine();
  std::optional<std::string> consumeReport(const uint8_t report[8]);
  const std::string& lineBuffer() const { return line_; }

 private:
  std::string line_;
  uint8_t last_keys_[6] = {0, 0, 0, 0, 0, 0};

  static bool keyWasDown(uint8_t key, const uint8_t previous[6]) {
    if (key == 0) {
      return false;
    }
    for (int i = 0; i < 6; ++i) {
      if (previous[i] == key) {
        return true;
      }
    }
    return false;
  }

  static char hidUsageToAscii(uint8_t usage, bool shift);
};

}  // namespace stockbot
