#include "hal_display.h"

#ifdef ARDUINO

namespace stockbot {

class M5GfxDisplay final : public HalDisplay {
 public:
  void begin() override {}
  void render(const DisplayBands&) override {}
  void setBacklight(BacklightLevel) override {}
};

}  // namespace stockbot

#endif
