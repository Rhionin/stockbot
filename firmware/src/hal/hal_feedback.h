#pragma once

#include "display_model.h"

namespace stockbot {

class HalFeedback {
 public:
  virtual ~HalFeedback() = default;
  virtual void play(AudioCue cue) = 0;
  virtual void flashLedForScan(bool stock_in) = 0;
};

}  // namespace stockbot
