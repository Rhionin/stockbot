// TEST-ONLY: writes testdata/device-protocol/*.json from real C++ protocol_json builders/parsers.
//
// Build from firmware/ with the same sources as run_host_tests.sh (see README).
//
// Usage: gen_device_protocol_vectors <out-dir>   (default: ../testdata/device-protocol)

#include <cstdio>
#include <cstring>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "protocol_json.h"
#include "ring_buffer.h"

namespace {

// Must match firmware/src/core/scanner_controller.cpp httpFailureIsRetryable (not linked here).
bool httpFailureIsRetryable(int http_status, bool transport_ok) {
  if (!transport_ok) {
    return true;
  }
  return http_status >= 500 || http_status == 429;
}

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

stockbot::RingScanRecord makeRec(const char* id, const char* barcode, uint8_t direction, uint32_t scanned_ms) {
  stockbot::RingScanRecord rec{};
  std::strncpy(rec.client_scan_id, id, sizeof(rec.client_scan_id) - 1);
  std::strncpy(rec.barcode, barcode, sizeof(rec.barcode) - 1);
  rec.direction = direction;
  rec.scanned_ms = scanned_ms;
  return rec;
}

void writeFile(const std::string& dir, const char* name, const std::string& body) {
  const std::string path = dir + "/" + name;
  std::ofstream out(path);
  if (!out) {
    std::cerr << "Failed to open " << path << "\n";
    std::exit(1);
  }
  out << body;
  if (!body.empty() && body.back() != '\n') {
    out << "\n";
  }
}

std::string hubConfigJson(const stockbot::DeviceHubConfigParsed& c) {
  std::ostringstream oss;
  oss << "{"
      << "\"label\":\"" << jsonEscape(c.label) << "\","
      << "\"defaultDirection\":\"" << jsonEscape(c.default_direction) << "\","
      << "\"inactivityRevertSeconds\":" << c.inactivity_revert_seconds << ","
      << "\"commitMode\":\"" << jsonEscape(c.commit_mode) << "\","
      << "\"configVersion\":" << c.config_version;
  oss << "}";
  return oss.str();
}

std::string pairParsedJson(const stockbot::PairCompleteParsed& p) {
  std::ostringstream oss;
  oss << "{"
      << "\"deviceId\":\"" << jsonEscape(p.device_id) << "\","
      << "\"deviceToken\":\"" << jsonEscape(p.device_token) << "\","
      << "\"householdId\":\"" << jsonEscape(p.household_id) << "\","
      << "\"householdName\":\"" << jsonEscape(p.household_name) << "\","
      << "\"heartbeatSeconds\":" << p.heartbeat_seconds << ","
      << "\"config\":" << hubConfigJson(p.config);
  oss << "}";
  return oss.str();
}

std::string scanParsedJson(const stockbot::ParsedDeviceScanResponse& p) {
  std::ostringstream oss;
  oss << "{"
      << "\"id\":\"" << jsonEscape(p.id) << "\","
      << "\"status\":\"" << jsonEscape(p.status) << "\","
      << "\"scanStatus\":\"" << jsonEscape(p.scan_status) << "\","
      << "\"known\":" << (p.known ? "true" : "false") << ","
      << "\"productNameIsNull\":" << (p.product_name_is_null ? "true" : "false") << ","
      << "\"productName\":\"" << jsonEscape(p.product_name) << "\","
      << "\"barcode\":\"" << jsonEscape(p.barcode) << "\","
      << "\"direction\":\"" << jsonEscape(p.direction) << "\","
      << "\"queuedCount\":" << p.queued_count << ","
      << "\"delta\":" << p.delta << ","
      << "\"onHand\":" << p.on_hand << ","
      << "\"backfilled\":" << (p.backfilled ? "true" : "false") << ","
      << "\"code\":\"" << jsonEscape(p.code) << "\","
      << "\"unauthorized\":" << (p.unauthorized ? "true" : "false") << ","
      << "\"parseOk\":" << (p.parse_ok ? "true" : "false");
  oss << "}";
  return oss.str();
}

std::string heartbeatParsedJson(const stockbot::HeartbeatParsed& h) {
  std::ostringstream oss;
  oss << "{"
      << "\"parseOk\":" << (h.parse_ok ? "true" : "false") << ","
      << "\"config\":" << hubConfigJson(h.config) << ",";
  if (h.rotated_device_token.has_value()) {
    oss << "\"rotatedDeviceToken\":\"" << jsonEscape(*h.rotated_device_token) << "\","
        << "\"hasRotatedDeviceToken\":true";
  } else {
    oss << "\"rotatedDeviceToken\":null,\"hasRotatedDeviceToken\":false";
  }
  oss << "}";
  return oss.str();
}

std::string setupParsedJson(const stockbot::SetupQrParsed& s) {
  return std::string("{\"ok\":") + (s.ok ? "true" : "false") + ",\"ssid\":\"" + jsonEscape(s.ssid) +
         "\",\"pass\":\"" + jsonEscape(s.pass) + "\",\"url\":\"" + jsonEscape(s.url) + "\",\"code\":\"" +
         jsonEscape(s.code) + "\"}";
}

}  // namespace

int main(int argc, char** argv) {
  std::string out_dir = "../testdata/device-protocol";
  if (argc >= 2) {
    out_dir = argv[1];
  }

  // --- requests.json ---
  {
    std::ostringstream oss;
    oss << "[\n";
    bool first = true;
    auto emit = [&](const char* name, const char* method, const char* endpoint, const std::string& body) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      oss << "  {\"name\":\"" << name << "\",\"method\":\"" << method << "\",\"endpoint\":\"" << endpoint
          << "\",\"body\":\"" << jsonEscape(body) << "\"}";
    };

    emit("pair-basic", "POST", "/api/devices/pair/complete",
         stockbot::buildPairCompleteRequestJson("SBP-0123-ABCD-G", "Pantry", "1.0.0", "boot-1"));

    emit("pair-escape-unicode", "POST", "/api/devices/pair/complete",
         stockbot::buildPairCompleteRequestJson("SBP-\"\\n", "Café \"hub\"", "1.0.0\r\n", "boot\\id"));

    emit("pair-max-label", "POST", "/api/devices/pair/complete",
         stockbot::buildPairCompleteRequestJson("SBP-0123-ABCD-G",
                                                "abcdefghijklmnopqrstuvwxyz-0123456789-long-label", "9.9.9",
                                                "boot-zzzzzzzzzzzzzzzzzzzzzzzzzzzz"));

    const auto rec = makeRec("scan-1", "4011", 0, 1000);
    emit("scan-single", "POST", "/api/device/scans",
         stockbot::buildSingleScanRequestJson(rec, "boot-1", 5500, false));

    const auto rec_in = makeRec("scan-in", "4011", 1, 2000);
    emit("scan-single-stock-in", "POST", "/api/device/scans",
         stockbot::buildSingleScanRequestJson(rec_in, "boot-1", 100, false));

    const auto rec_approx = makeRec("scan-approx", "4011", 0, 0);
    emit("scan-single-time-approx", "POST", "/api/device/scans",
         stockbot::buildSingleScanRequestJson(rec_approx, "boot-1", 0, true));

    const auto rec_esc = makeRec("id\"\\n", "12\"34", 0, 500);
    emit("scan-single-escaped", "POST", "/api/device/scans",
         stockbot::buildSingleScanRequestJson(rec_esc, "boot\\n", 42, false));

    const auto rec_max = makeRec("012345678901234567890123456789012345", "123456789012345", 1,
                                 4294967295U);
    emit("scan-single-max-fields", "POST", "/api/device/scans",
         stockbot::buildSingleScanRequestJson(rec_max, "boot-max-id-field-123456789012345678", 999999999,
                                              false));

    std::vector<stockbot::RingScanRecord> batch;
    batch.push_back(rec);
    auto rec2 = makeRec("scan-2", "7890123456789", 1, 2000);
    batch.push_back(rec2);
    auto rec3 = makeRec("scan-3", "123", 0, 0);
    batch.push_back(rec3);
    emit("scan-batch-three", "POST", "/api/device/scans",
         stockbot::buildBatchScansRequestJson(batch, "boot-1", 5000));

    emit("heartbeat-full", "POST", "/api/device/heartbeat",
         stockbot::buildHeartbeatRequestJson("1.0.0-esp32", "boot-1", 60, 3, 80, true, true));

    emit("heartbeat-no-battery", "POST", "/api/device/heartbeat",
         stockbot::buildHeartbeatRequestJson("1.0.0", "boot-2", 0, 0, -1, false, false));

    emit("heartbeat-escape", "POST", "/api/device/heartbeat",
         stockbot::buildHeartbeatRequestJson("ver\"\\n", "boot\r", 1, 500, 0, false, true));

    oss << "\n]\n";
    writeFile(out_dir, "requests.json", oss.str());
  }

  // --- responses.json ---
  {
    std::ostringstream oss;
    oss << "[\n";
    bool first = true;
    auto emit_pair = [&](const char* name, int status, const char* body, bool fn_ok) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      stockbot::PairCompleteParsed parsed;
      const bool called = stockbot::parsePairCompleteResponseJson(status, body, parsed);
      oss << "  {\"name\":\"" << name << "\",\"kind\":\"pair_complete\",\"httpStatus\":" << status
          << ",\"body\":\"" << jsonEscape(body) << "\",\"fnOk\":" << (called ? "true" : "false")
          << ",\"parsed\":" << pairParsedJson(parsed) << "}";
      if (called != fn_ok) {
        std::cerr << "pair case " << name << " fn_ok mismatch\n";
        std::exit(1);
      }
    };
    auto emit_scan = [&](const char* name, int status, const char* body, bool fn_ok) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      stockbot::ParsedDeviceScanResponse parsed;
      const bool called = stockbot::parseDeviceScanResponseJson(status, body, parsed);
      oss << "  {\"name\":\"" << name << "\",\"kind\":\"device_scan\",\"httpStatus\":" << status
          << ",\"body\":\"" << jsonEscape(body) << "\",\"fnOk\":" << (called ? "true" : "false")
          << ",\"parsed\":" << scanParsedJson(parsed) << "}";
      if (called != fn_ok) {
        std::cerr << "scan case " << name << " mismatch\n";
        std::exit(1);
      }
    };
    auto emit_batch = [&](const char* name, int status, const char* body, bool fn_ok) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      std::vector<stockbot::BatchScanResultItem> items;
      const bool called = stockbot::parseDeviceScanBatchResponseJson(status, body, items);
      oss << "  {\"name\":\"" << name << "\",\"kind\":\"scan_batch\",\"httpStatus\":" << status
          << ",\"body\":\"" << jsonEscape(body) << "\",\"fnOk\":" << (called ? "true" : "false")
          << ",\"items\":[";
      for (size_t i = 0; i < items.size(); ++i) {
        if (i > 0) {
          oss << ",";
        }
        oss << "{\"id\":\"" << jsonEscape(items[i].id) << "\",\"status\":\"" << jsonEscape(items[i].status)
            << "\"}";
      }
      oss << "]}";
      if (called != fn_ok) {
        std::cerr << "batch case " << name << " fn_ok mismatch\n";
        std::exit(1);
      }
    };
    auto emit_hb = [&](const char* name, int status, const char* body, bool fn_ok) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      stockbot::HeartbeatParsed parsed;
      const bool called = stockbot::parseHeartbeatResponseJson(status, body, parsed);
      oss << "  {\"name\":\"" << name << "\",\"kind\":\"heartbeat\",\"httpStatus\":" << status << ",\"body\":\""
          << jsonEscape(body) << "\",\"fnOk\":" << (called ? "true" : "false")
          << ",\"parsed\":" << heartbeatParsedJson(parsed) << "}";
      if (called != fn_ok) {
        std::cerr << "heartbeat case " << name << " fn_ok mismatch\n";
        std::exit(1);
      }
    };

    const char* pair_ok =
        R"({"deviceId":"dev-1","token":"sbd_testtoken","householdId":"hh-1","heartbeatSeconds":120,"config":{"label":"Room","defaultDirection":"stock_out","inactivityRevertSeconds":300,"commitMode":"queue","configVersion":2}})";
    stockbot::PairCompleteParsed p_exp;
    stockbot::parsePairCompleteResponseJson(201, pair_ok, p_exp);
    (void)p_exp;
    emit_pair("pair-201-ok", 201, pair_ok, true);

    emit_pair("pair-401", 401, "{}", false);
    emit_pair("pair-404", 404, R"({"code":"not_found"})", false);
    emit_pair("pair-200-missing-token", 200, R"({"deviceId":"dev-1"})", false);

    const char* scan_ok =
        R"({"id":"scan-1","status":"created","scanStatus":"queued","known":true,"productName":"Milk","barcode":"4011","direction":"stock_out","queuedCount":1,"onHand":6,"delta":0,"backfilled":false})";
    stockbot::ParsedDeviceScanResponse s_exp;
    stockbot::parseDeviceScanResponseJson(200, scan_ok, s_exp);
    (void)s_exp;
    emit_scan("scan-200-ok", 200, scan_ok, true);

    emit_scan("scan-401", 401, "{}", true);

    emit_scan("scan-500", 500, "{}", false);

    const char* bounded =
        R"({"results":[{"id":"inner","status":"created","onHand":2}],"onHand":99})";
    emit_scan("scan-results-bounded-onhand", 200, bounded, true);

    const char* null_name = R"({"id":"x","status":"created","productName":null})";
    emit_scan("scan-product-name-null", 200, null_name, true);

    emit_scan("scan-malformed", 200, "{not json", false);

    const char* batch_ok =
        R"({"results":[{"id":"scan-a","status":"created","onHand":1},{"id":"scan-b","status":"rejected","onHand":0}],"config":{}})";
    emit_batch("batch-results", 200, batch_ok, true);

    emit_batch("batch-401", 401, "{}", true);
    emit_batch("batch-503", 503, "{}", false);

    const char* hb_ok =
        R"({"config":{"defaultDirection":"stock_in","inactivityRevertSeconds":600,"commitMode":"immediate","configVersion":9},"rotateToken":false})";
    emit_hb("heartbeat-200", 200, hb_ok, true);

    const char* hb_rotate =
        R"({"config":{},"rotateToken":true,"token":"sbd_rotated"})";
    emit_hb("heartbeat-rotate-token", 200, hb_rotate, true);

    const char* hb_rotate_empty =
        R"({"rotateToken":true})";
    emit_hb("heartbeat-rotate-empty", 200, hb_rotate_empty, true);

    emit_hb("heartbeat-401", 401, "{}", true);
    emit_hb("heartbeat-503", 503, "{}", false);

    oss << "\n]\n";
    writeFile(out_dir, "responses.json", oss.str());
  }

  // --- setup_qr.json ---
  {
    std::ostringstream oss;
    oss << "[\n";
    bool first = true;
    auto emit = [&](const char* name, const char* raw) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      stockbot::SetupQrParsed parsed;
      const bool ok = stockbot::parseSetupQrPayload(raw, parsed);
      oss << "  {\"name\":\"" << name << "\",\"raw\":\"" << jsonEscape(raw) << "\",\"fnOk\":" << (ok ? "true" : "false")
          << ",\"parsed\":" << setupParsedJson(parsed) << "}";
    };

    emit("setup-long-keys", R"({"v":1,"ssid":"Home","pass":"pw","url":"https://x","code":"SBP-0123-ABCD-G"})");
    emit("setup-short-keys", R"({"s":"Net","w":"secret","u":"https://u","p":"CODE"})");
    emit("setup-trim", "  \n{\"ssid\":\"X\"}  ");
    emit("setup-v0-reject", R"({"v":0,"ssid":"X"})");
    emit("setup-no-ssid", R"({"v":1,"pass":"x"})");
    emit("setup-not-json", "not-json");
    emit("setup-unicode", R"({"ssid":"Café ☕"})");

    oss << "\n]\n";
    writeFile(out_dir, "setup_qr.json", oss.str());
  }

  // --- api_error.json ---
  {
    std::ostringstream oss;
    oss << "[\n";
    bool first = true;
    auto emit = [&](const char* name, const char* body, bool expect_ok, const char* expect_code) {
      if (!first) {
        oss << ",\n";
      }
      first = false;
      std::string code;
      const bool ok = stockbot::parseApiErrorCode(body, code);
      oss << "  {\"name\":\"" << name << "\",\"body\":\"" << jsonEscape(body) << "\",\"fnOk\":"
          << (ok ? "true" : "false") << ",\"code\":\"" << jsonEscape(code) << "\"}";
      if (ok != expect_ok || code != expect_code) {
        std::cerr << "api_error case " << name << " mismatch\n";
        std::exit(1);
      }
    };

    emit("error-code", R"({"code":"not_found","message":"x"})", true, "not_found");
    emit("error-missing", R"({"message":"x"})", false, "");
    emit("error-wrong-type", R"({"code":404})", false, "");

    oss << "\n]\n";
    writeFile(out_dir, "api_error.json", oss.str());
  }

  // --- http_retry.json ---
  {
    const int statuses[] = {0,   200, 400, 401, 403, 404, 409, 429, 500, 502, 503, 599};
    std::ostringstream oss;
    oss << "[\n";
    bool first = true;
    for (int status : statuses) {
      for (int toki = 0; toki < 2; ++toki) {
        const bool transport_ok = (toki == 1);
        const bool retry = httpFailureIsRetryable(status, transport_ok);
        if (!first) {
          oss << ",\n";
        }
        first = false;
        oss << "  {\"httpStatus\":" << status << ",\"transportOk\":" << (transport_ok ? "true" : "false")
            << ",\"retryable\":" << (retry ? "true" : "false") << "}";
      }
    }
    oss << "\n]\n";
    writeFile(out_dir, "http_retry.json", oss.str());
  }

  std::cout << "Wrote device-protocol vectors to " << out_dir << "\n";
  return 0;
}
