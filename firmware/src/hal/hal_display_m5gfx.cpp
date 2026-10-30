#include "hal_display.h"

#ifdef ARDUINO

#include <M5GFX.h>
#include <M5Unified.h>

#include "board_pins.h"

namespace stockbot {

namespace {

M5Canvas canvas(&M5.Display);
bool canvas_ready = false;

uint8_t backlightDuty(BacklightLevel level) {
  switch (level) {
    case BacklightLevel::Full100:
      return 255;
    case BacklightLevel::Dim20:
      return 51;
    case BacklightLevel::Off0:
    default:
      return 0;
  }
}

}  // namespace

class M5GfxDisplay final : public HalDisplay {
 public:
  void begin() override {
    auto cfg = M5.config();
    M5.begin(cfg);
    M5.Display.setRotation(1);
    M5.Display.setBrightness(255);
#if USE_U120_I2C_DISPLAY
    M5.Display.begin();
#endif
    canvas.createSprite(240, 135);
    canvas.setColorDepth(16);
    canvas_ready = true;
  }

  void setBacklight(BacklightLevel level) override {
    M5.Display.setBrightness(backlightDuty(level));
  }

  void render(const DisplayBands& bands) override {
    if (!canvas_ready) {
      return;
    }
    canvas.fillSprite(bands.band1_bg_rgb565);
    canvas.setTextColor(bands.band1_fg_rgb565, bands.band1_bg_rgb565);
    canvas.setTextSize(1);
    canvas.setCursor(8, 6);
    canvas.print(bands.band1.c_str());
    if (!bands.band1_right.empty()) {
      canvas.setCursor(232 - bands.band1_right.size() * 6, 6);
      canvas.print(bands.band1_right.c_str());
    }

    canvas.fillRect(0, 28, 240, 76, 0x1082);  // #0F172A
    canvas.setTextColor(0xFFDE, 0x1082);
    canvas.setTextSize(2);
    canvas.setCursor(10, 38);
    canvas.print(bands.band2_line1.c_str());
    if (!bands.band2_line2.empty()) {
      canvas.setCursor(10, 54);
      canvas.print(bands.band2_line2.c_str());
    }
    canvas.setTextSize(1);

    canvas.fillRect(0, 104, 240, 1, 0x3166);
    canvas.fillRect(0, 105, 240, 30, 0x3166);  // #1E293B approx
    canvas.setTextColor(bands.band3_fg_rgb565, 0x3166);
    canvas.setCursor(10, 111);
    canvas.print(bands.band3.c_str());

    canvas.pushSprite(0, 0);
  }
};

HalDisplay* createPlatformDisplay() {
  static M5GfxDisplay display;
  return &display;
}

}  // namespace stockbot

#endif
