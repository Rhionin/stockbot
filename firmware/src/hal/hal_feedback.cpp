#include "hal_feedback.h"

#ifdef ARDUINO

namespace stockbot {

class StockbotFeedback final : public HalFeedback {
 public:
  void play(AudioCue) override {}
  void flashLedForScan(bool) override {}
};

}  // namespace stockbot

#endif
