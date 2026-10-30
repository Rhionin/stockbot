#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#include "ring_buffer.h"

namespace stockbot {

struct DeviceHubConfigParsed {
  std::string label;
  std::string default_direction = "stock_out";
  uint32_t inactivity_revert_seconds = 300;
  std::string commit_mode = "queue";
  uint32_t config_version = 0;
};

struct PairCompleteParsed {
  std::string device_id;
  std::string device_token;
  std::string household_id;
  std::string household_name;
  DeviceHubConfigParsed config;
  uint32_t heartbeat_seconds = 60;
};

struct ParsedDeviceScanResponse {
  std::string id;
  std::string status;
  std::string scan_status;
  bool known = false;
  bool product_name_is_null = true;
  std::string product_name;
  std::string barcode;
  std::string direction;
  int queued_count = 0;
  int delta = 0;
  int on_hand = 0;
  bool backfilled = false;
  std::string code;
  bool unauthorized = false;
  bool parse_ok = false;
};

struct HeartbeatParsed {
  DeviceHubConfigParsed config;
  std::optional<std::string> rotated_device_token;
  bool parse_ok = false;
};

struct SetupQrParsed {
  bool ok = false;
  std::string ssid;
  std::string pass;
  std::string url;
  std::string code;
};

struct BatchScanResultItem {
  std::string id;
  std::string status;
};

std::string buildPairCompleteRequestJson(const std::string& code, const std::string& label,
                                         const std::string& hub_version, const std::string& boot_id);

bool parsePairCompleteResponseJson(int http_status, const std::string& body, PairCompleteParsed& out);

std::string buildSingleScanRequestJson(const RingScanRecord& record, const std::string& boot_id,
                                       uint64_t age_ms, bool time_approx);

std::string buildBatchScansRequestJson(const std::vector<RingScanRecord>& records,
                                       const std::string& boot_id, uint64_t now_ms);

bool parseDeviceScanResponseJson(int http_status, const std::string& body,
                                 ParsedDeviceScanResponse& out);

bool parseDeviceScanBatchResponseJson(int http_status, const std::string& body,
                                      std::vector<BatchScanResultItem>& out);

std::string buildHeartbeatRequestJson(const std::string& hub_version, const std::string& boot_id,
                                      uint32_t uptime_s, size_t queued, int battery_pct, bool charging,
                                      bool clock_synced);

bool parseHeartbeatResponseJson(int http_status, const std::string& body, HeartbeatParsed& out);

bool parseSetupQrPayload(const std::string& raw, SetupQrParsed& out);

bool parseApiErrorCode(const std::string& body, std::string& code_out);

bool pairingCompleteFailureIsExpired(bool transport_ok, int http_status, const std::string& body);
bool pairingCompleteFailureIsRateLimited(bool transport_ok, int http_status, const std::string& body);
bool pairingCompleteFailureIsClientRejected(bool transport_ok, int http_status, const std::string& body);

}  // namespace stockbot
