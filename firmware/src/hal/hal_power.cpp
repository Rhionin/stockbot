#include "hal_power.h"

#ifdef ARDUINO

namespace stockbot {

class StickS3Power final : public HalPower {
 public:
  void begin() override {}
  PowerStatus read() override { return {}; }
  void setGrovePower(bool) override {}
  void enterDeepSleepWakeOnButton() override {}
};

}  // namespace stockbot

#endif
