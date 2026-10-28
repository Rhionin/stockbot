#include "wifi_station.h"

#ifdef ARDUINO

#include <WiFi.h>

namespace stockbot {

class ArduinoWifiStation final : public WifiStation {
 public:
  void begin() override { WiFi.persistent(false); }

  void connect(const std::string& ssid, const std::string& pass) override {
    ssid_ = ssid;
    pass_ = pass;
    want_connected_ = true;
    last_attempt_ms_ = 0;
    if (ssid_.empty()) {
      return;
    }
    WiFi.mode(WIFI_STA);
    WiFi.begin(ssid_.c_str(), pass_.c_str());
  }

  bool isConnected() const override { return WiFi.status() == WL_CONNECTED; }

  void tick() override {
    if (!want_connected_ || ssid_.empty()) {
      return;
    }
    if (isConnected()) {
      return;
    }
    const uint32_t now = millis();
    if (now - last_attempt_ms_ < 5000) {
      return;
    }
    last_attempt_ms_ = now;
    WiFi.disconnect(false, false);
    WiFi.mode(WIFI_STA);
    WiFi.begin(ssid_.c_str(), pass_.c_str());
  }

  void disconnectStation() override {
    want_connected_ = false;
    WiFi.disconnect(true, true);
  }

 private:
  std::string ssid_;
  std::string pass_;
  bool want_connected_ = false;
  uint32_t last_attempt_ms_ = 0;
};

WifiStation* createWifiStation() {
  static ArduinoWifiStation station;
  return &station;
}

}  // namespace stockbot

#endif
