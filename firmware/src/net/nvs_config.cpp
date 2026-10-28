#include "nvs_config.h"

#ifdef ARDUINO

namespace stockbot {

class PreferencesNvsStore final : public NvsConfigStore {
 public:
  bool load(NvsConfig&) override { return false; }
  bool save(const NvsConfig&) override { return false; }
  void clearDeviceToken() override {}
};

}  // namespace stockbot

#endif
