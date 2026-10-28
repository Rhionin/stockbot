#pragma once

namespace stockbot {

class CaptivePortal {
 public:
  virtual ~CaptivePortal() = default;
  virtual void start(const char* ap_suffix_hex4) = 0;
  virtual void stop() = 0;
  virtual void loop() = 0;
};

}  // namespace stockbot
