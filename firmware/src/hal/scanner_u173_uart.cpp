#include "hal_scanner.h"

#ifdef ARDUINO

#include <HardwareSerial.h>

#include "board_pins.h"

namespace stockbot {

namespace {

class U173UartScanner final : public HalScanner {
 public:
  void begin() override {
    Serial1.begin(115200, SERIAL_8N1, PIN_GROVE_G2, -1);
    pinMode(PIN_GROVE_G1, INPUT_PULLUP);
  }

  void enable1DOnly(bool enabled) override {
    Serial1.print(enabled ? "SET_1D_ONLY\n" : "SET_ALL_SYM\n");
  }

  void setContinuousMode(bool) override {}

  void poll(uint64_t now_ms, const std::function<void(const ScanLine&)>& on_scan) override {
    if (digitalRead(PIN_GROVE_G1) == LOW) {
      Serial1.print("TRIG\n");
    }
    while (Serial1.available()) {
      const char ch = static_cast<char>(Serial1.read());
      if (ch == '\r') {
        continue;
      }
      if (ch == '\n') {
        if (!line_.empty()) {
          on_scan(ScanLine{line_, now_ms});
          line_.clear();
        }
        continue;
      }
      line_.push_back(ch);
    }
  }

 private:
  std::string line_;
};

U173UartScanner g_u173_uart;

}  // namespace

HalScanner* createU173UartScanner() { return &g_u173_uart; }

}  // namespace stockbot

#endif
