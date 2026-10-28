#pragma once

#include <cstdint>
#include <functional>
#include <optional>
#include <string>

namespace stockbot {

struct ScanLine {
  std::string barcode;
  uint64_t timestamp_ms = 0;
};

class HalScanner {
 public:
  virtual ~HalScanner() = default;
  virtual void begin() = 0;
  virtual void poll(uint64_t now_ms, const std::function<void(const ScanLine&)>& on_scan) = 0;
  virtual void enable1DOnly(bool enabled) = 0;
  virtual void setContinuousMode(bool enabled) = 0;
};

}  // namespace stockbot
