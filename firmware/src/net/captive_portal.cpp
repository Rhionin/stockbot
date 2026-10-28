#include "captive_portal.h"

#ifdef ARDUINO

#include <DNSServer.h>
#include <WebServer.h>
#include <WiFi.h>

namespace stockbot {

namespace {

const char kSetupHtml[] = R"HTML(<!DOCTYPE html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Stockbot Scanner Setup</title></head><body><h1>Stockbot Scanner</h1>
<form method="POST" action="/save"><label>Wi-Fi SSID<input name="ssid" required></label><br>
<label>Wi-Fi Password<input name="pass" type="password"></label><br>
<label>Stockbot URL<input name="url" value="https://stockbot.rhionin.com"></label><br>
<label>Pairing code (optional)<input name="code" placeholder="SBP-...."></label><br>
<button type="submit">Save</button></form></body></html>)HTML";

}  // namespace

class StockbotCaptivePortal final : public CaptivePortal {
 public:
  void start(const std::string& ap_suffix) override {
    suffix_ = ap_suffix;
    const std::string ssid = "Stockbot-Scanner-" + suffix_;
    WiFi.mode(WIFI_AP);
    WiFi.softAP(ssid.c_str());
    dns_.start(53, "*", WiFi.softAPIP());
    server_.on("/", HTTP_GET, [this]() { server_.send(200, "text/html", kSetupHtml); });
    server_.on("/save", HTTP_POST, [this]() { handleSave(); });
    server_.onNotFound([this]() {
      server_.sendHeader("Location", "http://192.168.4.1/", true);
      server_.send(302, "text/plain", "");
    });
    server_.begin();
    running_ = true;
  }

  void stop() override {
    if (!running_) {
      return;
    }
    server_.stop();
    dns_.stop();
    WiFi.softAPdisconnect(true);
    running_ = false;
  }

  void loop() override {
    if (!running_) {
      return;
    }
    dns_.processNextRequest();
    server_.handleClient();
  }

  bool takeSubmission(CaptivePortalSubmission& out) override {
    if (!pending_) {
      return false;
    }
    out = pending_submission_;
    pending_ = false;
    out.ready = true;
    return true;
  }

 private:
  WebServer server_{80};
  DNSServer dns_;
  bool running_ = false;
  bool pending_ = false;
  CaptivePortalSubmission pending_submission_;
  std::string suffix_;

  void handleSave() {
    pending_submission_ = CaptivePortalSubmission{};
    if (server_.hasArg("ssid")) {
      pending_submission_.wifi_ssid = server_.arg("ssid").c_str();
    }
    if (server_.hasArg("pass")) {
      pending_submission_.wifi_pass = server_.arg("pass").c_str();
    }
    if (server_.hasArg("url")) {
      pending_submission_.server_url = server_.arg("url").c_str();
    }
    if (server_.hasArg("code")) {
      pending_submission_.pairing_code = server_.arg("code").c_str();
    }
    pending_ = true;
    server_.send(200, "text/html", "<html><body>Saved. Rebooting Wi-Fi…</body></html>");
  }
};

CaptivePortal* createCaptivePortal() {
  static StockbotCaptivePortal portal;
  return &portal;
}

}  // namespace stockbot

#endif
