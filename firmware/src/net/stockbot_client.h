#pragma once

#include <string>
#include <vector>

#include "ring_buffer.h"
#include "scanner_state.h"

namespace stockbot {

struct DeviceScanResponse {
  std::string status;
  std::string scan_status;
  bool known = false;
  std::string product_name;
  std::string barcode;
  Direction direction = Direction::StockOut;
  int queued_count = 0;
  int delta = 0;
  int on_hand = 0;
  bool backfilled = false;
};

class StockbotClient {
 public:
  virtual ~StockbotClient() = default;
  virtual bool completePairing(const std::string& code, const std::string& label) = 0;
  virtual bool sendScan(const RingScanRecord& record, DeviceScanResponse& out) = 0;
  virtual bool flushBatch(const std::vector<RingScanRecord>& batch) = 0;
  virtual bool sendHeartbeat(size_t queued, int battery_pct, bool charging) = 0;
};

}  // namespace stockbot
