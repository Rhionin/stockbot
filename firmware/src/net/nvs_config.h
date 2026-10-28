#pragma once

#include <string>

#include "scanner_state.h"

namespace stockbot {

struct NvsConfig {
  std::string wifi_ssid;
  std::string wifi_pass;
  std::string server_url = "https://stockbot.rhionin.com";
  std::string device_token;
  std::string label = "Storage Room Scanner";
  Direction default_direction = Direction::StockOut;
  uint32_t inactivity_revert_s = 300;
  CommitMode commit_mode = CommitMode::Queue;
  uint32_t config_version = 0;
};

class NvsConfigStore {
 public:
  virtual ~NvsConfigStore() = default;
  virtual bool load(NvsConfig& out) = 0;
  virtual bool save(const NvsConfig& cfg) = 0;
  virtual void clearDeviceToken() = 0;
  virtual void factoryReset() = 0;
};

#ifdef ARDUINO
NvsConfigStore* createNvsConfigStore();
#endif

}  // namespace stockbot
