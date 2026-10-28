#include "stockbot_client.h"

#ifdef ARDUINO

namespace stockbot {

class HttpsStockbotClient final : public StockbotClient {
 public:
  bool completePairing(const std::string&, const std::string&) override { return false; }
  bool sendScan(const RingScanRecord&, DeviceScanResponse&) override { return false; }
  bool flushBatch(const std::vector<RingScanRecord>&) override { return false; }
  bool sendHeartbeat(size_t, int, bool) override { return false; }
};

}  // namespace stockbot

#endif
