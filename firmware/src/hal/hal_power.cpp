#include "hal_power.h"

#ifdef ARDUINO

#include <esp_sleep.h>

#include <M5Unified.h>

#include "board_pins.h"

namespace stockbot {

class StickS3Power final : public HalPower {
 public:
  void begin() override {}

  PowerStatus read() override {
    PowerStatus status;
#if TARGET_STICKS3
    status.vbus_present = M5.Power.getVBUSVoltage() > 4000;
    status.charging = M5.Power.isCharging();
    status.battery_pct = M5.Power.getBatteryLevel();
#else
    status.vbus_present = true;
    status.charging = false;
    status.battery_pct = std::nullopt;
#endif
    return status;
  }

  void setGrovePower(bool enabled) override {
#if TARGET_STICKS3
    M5.Power.setExtOutput(enabled);
#else
    (void)enabled;
#endif
  }

  void enterDeepSleepWakeOnButton() override {
    setGrovePower(false);
    esp_sleep_enable_ext0_wakeup(static_cast<gpio_num_t>(PIN_BTN_TRIGGER), 0);
    esp_deep_sleep_start();
  }
};

HalPower* createPlatformPower() {
  static StickS3Power power;
  return &power;
}

}  // namespace stockbot

#endif
