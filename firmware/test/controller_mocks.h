#pragma once

#include <cstdio>
#include <cstring>
#include <deque>
#include <functional>
#include <string>
#include <vector>

#include "captive_portal.h"
#include "hal_display.h"
#include "hal_feedback.h"
#include "hal_power.h"
#include "hal_scanner.h"
#include "nvs_config.h"
#include "protocol_json.h"
#include "ring_buffer.h"
#include "stockbot_client.h"
#include "wifi_station.h"

struct HttpRequestRecord {
  std::string kind;
  uint64_t at_ms = 0;
  int http_status = 0;
  bool transport_ok = false;
};

struct MockHttpScriptEntry {
  stockbot::HttpCallResult result;
  stockbot::PairCompleteParsed pair_body;
  stockbot::ParsedDeviceScanResponse scan_body;
  stockbot::HeartbeatParsed hb_body;
};

class MemoryRingStorage : public stockbot::RingStorage {
 public:
  bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
    out = records_;
    return true;
  }

  bool writeAll(const std::vector<stockbot::RingScanRecord>& records) override {
    records_ = records;
    return true;
  }

 private:
  std::vector<stockbot::RingScanRecord> records_;
};

class FailClearRingStorage : public stockbot::RingStorage {
 public:
  MemoryRingStorage inner;
  bool fail_empty_write = false;

  bool readAll(std::vector<stockbot::RingScanRecord>& out) override { return inner.readAll(out); }

  bool writeAll(const std::vector<stockbot::RingScanRecord>& records) override {
    if (fail_empty_write && records.empty()) {
      return false;
    }
    return inner.writeAll(records);
  }
};

class MockScanner : public stockbot::HalScanner {
 public:
  int one_d_calls = 0;
  bool one_d_enabled = false;
  bool continuous = false;
  void begin() override {}
  void poll(uint64_t, const std::function<void(const stockbot::ScanLine&)>&) override {}
  void enable1DOnly(bool enabled) override {
    ++one_d_calls;
    one_d_enabled = enabled;
  }
  void setContinuousMode(bool enabled) override { continuous = enabled; }
};

class MockDisplay : public stockbot::HalDisplay {
 public:
  stockbot::DisplayBands last;
  int render_calls = 0;
  void begin() override {}
  void render(const stockbot::DisplayBands& bands) override {
    last = bands;
    ++render_calls;
  }
  void setBacklight(stockbot::BacklightLevel) override {}
};

class MockFeedback : public stockbot::HalFeedback {
 public:
  std::vector<stockbot::AudioCue> cues;
  void begin() override {}
  void play(stockbot::AudioCue cue) override { cues.push_back(cue); }
  void flashLedForScan(bool) override {}
};

class MockPower : public stockbot::HalPower {
 public:
  stockbot::PowerStatus status;
  void begin() override {}
  stockbot::PowerStatus read() override { return status; }
  void setGrovePower(bool) override {}
  void enterDeepSleepWakeOnButton() override {}
};

class MockNvs : public stockbot::NvsConfigStore {
 public:
  stockbot::NvsConfig cfg;
  int factory_reset_calls = 0;
  bool load(stockbot::NvsConfig& out) override {
    out = cfg;
    return true;
  }
  bool save(const stockbot::NvsConfig& in) override {
    cfg = in;
    return true;
  }
  void clearDeviceToken() override { cfg.device_token.clear(); }
  void factoryReset() override {
    ++factory_reset_calls;
    cfg = stockbot::NvsConfig{};
  }
};

class MockPortal : public stockbot::CaptivePortal {
 public:
  bool running = false;
  stockbot::CaptivePortalSubmission pending;
  void start(const std::string&) override { running = true; }
  void stop() override { running = false; }
  void loop() override {}
  bool takeSubmission(stockbot::CaptivePortalSubmission& out) override {
    if (!pending.ready) {
      return false;
    }
    out = pending;
    pending.ready = false;
    return true;
  }
};

class MockWifi : public stockbot::WifiStation {
 public:
  std::string last_ssid;
  int connect_calls = 0;
  bool connected = false;
  void begin() override {}
  void connect(const std::string& ssid, const std::string&) override {
    last_ssid = ssid;
    ++connect_calls;
    connected = !ssid.empty();
  }
  bool isConnected() const override { return connected; }
  void tick() override {}
  void disconnectStation() override {
    connected = false;
    last_ssid.clear();
  }
};

class MockClient : public stockbot::StockbotClient {
 public:
  std::string token;
  stockbot::HttpCallResult next_pair;
  stockbot::PairCompleteParsed pair_body;
  stockbot::HttpCallResult next_scan;
  stockbot::ParsedDeviceScanResponse scan_body;
  stockbot::HttpCallResult next_hb;
  stockbot::HeartbeatParsed hb_body;

  std::deque<MockHttpScriptEntry> pair_queue;
  std::deque<MockHttpScriptEntry> scan_queue;
  std::deque<MockHttpScriptEntry> hb_queue;

  uint64_t* now_ms_ptr = nullptr;
  std::vector<HttpRequestRecord> http_log;

  void clearHttpLog() { http_log.clear(); }

  void logHttp(const char* kind, const stockbot::HttpCallResult& res) {
    if (now_ms_ptr != nullptr) {
      http_log.push_back({kind, *now_ms_ptr, res.http_status, res.transport_ok});
    }
  }

  MockHttpScriptEntry popPair() {
    if (!pair_queue.empty()) {
      MockHttpScriptEntry out = pair_queue.front();
      pair_queue.pop_front();
      return out;
    }
    MockHttpScriptEntry e;
    e.result = next_pair;
    e.pair_body = pair_body;
    return e;
  }

  MockHttpScriptEntry popScan() {
    if (!scan_queue.empty()) {
      MockHttpScriptEntry out = scan_queue.front();
      scan_queue.pop_front();
      return out;
    }
    MockHttpScriptEntry e;
    e.result = next_scan;
    e.scan_body = scan_body;
    return e;
  }

  MockHttpScriptEntry popHb() {
    if (!hb_queue.empty()) {
      MockHttpScriptEntry out = hb_queue.front();
      hb_queue.pop_front();
      return out;
    }
    MockHttpScriptEntry e;
    e.result = next_hb;
    e.hb_body = hb_body;
    return e;
  }

  void setServerUrl(const std::string&) override {}
  void setDeviceToken(const std::string& t) override { token = t; }

  stockbot::HttpCallResult completePairing(const std::string&, const std::string&, const std::string&,
                                           const std::string&, stockbot::PairCompleteParsed& out) override {
    const MockHttpScriptEntry scripted = popPair();
    out = scripted.pair_body;
    logHttp("pair", scripted.result);
    return scripted.result;
  }

  stockbot::HttpCallResult sendScan(const stockbot::RingScanRecord& rec, const std::string&, uint64_t age_ms,
                                    bool time_approx, stockbot::ParsedDeviceScanResponse& out) override {
    last_send_barcode = rec.barcode;
    last_send_scan_id = rec.client_scan_id;
    last_send_age_ms = age_ms;
    last_send_time_approx = time_approx;
    send_order.push_back(rec.barcode);
    const MockHttpScriptEntry scripted = popScan();
    out = scripted.scan_body;
    logHttp("scan", scripted.result);
    return scripted.result;
  }

  std::string last_send_barcode;
  std::string last_send_scan_id;
  uint64_t last_send_age_ms = 0;
  bool last_send_time_approx = false;
  std::vector<std::string> send_order;

  stockbot::HttpCallResult flushBatch(const std::vector<stockbot::RingScanRecord>& batch,
                                      const std::string& boot_id, uint64_t now_ms,
                                      stockbot::ParsedDeviceScanResponse& out) override {
    last_flush_batch = batch;
    last_flush_boot = boot_id;
    last_flush_now_ms = now_ms;
    last_flush_body = stockbot::buildBatchScansRequestJson(batch, boot_id, now_ms);
    const MockHttpScriptEntry scripted = popScan();
    out = scripted.scan_body;
    logHttp("flush", scripted.result);
    return scripted.result;
  }

  std::vector<stockbot::RingScanRecord> last_flush_batch;
  std::string last_flush_boot;
  uint64_t last_flush_now_ms = 0;
  std::string last_flush_body;
  std::string last_heartbeat_body;

  stockbot::HttpCallResult sendHeartbeat(const std::string& hub_version, const std::string& boot_id,
                                         uint32_t uptime_s, size_t queued, int battery_pct, bool charging,
                                         stockbot::HeartbeatParsed& out) override {
    ++send_heartbeat_calls;
    last_heartbeat_body =
        stockbot::buildHeartbeatRequestJson(hub_version, boot_id, uptime_s, queued, battery_pct, charging,
                                            true);
    const MockHttpScriptEntry scripted = popHb();
    out = scripted.hb_body;
    logHttp("heartbeat", scripted.result);
    return scripted.result;
  }

  int send_heartbeat_calls = 0;
};
