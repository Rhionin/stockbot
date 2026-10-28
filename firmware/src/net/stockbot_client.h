#pragma once

#include <cstdint>
#include <string>
#include <vector>

#include "protocol_json.h"
#include "ring_buffer.h"

namespace stockbot {

struct HttpCallResult {
  bool transport_ok = false;
  int http_status = 0;
  std::string body;
};

class StockbotClient {
 public:
  virtual ~StockbotClient() = default;
  virtual void setServerUrl(const std::string& url) = 0;
  virtual void setDeviceToken(const std::string& token) = 0;
  virtual HttpCallResult completePairing(const std::string& code, const std::string& label,
                                         const std::string& hub_version, const std::string& boot_id,
                                         PairCompleteParsed& out) = 0;
  virtual HttpCallResult sendScan(const struct RingScanRecord& record, const std::string& boot_id,
                                  uint64_t age_ms, ParsedDeviceScanResponse& out) = 0;
  virtual HttpCallResult flushBatch(const std::vector<struct RingScanRecord>& batch,
                                    const std::string& boot_id, ParsedDeviceScanResponse& last_out) = 0;
  virtual HttpCallResult sendHeartbeat(const std::string& hub_version, const std::string& boot_id,
                                       uint32_t uptime_s, size_t queued, int battery_pct, bool charging,
                                       HeartbeatParsed& out) = 0;
};

#ifdef ARDUINO
StockbotClient* createStockbotClient();
#endif

}  // namespace stockbot
