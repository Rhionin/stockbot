#include "hal_scanner.h"

#ifdef ARDUINO

#include "hid_keycode.h"

namespace stockbot {

namespace {

std::function<void(const ScanLine&)> g_hid_scan_callback;
HidKeycodeDecoder g_hid_decoder;

class UsbHidScanner final : public HalScanner {
 public:
  void begin() override { g_hid_decoder.resetLine(); }

  void enable1DOnly(bool) override {}

  void setContinuousMode(bool) override {}

  void poll(uint64_t now_ms, const std::function<void(const ScanLine&)>& on_scan) override {
    g_hid_scan_callback = on_scan;
    (void)now_ms;
  }
};

UsbHidScanner g_usb_hid;

}  // namespace

HalScanner* createUsbHidScanner() { return &g_usb_hid; }

void stockbotOnHidKeyboardReport(const uint8_t report[8], uint64_t now_ms) {
  if (!g_hid_scan_callback) {
    return;
  }
  if (auto line = g_hid_decoder.consumeReport(report)) {
    g_hid_scan_callback(ScanLine{*line, now_ms});
  }
}

}  // namespace stockbot

#endif
