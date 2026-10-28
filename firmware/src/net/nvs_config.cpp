#include "nvs_config.h"

#ifdef ARDUINO

#include <Preferences.h>

namespace stockbot {

namespace {

constexpr const char* kNs = "stockbot";

Direction loadDirection(const String& value) {
  return value == "stock_in" ? Direction::StockIn : Direction::StockOut;
}

CommitMode loadCommitMode(const String& value) {
  return value == "immediate" ? CommitMode::Immediate : CommitMode::Queue;
}

}  // namespace

class PreferencesNvsStore final : public NvsConfigStore {
 public:
  bool load(NvsConfig& out) override {
    Preferences prefs;
    if (!prefs.begin(kNs, true)) {
      return false;
    }
    out.wifi_ssid = prefs.getString("wifi_ssid", "").c_str();
    out.wifi_pass = prefs.getString("wifi_pass", "").c_str();
    out.server_url = prefs.getString("server_url", "https://stockbot.rhionin.com").c_str();
    out.device_token = prefs.getString("device_token", "").c_str();
    out.label = prefs.getString("label", "Storage Room Scanner").c_str();
    out.default_direction = loadDirection(prefs.getString("default_dir", "stock_out"));
    out.inactivity_revert_s = prefs.getUInt("inact_rev", 300);
    out.commit_mode = loadCommitMode(prefs.getString("commit_mode", "queue"));
    out.config_version = prefs.getUInt("cfg_ver", 0);
    out.boot_count = prefs.getUInt("boot_cnt", 0);
    prefs.end();
    return true;
  }

  bool save(const NvsConfig& cfg) override {
    Preferences prefs;
    if (!prefs.begin(kNs, false)) {
      return false;
    }
    prefs.putString("wifi_ssid", cfg.wifi_ssid.c_str());
    prefs.putString("wifi_pass", cfg.wifi_pass.c_str());
    prefs.putString("server_url", cfg.server_url.c_str());
    prefs.putString("device_token", cfg.device_token.c_str());
    prefs.putString("label", cfg.label.c_str());
    prefs.putString("default_dir", cfg.default_direction == Direction::StockIn ? "stock_in" : "stock_out");
    prefs.putUInt("inact_rev", cfg.inactivity_revert_s);
    prefs.putString("commit_mode", cfg.commit_mode == CommitMode::Immediate ? "immediate" : "queue");
    prefs.putUInt("cfg_ver", cfg.config_version);
    prefs.putUInt("boot_cnt", cfg.boot_count);
    prefs.end();
    return true;
  }

  void clearDeviceToken() override {
    Preferences prefs;
    if (prefs.begin(kNs, false)) {
      prefs.remove("device_token");
      prefs.end();
    }
  }

  void factoryReset() override {
    Preferences prefs;
    if (prefs.begin(kNs, false)) {
      prefs.clear();
      prefs.end();
    }
  }
};

PreferencesNvsStore* createNvsConfigStore() {
  static PreferencesNvsStore store;
  return &store;
}

}  // namespace stockbot

#endif
