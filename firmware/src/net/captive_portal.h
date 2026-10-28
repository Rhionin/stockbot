#pragma once

#include <string>

namespace stockbot {

struct CaptivePortalSubmission {
  bool ready = false;
  std::string wifi_ssid;
  std::string wifi_pass;
  std::string server_url;
  std::string pairing_code;
};

class CaptivePortal {
 public:
  virtual ~CaptivePortal() = default;
  virtual void start(const std::string& ap_suffix) = 0;
  virtual void stop() = 0;
  virtual void loop() = 0;
  virtual bool takeSubmission(CaptivePortalSubmission& out) = 0;
};

#ifdef ARDUINO
CaptivePortal* createCaptivePortal();
#endif

}  // namespace stockbot
