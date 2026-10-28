#pragma once

#include "display_model.h"

namespace stockbot {

class HalDisplay {
 public:
  virtual ~HalDisplay() = default;
  virtual void begin() = 0;
  virtual void render(const DisplayBands& bands) = 0;
  virtual void setBacklight(BacklightLevel level) = 0;
};

#ifdef ARDUINO
HalDisplay* createPlatformDisplay();
#endif

}  // namespace stockbot
