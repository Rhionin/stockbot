#pragma once

#include <string>

namespace stockbot {

class WifiStation {
 public:
  virtual ~WifiStation() = default;
  virtual void begin() = 0;
  virtual void connect(const std::string& ssid, const std::string& pass) = 0;
  virtual bool isConnected() const = 0;
  virtual void tick() = 0;
  virtual void disconnectStation() = 0;
};

#ifdef ARDUINO
WifiStation* createWifiStation();
#endif

}  // namespace stockbot
