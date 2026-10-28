#include "stockbot_client.h"

#ifdef ARDUINO

#include <HTTPClient.h>
#include <WiFiClientSecure.h>

#include "protocol_json.h"

namespace stockbot {

namespace {

WiFiClientSecure& tlsClient() {
  static WiFiClientSecure client;
  return client;
}

HttpCallResult performPost(const std::string& base_url, const std::string& path, const std::string& body,
                           const std::string& bearer_token) {
  HttpCallResult result;
  HTTPClient http;
  WiFiClient* stream = nullptr;
  WiFiClient plain;
  const bool secure = base_url.rfind("https://", 0) == 0;
  if (secure) {
    tlsClient().setInsecure();
    stream = &tlsClient();
  } else {
    stream = &plain;
  }
  const std::string url = base_url + path;
  if (!http.begin(*stream, url.c_str())) {
    return result;
  }
  http.addHeader("Content-Type", "application/json");
  if (!bearer_token.empty()) {
    http.addHeader("Authorization", ("Bearer " + bearer_token).c_str());
  }
  const int code = http.POST(body.c_str());
  result.transport_ok = code > 0;
  result.http_status = code;
  if (code > 0) {
    result.body = http.getString().c_str();
  }
  http.end();
  return result;
}

}  // namespace

class HttpsStockbotClient final : public StockbotClient {
 public:
  void setServerUrl(const std::string& url) override { base_url_ = url; }
  void setDeviceToken(const std::string& token) override { token_ = token; }

  HttpCallResult completePairing(const std::string& code, const std::string& label,
                                 const std::string& hub_version, const std::string& boot_id,
                                 PairCompleteParsed& out) override {
    const std::string body = buildPairCompleteRequestJson(code, label, hub_version, boot_id);
    const HttpCallResult res = performPost(base_url_, "/api/devices/pair/complete", body, "");
    if (res.transport_ok) {
      parsePairCompleteResponseJson(res.http_status, res.body, out);
    }
    return res;
  }

  HttpCallResult sendScan(const RingScanRecord& record, const std::string& boot_id, uint64_t age_ms,
                          ParsedDeviceScanResponse& out) override {
    const std::string body = buildSingleScanRequestJson(record, boot_id, age_ms, age_ms > 0);
    const HttpCallResult res = performPost(base_url_, "/api/device/scans", body, token_);
    if (res.transport_ok) {
      parseDeviceScanResponseJson(res.http_status, res.body, out);
    }
    return res;
  }

  HttpCallResult flushBatch(const std::vector<RingScanRecord>& batch, const std::string& boot_id,
                            ParsedDeviceScanResponse& last_out) override {
    const std::string body = buildBatchScansRequestJson(batch, boot_id, 0);
    const HttpCallResult res = performPost(base_url_, "/api/device/scans", body, token_);
    if (res.transport_ok && !batch.empty()) {
      parseDeviceScanResponseJson(res.http_status, res.body, last_out);
    }
    return res;
  }

  HttpCallResult sendHeartbeat(const std::string& hub_version, const std::string& boot_id,
                               uint32_t uptime_s, size_t queued, int battery_pct, bool charging,
                               HeartbeatParsed& out) override {
    const std::string body =
        buildHeartbeatRequestJson(hub_version, boot_id, uptime_s, queued, battery_pct, charging, true);
    const HttpCallResult res = performPost(base_url_, "/api/device/heartbeat", body, token_);
    if (res.transport_ok) {
      parseHeartbeatResponseJson(res.http_status, res.body, out);
    }
    return res;
  }

 private:
  std::string base_url_ = "https://stockbot.rhionin.com";
  std::string token_;
};

StockbotClient* createStockbotClient() {
  static HttpsStockbotClient client;
  return &client;
}

}  // namespace stockbot

#endif
