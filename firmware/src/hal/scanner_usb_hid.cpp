#include "hal_scanner.h"

#ifdef ARDUINO

namespace stockbot {

class UsbHidScanner final : public HalScanner {
 public:
  void begin() override {}
  void poll(uint64_t, const std::function<void(const ScanLine&)>&) override {}
  void enable1DOnly(bool) override {}
  void setContinuousMode(bool) override {}
};

}  // namespace stockbot

#endif
