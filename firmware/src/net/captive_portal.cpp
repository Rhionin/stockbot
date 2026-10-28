#include "captive_portal.h"

#ifdef ARDUINO

namespace stockbot {

class StockbotCaptivePortal final : public CaptivePortal {
 public:
  void start(const char*) override {}
  void stop() override {}
  void loop() override {}
};

}  // namespace stockbot

#endif
