#pragma once

#include <optional>

namespace stockbot {

struct PowerStatus {
  bool vbus_present = false;
  std::optional<int> battery_pct;
  bool charging = false;
};

class HalPower {
 public:
  virtual ~HalPower() = default;
  virtual void begin() = 0;
  virtual PowerStatus read() = 0;
  virtual void setGrovePower(bool enabled) = 0;
  virtual void enterDeepSleepWakeOnButton() = 0;
};

}  // namespace stockbot
