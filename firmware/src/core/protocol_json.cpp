#include "protocol_json.h"

#include <cctype>
#include <optional>
#include <sstream>

#include "scanner_state.h"
#include "text_util.h"

namespace stockbot {

namespace {

std::string jsonEscape(const std::string& in) {
  std::string out;
  out.reserve(in.size() + 8);
  for (char ch : in) {
    switch (ch) {
      case '"':
        out += "\\\"";
        break;
      case '\\':
        out += "\\\\";
        break;
      case '\n':
        out += "\\n";
        break;
      case '\r':
        out += "\\r";
        break;
      default:
        out.push_back(ch);
        break;
    }
  }
  return out;
}

std::optional<std::string> jsonStringAtKey(const std::string& body, const std::string& key) {
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  pos = body.find(':', pos + needle.size());
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  ++pos;
  while (pos < body.size() && std::isspace(static_cast<unsigned char>(body[pos]))) {
    ++pos;
  }
  if (pos >= body.size()) {
    return std::nullopt;
  }
  if (body.compare(pos, 4, "null") == 0) {
    return std::string();
  }
  if (body[pos] != '"') {
    return std::nullopt;
  }
  ++pos;
  std::string out;
  while (pos < body.size() && body[pos] != '"') {
    if (body[pos] == '\\' && pos + 1 < body.size()) {
      out.push_back(body[pos + 1]);
      pos += 2;
      continue;
    }
    out.push_back(body[pos++]);
  }
  return out;
}

std::optional<int64_t> jsonIntAtKey(const std::string& body, const std::string& key) {
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  pos = body.find(':', pos + needle.size());
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  ++pos;
  while (pos < body.size() && std::isspace(static_cast<unsigned char>(body[pos]))) {
    ++pos;
  }
  size_t end = pos;
  while (end < body.size() && (std::isdigit(static_cast<unsigned char>(body[end])) || body[end] == '-')) {
    ++end;
  }
  if (end == pos) {
    return std::nullopt;
  }
  return std::stoll(body.substr(pos, end - pos));
}

std::optional<bool> jsonBoolAtKey(const std::string& body, const std::string& key) {
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  pos = body.find(':', pos + needle.size());
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  ++pos;
  while (pos < body.size() && std::isspace(static_cast<unsigned char>(body[pos]))) {
    ++pos;
  }
  if (body.compare(pos, 4, "true") == 0) {
    return true;
  }
  if (body.compare(pos, 5, "false") == 0) {
    return false;
  }
  return std::nullopt;
}

std::optional<std::string> jsonObjectAtKey(const std::string& body, const std::string& key) {
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  pos = body.find('{', pos);
  if (pos == std::string::npos) {
    return std::nullopt;
  }
  int depth = 0;
  for (size_t i = pos; i < body.size(); ++i) {
    if (body[i] == '{') {
      ++depth;
    } else if (body[i] == '}') {
      --depth;
      if (depth == 0) {
        return body.substr(pos, i - pos + 1);
      }
    }
  }
  return std::nullopt;
}

void parseHubConfigObject(const std::string& config_json, DeviceHubConfigParsed& cfg) {
  if (auto label = jsonStringAtKey(config_json, "label")) {
    cfg.label = *label;
  }
  if (auto dir = jsonStringAtKey(config_json, "defaultDirection")) {
    cfg.default_direction = *dir;
  }
  if (auto sec = jsonIntAtKey(config_json, "inactivityRevertSeconds")) {
    cfg.inactivity_revert_seconds = static_cast<uint32_t>(*sec);
  }
  if (auto idle = jsonIntAtKey(config_json, "idleSeconds")) {
    if (cfg.inactivity_revert_seconds == 300) {
      cfg.inactivity_revert_seconds = static_cast<uint32_t>(*idle);
    }
  }
  if (auto mode = jsonStringAtKey(config_json, "commitMode")) {
    cfg.commit_mode = *mode;
  }
  if (auto ver = jsonIntAtKey(config_json, "configVersion")) {
    cfg.config_version = static_cast<uint32_t>(*ver);
  }
}

std::string directionToString(Direction direction) {
  return direction == Direction::StockIn ? "stock_in" : "stock_out";
}

}  // namespace

std::string buildPairCompleteRequestJson(const std::string& code, const std::string& label,
                                         const std::string& hub_version, const std::string& boot_id) {
  std::ostringstream oss;
  oss << "{"
      << "\"code\":\"" << jsonEscape(code) << "\","
      << "\"label\":\"" << jsonEscape(label) << "\","
      << "\"hub\":{"
      << "\"kind\":\"esp32_scanner\","
      << "\"version\":\"" << jsonEscape(hub_version) << "\","
      << "\"bootId\":\"" << jsonEscape(boot_id) << "\","
      << "\"label\":\"" << jsonEscape(label) << "\""
      << "}"
      << "}";
  return oss.str();
}

bool parsePairCompleteResponseJson(int http_status, const std::string& body, PairCompleteParsed& out) {
  out = PairCompleteParsed{};
  if (http_status == 401) {
    return false;
  }
  if (http_status != 201 && http_status != 200) {
    return false;
  }
  if (auto device_id = jsonStringAtKey(body, "deviceId")) {
    out.device_id = *device_id;
  }
  if (auto token = jsonStringAtKey(body, "token")) {
    out.device_token = *token;
  }
  if (auto household = jsonStringAtKey(body, "householdId")) {
    out.household_id = *household;
  }
  if (auto hb = jsonIntAtKey(body, "heartbeatSeconds")) {
    out.heartbeat_seconds = static_cast<uint32_t>(*hb);
  }
  if (auto cfg = jsonObjectAtKey(body, "config")) {
    parseHubConfigObject(*cfg, out.config);
  }
  return !out.device_token.empty();
}

std::string buildSingleScanRequestJson(const RingScanRecord& record, const std::string& boot_id,
                                       uint64_t age_ms, bool time_approx) {
  Direction direction = record.direction == 1 ? Direction::StockIn : Direction::StockOut;
  std::ostringstream oss;
  oss << "{"
      << "\"id\":\"" << jsonEscape(record.client_scan_id) << "\","
      << "\"barcode\":\"" << jsonEscape(record.barcode) << "\","
      << "\"direction\":\"" << directionToString(direction) << "\","
      << "\"unitCount\":1,"
      << "\"ageMs\":" << age_ms << ","
      << "\"bootId\":\"" << jsonEscape(boot_id) << "\"";
  if (time_approx) {
    oss << ",\"timeApprox\":true";
  }
  oss << "}";
  return oss.str();
}

std::string buildBatchScansRequestJson(const std::vector<RingScanRecord>& records,
                                       const std::string& boot_id, uint64_t age_ms_base) {
  std::ostringstream oss;
  oss << "{\"scans\":[";
  for (size_t i = 0; i < records.size(); ++i) {
    if (i > 0) {
      oss << ',';
    }
    oss << buildSingleScanRequestJson(records[i], boot_id, age_ms_base + i, false);
  }
  oss << "]}";
  return oss.str();
}

static void parseEnrichedScanFields(const std::string& body, ParsedDeviceScanResponse& out) {
  if (auto scan_status = jsonStringAtKey(body, "scanStatus")) {
    out.scan_status = *scan_status;
  }
  if (auto known = jsonBoolAtKey(body, "known")) {
    out.known = *known;
  }
  if (body.find("\"productName\":null") != std::string::npos) {
    out.product_name_is_null = true;
  } else if (auto name = jsonStringAtKey(body, "productName")) {
    out.product_name_is_null = false;
    out.product_name = *name;
  }
  if (auto barcode = jsonStringAtKey(body, "barcode")) {
    out.barcode = *barcode;
  }
  if (auto direction = jsonStringAtKey(body, "direction")) {
    out.direction = *direction;
  }
  if (auto queued = jsonIntAtKey(body, "queuedCount")) {
    out.queued_count = static_cast<int>(*queued);
  }
  if (auto delta = jsonIntAtKey(body, "delta")) {
    out.delta = static_cast<int>(*delta);
  }
  if (auto on_hand = jsonIntAtKey(body, "onHand")) {
    out.on_hand = static_cast<int>(*on_hand);
  }
  if (auto backfilled = jsonBoolAtKey(body, "backfilled")) {
    out.backfilled = *backfilled;
  }
  if (auto code = jsonStringAtKey(body, "code")) {
    out.code = *code;
  }
}

bool parseDeviceScanResponseJson(int http_status, const std::string& body,
                                 ParsedDeviceScanResponse& out) {
  out = ParsedDeviceScanResponse{};
  out.unauthorized = (http_status == 401);
  if (out.unauthorized) {
    return true;
  }
  if (http_status < 200 || http_status >= 300) {
    return false;
  }

  std::string item = body;
  if (auto results_pos = body.find("\"results\""); results_pos != std::string::npos) {
    const size_t obj_start = body.find('{', results_pos);
    if (obj_start != std::string::npos) {
      item = body.substr(obj_start);
    }
  }

  if (auto id = jsonStringAtKey(item, "id")) {
    out.id = *id;
  }
  if (auto status = jsonStringAtKey(item, "status")) {
    out.status = *status;
  }
  parseEnrichedScanFields(item, out);
  out.parse_ok = !out.status.empty() || !out.id.empty();
  return out.parse_ok;
}

std::string buildHeartbeatRequestJson(const std::string& hub_version, const std::string& boot_id,
                                      uint32_t uptime_s, size_t queued, int battery_pct,
                                      bool charging, bool clock_synced) {
  std::ostringstream oss;
  oss << "{"
      << "\"version\":\"" << jsonEscape(hub_version) << "\","
      << "\"bootId\":\"" << jsonEscape(boot_id) << "\","
      << "\"uptimeS\":" << uptime_s << ","
      << "\"queued\":" << queued << ","
      << "\"clockSynced\":" << (clock_synced ? "true" : "false");
  if (battery_pct >= 0) {
    oss << ",\"batteryPct\":" << battery_pct << ",\"charging\":" << (charging ? "true" : "false");
  }
  oss << "}";
  return oss.str();
}

bool parseHeartbeatResponseJson(int http_status, const std::string& body, HeartbeatParsed& out) {
  out = HeartbeatParsed{};
  if (http_status == 401) {
    out.parse_ok = true;
    return true;
  }
  if (http_status < 200 || http_status >= 300) {
    return false;
  }
  if (auto cfg = jsonObjectAtKey(body, "config")) {
    parseHubConfigObject(*cfg, out.config);
  }
  if (auto token = jsonStringAtKey(body, "token")) {
    out.rotated_device_token = *token;
  }
  if (auto rotate = jsonBoolAtKey(body, "rotateToken"); rotate && *rotate) {
    if (!out.rotated_device_token.has_value()) {
      out.rotated_device_token = std::string();
    }
  }
  out.parse_ok = true;
  return true;
}

bool parseSetupQrPayload(const std::string& raw, SetupQrParsed& out) {
  out = SetupQrParsed{};
  const std::string trimmed = trimAscii(raw);
  if (trimmed.empty() || trimmed.front() != '{') {
    return false;
  }
  if (auto ssid = jsonStringAtKey(trimmed, "ssid")) {
    out.ssid = *ssid;
  } else if (auto s = jsonStringAtKey(trimmed, "s")) {
    out.ssid = *s;
  }
  if (auto pass = jsonStringAtKey(trimmed, "pass")) {
    out.pass = *pass;
  } else if (auto w = jsonStringAtKey(trimmed, "w")) {
    out.pass = *w;
  }
  if (auto url = jsonStringAtKey(trimmed, "url")) {
    out.url = *url;
  } else if (auto u = jsonStringAtKey(trimmed, "u")) {
    out.url = *u;
  }
  if (auto code = jsonStringAtKey(trimmed, "code")) {
    out.code = *code;
  } else if (auto p = jsonStringAtKey(trimmed, "p")) {
    out.code = *p;
  }
  if (auto v = jsonIntAtKey(trimmed, "v")) {
    out.ok = (*v == 1) && !out.ssid.empty();
  } else {
    out.ok = !out.ssid.empty();
  }
  return out.ok;
}

}  // namespace stockbot
