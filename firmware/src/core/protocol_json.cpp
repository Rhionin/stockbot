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

std::optional<std::string> jsonStringAtKey(const std::string& body, const std::string& key,
                                           size_t search_start = 0,
                                           size_t search_end = std::string::npos) {
  if (search_end == std::string::npos) {
    search_end = body.size();
  }
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle, search_start);
  if (pos == std::string::npos || pos >= search_end) {
    return std::nullopt;
  }
  pos = body.find(':', pos + needle.size());
  if (pos == std::string::npos || pos >= search_end) {
    return std::nullopt;
  }
  ++pos;
  while (pos < search_end && std::isspace(static_cast<unsigned char>(body[pos]))) {
    ++pos;
  }
  if (pos >= search_end) {
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
  while (pos < search_end && body[pos] != '"') {
    if (body[pos] == '\\' && pos + 1 < search_end) {
      out.push_back(body[pos + 1]);
      pos += 2;
      continue;
    }
    out.push_back(body[pos++]);
  }
  return out;
}

std::optional<int64_t> jsonIntAtKey(const std::string& body, const std::string& key, size_t search_start = 0,
                                    size_t search_end = std::string::npos) {
  if (search_end == std::string::npos) {
    search_end = body.size();
  }
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle, search_start);
  if (pos == std::string::npos || pos >= search_end) {
    return std::nullopt;
  }
  pos = body.find(':', pos + needle.size());
  if (pos == std::string::npos || pos >= search_end) {
    return std::nullopt;
  }
  ++pos;
  while (pos < search_end && std::isspace(static_cast<unsigned char>(body[pos]))) {
    ++pos;
  }
  size_t end = pos;
  while (end < search_end && (std::isdigit(static_cast<unsigned char>(body[end])) || body[end] == '-')) {
    ++end;
  }
  if (end == pos) {
    return std::nullopt;
  }
  return std::stoll(body.substr(pos, end - pos));
}

std::optional<bool> jsonBoolAtKey(const std::string& body, const std::string& key, size_t search_start = 0,
                                  size_t search_end = std::string::npos) {
  if (search_end == std::string::npos) {
    search_end = body.size();
  }
  const std::string needle = "\"" + key + "\"";
  size_t pos = body.find(needle, search_start);
  if (pos == std::string::npos || pos >= search_end) {
    return std::nullopt;
  }
  pos = body.find(':', pos + needle.size());
  if (pos == std::string::npos || pos >= search_end) {
    return std::nullopt;
  }
  ++pos;
  while (pos < search_end && std::isspace(static_cast<unsigned char>(body[pos]))) {
    ++pos;
  }
  if (pos + 4 <= search_end && body.compare(pos, 4, "true") == 0) {
    return true;
  }
  if (pos + 5 <= search_end && body.compare(pos, 5, "false") == 0) {
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
  if (auto household_name = jsonStringAtKey(body, "householdName")) {
    out.household_name = *household_name;
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
                                       const std::string& boot_id, uint64_t now_ms) {
  std::ostringstream oss;
  oss << "{\"scans\":[";
  for (size_t i = 0; i < records.size(); ++i) {
    if (i > 0) {
      oss << ',';
    }
    const uint32_t age_ms = ringScanAgeMs(records[i], now_ms);
    const bool time_approx = records[i].scanned_ms == 0;
    oss << buildSingleScanRequestJson(records[i], boot_id, age_ms, time_approx);
  }
  oss << "]}";
  return oss.str();
}

void parseEnrichedScanFields(const std::string& body, ParsedDeviceScanResponse& out, size_t obj_start,
                             size_t obj_end) {
  if (auto scan_status = jsonStringAtKey(body, "scanStatus", obj_start, obj_end)) {
    out.scan_status = *scan_status;
  }
  if (auto known = jsonBoolAtKey(body, "known", obj_start, obj_end)) {
    out.known = *known;
  }
  if (body.find("\"productName\":null", obj_start) != std::string::npos &&
      body.find("\"productName\":null", obj_start) < obj_end) {
    out.product_name_is_null = true;
  } else if (auto name = jsonStringAtKey(body, "productName", obj_start, obj_end)) {
    out.product_name_is_null = false;
    out.product_name = *name;
  }
  if (auto barcode = jsonStringAtKey(body, "barcode", obj_start, obj_end)) {
    out.barcode = *barcode;
  }
  if (auto direction = jsonStringAtKey(body, "direction", obj_start, obj_end)) {
    out.direction = *direction;
  }
  if (auto queued = jsonIntAtKey(body, "queuedCount", obj_start, obj_end)) {
    out.queued_count = static_cast<int>(*queued);
  }
  if (auto delta = jsonIntAtKey(body, "delta", obj_start, obj_end)) {
    out.delta = static_cast<int>(*delta);
  }
  if (auto on_hand = jsonIntAtKey(body, "onHand", obj_start, obj_end)) {
    out.on_hand = static_cast<int>(*on_hand);
  }
  if (auto backfilled = jsonBoolAtKey(body, "backfilled", obj_start, obj_end)) {
    out.backfilled = *backfilled;
  }
  if (auto code = jsonStringAtKey(body, "code", obj_start, obj_end)) {
    out.code = *code;
  }
}

std::optional<std::pair<size_t, size_t>> jsonObjectSpanAt(const std::string& body, size_t from) {
  const size_t obj_start = body.find('{', from);
  if (obj_start == std::string::npos) {
    return std::nullopt;
  }
  int depth = 0;
  for (size_t i = obj_start; i < body.size(); ++i) {
    if (body[i] == '{') {
      ++depth;
    } else if (body[i] == '}') {
      --depth;
      if (depth == 0) {
        return std::make_pair(obj_start, i + 1);
      }
    }
  }
  return std::nullopt;
}

std::vector<std::pair<size_t, size_t>> jsonResultObjectSpans(const std::string& body) {
  std::vector<std::pair<size_t, size_t>> spans;
  const std::string needle = "\"results\"";
  const size_t results_pos = body.find(needle);
  if (results_pos == std::string::npos) {
    return spans;
  }
  const size_t array_start = body.find('[', results_pos);
  if (array_start == std::string::npos) {
    return spans;
  }
  size_t pos = array_start + 1;
  while (pos < body.size()) {
    while (pos < body.size() && (body[pos] == ' ' || body[pos] == ',' || body[pos] == '\n' || body[pos] == '\r')) {
      ++pos;
    }
    if (pos >= body.size() || body[pos] == ']') {
      break;
    }
    if (auto span = jsonObjectSpanAt(body, pos)) {
      spans.push_back(*span);
      pos = span->second;
      continue;
    }
    break;
  }
  return spans;
}

void parseScanResultObject(const std::string& body, size_t obj_start, size_t obj_end,
                           ParsedDeviceScanResponse& out) {
  if (auto id = jsonStringAtKey(body, "id", obj_start, obj_end)) {
    out.id = *id;
  }
  if (auto status = jsonStringAtKey(body, "status", obj_start, obj_end)) {
    out.status = *status;
  }
  parseEnrichedScanFields(body, out, obj_start, obj_end);
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

  const auto result_spans = jsonResultObjectSpans(body);
  if (!result_spans.empty()) {
    parseScanResultObject(body, result_spans.front().first, result_spans.front().second, out);
  } else if (auto span = jsonObjectSpanAt(body, 0)) {
    parseScanResultObject(body, span->first, span->second, out);
  }

  out.parse_ok = !out.status.empty() || !out.id.empty();
  return out.parse_ok;
}

bool parseDeviceScanBatchResponseJson(int http_status, const std::string& body,
                                      std::vector<BatchScanResultItem>& out) {
  out.clear();
  if (http_status == 401) {
    return true;
  }
  if (http_status < 200 || http_status >= 300) {
    return false;
  }
  const auto spans = jsonResultObjectSpans(body);
  if (spans.empty()) {
    ParsedDeviceScanResponse single;
    if (!parseDeviceScanResponseJson(http_status, body, single)) {
      return false;
    }
    if (!single.id.empty() || !single.status.empty()) {
      out.push_back(BatchScanResultItem{single.id, single.status});
    }
    return true;
  }
  for (const auto& span : spans) {
    BatchScanResultItem item;
    if (auto id = jsonStringAtKey(body, "id", span.first, span.second)) {
      item.id = *id;
    }
    if (auto status = jsonStringAtKey(body, "status", span.first, span.second)) {
      item.status = *status;
    }
    out.push_back(std::move(item));
  }
  return true;
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
      << "\"droppedCount\":0,"
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
