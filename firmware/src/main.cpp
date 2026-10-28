#ifdef ARDUINO

#include <M5Unified.h>

#include "board_pins.h"
#include "captive_portal.h"
#include "hal_display.h"
#include "hal_feedback.h"
#include "hal_power.h"
#include "hal_scanner.h"
#include "storage/littlefs_ring_storage.h"
#include "nvs_config.h"
#include "scanner_controller.h"
#include "stockbot_client.h"

using namespace stockbot;

static LittleFsRingStorage g_ring_storage;
static RingBuffer g_ring(g_ring_storage);

static HalScanner* g_scanner = nullptr;
static HalDisplay* g_display = nullptr;
static HalFeedback* g_feedback = nullptr;
static HalPower* g_power = nullptr;
static NvsConfigStore* g_nvs = nullptr;
static CaptivePortal* g_portal = nullptr;
static StockbotClient* g_client = nullptr;
static ScannerController* g_controller = nullptr;

static bool g_btn_mode_down = false;
static uint64_t g_btn_mode_down_ms = 0;
static bool g_btn_trigger_down = false;

void setup() {
  g_ring_storage.begin();
  g_scanner = createPlatformScanner();
  g_display = createPlatformDisplay();
  g_feedback = createPlatformFeedback();
  g_power = createPlatformPower();
  g_nvs = createNvsConfigStore();
  g_portal = createCaptivePortal();
  g_client = createStockbotClient();

  static ScannerController controller(*g_scanner, *g_display, *g_feedback, *g_power, *g_nvs, *g_portal,
                                      *g_client, g_ring);
  g_controller = &controller;
  g_controller->begin(millis());

  pinMode(PIN_BTN_MODE, INPUT_PULLUP);
  pinMode(PIN_BTN_TRIGGER, INPUT_PULLUP);
}

void loop() {
  const uint64_t now = millis();

  const bool mode_pressed = digitalRead(PIN_BTN_MODE) == LOW;
  if (mode_pressed && !g_btn_mode_down) {
    g_btn_mode_down = true;
    g_btn_mode_down_ms = now;
  }
  if (!mode_pressed && g_btn_mode_down) {
    const uint64_t held = now - g_btn_mode_down_ms;
    if (held < 5000) {
      g_controller->onModeButtonShortPress(now);
    } else {
      g_controller->onModeButtonHold(held, now);
    }
    g_btn_mode_down = false;
  }
  if (mode_pressed && g_btn_mode_down && now - g_btn_mode_down_ms >= 10000) {
    g_controller->onModeButtonHold(now - g_btn_mode_down_ms, now);
    g_btn_mode_down = false;
  }

  const bool trigger_pressed = digitalRead(PIN_BTN_TRIGGER) == LOW;
  if (trigger_pressed && !g_btn_trigger_down) {
    g_btn_trigger_down = true;
  }
  if (!trigger_pressed && g_btn_trigger_down) {
    g_btn_trigger_down = false;
  }

  g_controller->tick(now);
  delay(5);
}

#else

int main() {
  return 0;
}

#endif
