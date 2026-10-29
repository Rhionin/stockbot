#include "hal_feedback.h"

#ifdef ARDUINO

#include <M5Unified.h>

#include "board_pins.h"

namespace stockbot {

namespace {

void tonePair(int f1, int f2, int ms) {
  M5.Speaker.tone(f1, ms);
  delay(ms);
  M5.Speaker.tone(f2, ms);
}

}  // namespace

class StockbotFeedback final : public HalFeedback {
 public:
  void begin() override { M5.Speaker.begin(); }

  void play(AudioCue cue) override {
    switch (cue) {
      case AudioCue::AscendingPairSuccess:
        tonePair(880, 1320, 80);
        break;
      case AudioCue::RisingTwoTone:
        tonePair(660, 990, 70);
        break;
      case AudioCue::FallingTwoTone:
        tonePair(990, 660, 70);
        break;
      case AudioCue::HighBeep:
        M5.Speaker.tone(1800, 70);
        break;
      case AudioCue::MediumBeep:
        M5.Speaker.tone(1300, 70);
        break;
      case AudioCue::DoubleBeep:
        M5.Speaker.tone(1300, 60);
        delay(40);
        M5.Speaker.tone(950, 70);
        break;
      case AudioCue::SoftDoublePulse:
        M5.Speaker.tone(1100, 60);
        delay(50);
        M5.Speaker.tone(1100, 60);
        break;
      case AudioCue::QuestionTone:
        tonePair(1000, 1400, 70);
        break;
      case AudioCue::ScanBeepLowTick:
        M5.Speaker.tone(1300, 50);
        delay(30);
        M5.Speaker.tone(600, 40);
        break;
      case AudioCue::TripleErrorBuzz:
        for (int i = 0; i < 3; ++i) {
          M5.Speaker.tone(450, 80);
          delay(60);
        }
        break;
      case AudioCue::ModeRevertChime:
        tonePair(880, 660, 60);
        break;
      case AudioCue::DockTick:
        M5.Speaker.tone(900, 40);
        break;
      case AudioCue::OffDockChirp:
        tonePair(880, 880, 50);
        break;
      default:
        break;
    }
  }

  void flashLedForScan(bool stock_in) override {
#if TARGET_ATOMS3LITE
    const uint32_t color = stock_in ? 0x00ff00 : 0xffa500;
    M5.Displays(0).fillScreen(color);
    delay(30);
    M5.Displays(0).fillScreen(TFT_BLACK);
#else
    (void)stock_in;
#endif
  }
};

HalFeedback* createPlatformFeedback() {
  static StockbotFeedback feedback;
  return &feedback;
}

}  // namespace stockbot

#endif
