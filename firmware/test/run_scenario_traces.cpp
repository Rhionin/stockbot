// TEST-ONLY: replays JSON controller scenarios and compares trace output to goldens.

#include <algorithm>
#include <cstdlib>
#include <fstream>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>

#include "controller_mocks.h"
#include "display_model.h"
#include "scanner_controller.h"
#include "scanner_state.h"
#include "scenario_min_json.h"

namespace {

std::string readFile(const std::string& path) {
  std::ifstream in(path);
  if (!in) {
    throw std::runtime_error("cannot read " + path);
  }
  std::ostringstream ss;
  ss << in.rdbuf();
  return ss.str();
}

void writeFile(const std::string& path, const std::string& body) {
  std::ofstream out(path);
  if (!out) {
    throw std::runtime_error("cannot write " + path);
  }
  out << body;
  if (!body.empty() && body.back() != '\n') {
    out << '\n';
  }
}

const char* operationalStateStr(stockbot::OperationalState s) {
  switch (s) {
    case stockbot::OperationalState::Unprovisioned:
      return "Unprovisioned";
    case stockbot::OperationalState::Pairing:
      return "Pairing";
    case stockbot::OperationalState::Paired:
      return "Paired";
  }
  return "Unprovisioned";
}

const char* uiScreenStr(stockbot::UiScreen s) {
  switch (s) {
    case stockbot::UiScreen::S0_Unprovisioned:
      return "S0_Unprovisioned";
    case stockbot::UiScreen::S1_Pairing:
      return "S1_Pairing";
    case stockbot::UiScreen::S2_Idle:
      return "S2_Idle";
    case stockbot::UiScreen::S3_ScanResult:
      return "S3_ScanResult";
    case stockbot::UiScreen::S4_UnknownBarcode:
      return "S4_UnknownBarcode";
    case stockbot::UiScreen::S5_OfflineBuffered:
      return "S5_OfflineBuffered";
    case stockbot::UiScreen::S6_DockReminder:
      return "S6_DockReminder";
    case stockbot::UiScreen::S7_HardError:
      return "S7_HardError";
    case stockbot::UiScreen::S7_OfflineBufferRefused:
      return "S7_OfflineBufferRefused";
    case stockbot::UiScreen::S_Connected:
      return "S_Connected";
    case stockbot::UiScreen::S_PairingExpired:
      return "S_PairingExpired";
    case stockbot::UiScreen::S_PairingTooManyTries:
      return "S_PairingTooManyTries";
    case stockbot::UiScreen::S_PairingCodeNotAccepted:
      return "S_PairingCodeNotAccepted";
    case stockbot::UiScreen::S_PairingCantConnect:
      return "S_PairingCantConnect";
    case stockbot::UiScreen::S_WifiButtonHold:
      return "S_WifiButtonHold";
    case stockbot::UiScreen::S_WifiButtonHoldErase:
      return "S_WifiButtonHoldErase";
    case stockbot::UiScreen::S_WifiSetup:
      return "S_WifiSetup";
  }
  return "S2_Idle";
}

MockHttpScriptEntry entryFromValue(const scenjson::Value& v) {
  MockHttpScriptEntry e;
  if (v.kind != scenjson::Value::Kind::Object) {
    return e;
  }
  if (const auto* tr = scenjson::objectField(v.obj, "transport_ok")) {
    if (auto b = scenjson::getBool(*tr)) {
      e.result.transport_ok = *b;
    }
  }
  if (const auto* st = scenjson::objectField(v.obj, "http_status")) {
    if (auto n = scenjson::getNumber(*st)) {
      e.result.http_status = static_cast<int>(*n);
    }
  }
  if (const auto* body = scenjson::objectField(v.obj, "body")) {
    if (auto s = scenjson::getString(*body)) {
      e.result.body = *s;
    }
  }
  e.scan_body.parse_ok = true;
  if (const auto* unauth = scenjson::objectField(v.obj, "unauthorized")) {
    if (auto b = scenjson::getBool(*unauth)) {
      e.scan_body.unauthorized = *b;
    }
  }
  if (const auto* status = scenjson::objectField(v.obj, "status")) {
    if (auto s = scenjson::getString(*status)) {
      e.scan_body.status = *s;
    }
  }
  if (const auto* scan_status = scenjson::objectField(v.obj, "scan_status")) {
    if (auto s = scenjson::getString(*scan_status)) {
      e.scan_body.scan_status = *s;
    }
  }
  if (const auto* known = scenjson::objectField(v.obj, "known")) {
    if (auto b = scenjson::getBool(*known)) {
      e.scan_body.known = *b;
    }
  }
  if (const auto* pn = scenjson::objectField(v.obj, "product_name")) {
    if (auto s = scenjson::getString(*pn)) {
      e.scan_body.product_name = *s;
    }
  }
  if (const auto* oh = scenjson::objectField(v.obj, "on_hand")) {
    if (auto n = scenjson::getNumber(*oh)) {
      e.scan_body.on_hand = static_cast<int>(*n);
    }
  }
  e.pair_body.device_token = "sbd_scenario";
  if (const auto* tok = scenjson::objectField(v.obj, "device_token")) {
    if (auto s = scenjson::getString(*tok)) {
      e.pair_body.device_token = *s;
    }
  }
  e.hb_body.parse_ok = true;
  if (const auto* cv = scenjson::objectField(v.obj, "config_version")) {
    if (auto n = scenjson::getNumber(*cv)) {
      e.hb_body.config.config_version = static_cast<uint32_t>(*n);
    }
  }
  if (const auto* dd = scenjson::objectField(v.obj, "default_direction")) {
    if (auto s = scenjson::getString(*dd)) {
      e.hb_body.config.default_direction = *s;
    }
  }
  return e;
}

void pushHttpQueue(MockClient& client, const scenjson::Value& step) {
  const auto pushArr = [&](const char* key, std::deque<MockHttpScriptEntry>& q) {
    const scenjson::Value* arrv = scenjson::objectField(step.obj, key);
    if (arrv == nullptr || arrv->kind != scenjson::Value::Kind::Array) {
      return;
    }
    for (const auto& item : arrv->arr.items) {
      q.push_back(entryFromValue(item));
    }
  };
  pushArr("pair", client.pair_queue);
  pushArr("scan", client.scan_queue);
  pushArr("heartbeat", client.hb_queue);
  pushArr("flush", client.scan_queue);
}

scenjson::Value makeTraceStep(uint64_t at_ms, stockbot::ScannerController& controller,
                              stockbot::RingBuffer& ring, const MockDisplay& display,
                              const std::vector<HttpRequestRecord>& http_since) {
  scenjson::Object o;
  auto addStr = [&](const char* k, const std::string& v) {
    o.fields.emplace_back(k, scenjson::Value::makeString(v));
  };
  auto addNum = [&](const char* k, int64_t v) {
    o.fields.emplace_back(k, scenjson::Value::makeNumber(static_cast<double>(v)));
  };

  addNum("at_ms", static_cast<int64_t>(at_ms));
  addStr("operational_state", operationalStateStr(controller.operationalState()));
  addStr("ui_screen", uiScreenStr(controller.uiScreen()));
  addNum("ring_count", static_cast<int64_t>(ring.count()));

  scenjson::Array barcodes;
  if (ring.count() <= 10) {
    for (const auto& rec : ring.peekBatch(stockbot::RingBuffer::kCapacity)) {
      barcodes.items.push_back(scenjson::Value::makeString(rec.barcode));
    }
  }
  o.fields.emplace_back("ring_barcodes", scenjson::Value::makeArray(std::move(barcodes)));

  scenjson::Array reqs;
  for (const auto& r : http_since) {
    scenjson::Object ro;
    ro.fields.emplace_back("kind", scenjson::Value::makeString(r.kind));
    ro.fields.emplace_back("at_ms", scenjson::Value::makeNumber(static_cast<double>(r.at_ms)));
    ro.fields.emplace_back("http_status", scenjson::Value::makeNumber(r.http_status));
    ro.fields.emplace_back("transport_ok", scenjson::Value::makeBool(r.transport_ok));
    reqs.items.push_back(scenjson::Value::makeObject(std::move(ro)));
  }
  o.fields.emplace_back("http_requests", scenjson::Value::makeArray(std::move(reqs)));

  addStr("band1", display.last.band1);
  addStr("band1_right", display.last.band1_right);
  addStr("band2_line1", display.last.band2_line1);
  addStr("band2_line2", display.last.band2_line2);
  addStr("band3", display.last.band3);
  return scenjson::Value::makeObject(std::move(o));
}

void applySetNvs(MockNvs& nvs, const scenjson::Object& step) {
  if (const auto* v = scenjson::objectField(step, "wifi_ssid")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.wifi_ssid = *s;
    }
  }
  if (const auto* v = scenjson::objectField(step, "wifi_pass")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.wifi_pass = *s;
    }
  }
  if (const auto* v = scenjson::objectField(step, "server_url")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.server_url = *s;
    }
  }
  if (const auto* v = scenjson::objectField(step, "device_token")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.device_token = *s;
    }
  }
  if (const auto* v = scenjson::objectField(step, "label")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.label = *s;
    }
  }
  if (const auto* v = scenjson::objectField(step, "config_version")) {
    if (auto n = scenjson::getNumber(*v)) {
      nvs.cfg.config_version = static_cast<uint32_t>(*n);
    }
  }
  if (const auto* v = scenjson::objectField(step, "inactivity_revert_seconds")) {
    if (auto n = scenjson::getNumber(*v)) {
      nvs.cfg.inactivity_revert_s = static_cast<uint32_t>(*n);
    }
  }
  if (const auto* v = scenjson::objectField(step, "default_direction")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.default_direction = (*s == "stock_in") ? stockbot::Direction::StockIn
                                                   : stockbot::Direction::StockOut;
    }
  }
  if (const auto* v = scenjson::objectField(step, "commit_mode")) {
    if (auto s = scenjson::getString(*v)) {
      nvs.cfg.commit_mode = (*s == "queue") ? stockbot::CommitMode::Queue : stockbot::CommitMode::Immediate;
    }
  }
}

void applyPower(MockPower& power, const scenjson::Object& step) {
  if (const auto* v = scenjson::objectField(step, "vbus")) {
    if (auto b = scenjson::getBool(*v)) {
      power.status.vbus_present = *b;
    }
  }
  if (const auto* v = scenjson::objectField(step, "charging")) {
    if (auto b = scenjson::getBool(*v)) {
      power.status.charging = *b;
    }
  }
  if (const auto* v = scenjson::objectField(step, "battery_pct")) {
    if (v->kind == scenjson::Value::Kind::Null) {
      power.status.battery_pct = std::nullopt;
    } else if (auto n = scenjson::getNumber(*v)) {
      power.status.battery_pct = static_cast<int>(*n);
    }
  }
  if (const auto* v = scenjson::objectField(step, "clear_battery")) {
    if (auto b = scenjson::getBool(*v); b && *b) {
      power.status.battery_pct = std::nullopt;
    }
  }
}

uint64_t stepAtMs(const scenjson::Object& step) {
  const scenjson::Value* at = scenjson::objectField(step, "at_ms");
  if (at == nullptr) {
    return 0;
  }
  if (auto n = scenjson::getNumber(*at)) {
    return static_cast<uint64_t>(*n);
  }
  return 0;
}

std::string runScenario(const scenjson::Value& root, const std::string& scenario_name) {
  const scenjson::Value* stepsv = scenjson::objectField(root.obj, "steps");
  if (stepsv == nullptr || stepsv->kind != scenjson::Value::Kind::Array) {
    throw std::runtime_error("scenario missing steps array");
  }

  MemoryRingStorage storage;
  stockbot::RingBuffer ring(storage);
  MockScanner scanner;
  MockDisplay display;
  MockFeedback feedback;
  MockPower power;
  MockNvs nvs;
  MockPortal portal;
  MockClient client;
  MockWifi wifi;
  stockbot::ScannerController controller(scanner, display, feedback, power, nvs, portal, client, wifi,
                                         ring);

  uint64_t now_holder = 0;
  client.now_ms_ptr = &now_holder;
  size_t http_mark = 0;

  scenjson::Array trace_steps;
  bool begun = false;

  for (const auto& stepv : stepsv->arr.items) {
    if (stepv.kind != scenjson::Value::Kind::Object) {
      continue;
    }
    const scenjson::Object& step = stepv.obj;
    const scenjson::Value* opv = scenjson::objectField(step, "op");
    if (opv == nullptr || opv->kind != scenjson::Value::Kind::String) {
      continue;
    }
    const std::string& op = opv->str;
    const uint64_t at_ms = stepAtMs(step);
    now_holder = at_ms;

    if (op == "set_nvs") {
      applySetNvs(nvs, step);
    } else if (op == "http_queue") {
      pushHttpQueue(client, stepv);
    } else if (op == "wifi_connected") {
      wifi.connected = true;
    } else if (op == "wifi_disconnected") {
      wifi.connected = false;
    } else if (op == "power") {
      applyPower(power, step);
    } else if (op == "portal_submit") {
      portal.pending.ready = true;
      if (const auto* v = scenjson::objectField(step, "wifi_ssid")) {
        if (auto s = scenjson::getString(*v)) {
          portal.pending.wifi_ssid = *s;
        }
      }
      if (const auto* v = scenjson::objectField(step, "wifi_pass")) {
        if (auto s = scenjson::getString(*v)) {
          portal.pending.wifi_pass = *s;
        }
      }
      if (const auto* v = scenjson::objectField(step, "server_url")) {
        if (auto s = scenjson::getString(*v)) {
          portal.pending.server_url = *s;
        }
      }
    } else if (op == "begin") {
      uint32_t nonce = 0;
      if (const auto* v = scenjson::objectField(step, "hw_random_nonce")) {
        if (auto n = scenjson::getNumber(*v)) {
          nonce = static_cast<uint32_t>(*n);
        }
      }
      if (!nvs.cfg.device_token.empty()) {
        client.setDeviceToken(nvs.cfg.device_token);
      }
      controller.begin(at_ms, nonce);
      begun = true;
    } else if (!begun) {
      controller.begin(at_ms);
      begun = true;
    }

    if (op == "tick") {
      controller.tick(at_ms);
    } else if (op == "barcode") {
      bool manual = false;
      if (const auto* v = scenjson::objectField(step, "manual")) {
        if (auto b = scenjson::getBool(*v)) {
          manual = *b;
        }
      }
      std::string line;
      if (const auto* v = scenjson::objectField(step, "line")) {
        if (auto s = scenjson::getString(*v)) {
          line = *s;
        }
      }
      controller.onBarcodeLine(line, manual, at_ms);
    } else if (op == "mode_button_down") {
      controller.onModeButtonDown(at_ms);
    } else if (op == "mode_button_up") {
      controller.onModeButtonUp(at_ms);
    } else if (op == "mode_button_short_press") {
      controller.onModeButtonShortPress(at_ms);
    } else if (op == "mode_button_hold") {
      uint64_t hold_ms = 10000;
      if (const auto* v = scenjson::objectField(step, "hold_ms")) {
        if (auto n = scenjson::getNumber(*v)) {
          hold_ms = static_cast<uint64_t>(*n);
        }
      }
      controller.onModeButtonHold(hold_ms, at_ms);
    } else if (op == "mode_button_hold_tick") {
      uint64_t hold_ms = 0;
      if (const auto* v = scenjson::objectField(step, "hold_ms")) {
        if (auto n = scenjson::getNumber(*v)) {
          hold_ms = static_cast<uint64_t>(*n);
        }
      }
      controller.onModeButtonHoldTick(hold_ms, at_ms);
    } else if (op == "mode_button_hold_poll") {
      controller.onModeButtonHoldPoll(at_ms);
    } else if (op == "trigger_button") {
      bool pressed = true;
      if (const auto* v = scenjson::objectField(step, "pressed")) {
        if (auto b = scenjson::getBool(*v)) {
          pressed = *b;
        }
      }
      controller.onTriggerButtonChanged(pressed, at_ms);
    } else if (op == "fill_ring_offline") {
      size_t count = stockbot::RingBuffer::kCapacity;
      if (const auto* v = scenjson::objectField(step, "count")) {
        if (auto n = scenjson::getNumber(*v)) {
          count = static_cast<size_t>(*n);
        }
      }
      client.next_scan = {false, 0, ""};
      client.scan_queue.clear();
      uint64_t t = at_ms;
      for (size_t i = 0; i < count; ++i) {
        char bc[32];
        const unsigned long n = static_cast<unsigned long>(i + 1);
        std::snprintf(bc, sizeof(bc), "%012lu", n);
        controller.onBarcodeLine(bc, false, t++);
      }
      now_holder = t - 1;
    }

    std::vector<HttpRequestRecord> http_since(client.http_log.begin() + static_cast<std::ptrdiff_t>(http_mark),
                                              client.http_log.end());
    trace_steps.items.push_back(makeTraceStep(at_ms, controller, ring, display, http_since));
    http_mark = client.http_log.size();
  }

  scenjson::Object root_out;
  root_out.fields.emplace_back("name", scenjson::Value::makeString(scenario_name));
  root_out.fields.emplace_back("steps", scenjson::Value::makeArray(std::move(trace_steps)));
  return scenjson::stringify(scenjson::Value::makeObject(std::move(root_out)), 2, 0) + "\n";
}

}  // namespace

int main(int argc, char** argv) {
  bool update = false;
  std::string scenarios_dir;
  std::string goldens_dir;
  std::string single;

  for (int i = 1; i < argc; ++i) {
    const std::string arg = argv[i];
    if (arg == "--update") {
      update = true;
    } else if (arg == "--scenarios-dir" && i + 1 < argc) {
      scenarios_dir = argv[++i];
    } else if (arg == "--goldens-dir" && i + 1 < argc) {
      goldens_dir = argv[++i];
    } else if (arg == "--scenario" && i + 1 < argc) {
      single = argv[++i];
    }
  }

  if (scenarios_dir.empty() || goldens_dir.empty()) {
    std::cerr << "usage: run_scenario_traces --scenarios-dir DIR --goldens-dir DIR [--update] [--scenario NAME]\n";
    return EXIT_FAILURE;
  }

  std::vector<std::string> names;
  if (!single.empty()) {
    names.push_back(single);
  } else {
    // deterministic order from fixed list
    const char* kNames[] = {
        "device_revoked_scan_401",     "dock_vbus_no_battery",
        "heartbeat_60s_interval",      "scan_retry_503_buffered",
        "busy_body_scan_not_retried",  "offline_buffer_full_refuse",
        "factory_reset_from_hold",     "pairing_expired_transient",
        "inactivity_revert_5min",
    };
    for (const char* n : kNames) {
      names.emplace_back(n);
    }
  }

  int failures = 0;
  for (const std::string& name : names) {
    const std::string scenario_path = scenarios_dir + "/" + name + ".json";
    const std::string golden_path = goldens_dir + "/" + name + ".trace.json";
    try {
      const scenjson::Value root = scenjson::parse(readFile(scenario_path));
      const std::string got = runScenario(root, name);
      if (update) {
        writeFile(golden_path, got);
        std::cout << "updated " << golden_path << '\n';
        continue;
      }
      const std::string want = readFile(golden_path);
      if (got != want) {
        std::cerr << "scenario trace mismatch: " << name << '\n';
        std::cerr << "--- golden\n+++ got\n";
        // lightweight diff via writing temp isn't needed; show first diff line
        const size_t n = std::min(got.size(), want.size());
        size_t i = 0;
        for (; i < n; ++i) {
          if (got[i] != want[i]) {
            break;
          }
        }
        std::cerr << "first diff at byte " << i << '\n';
        failures++;
      }
    } catch (const std::exception& ex) {
      std::cerr << "scenario " << name << " failed: " << ex.what() << '\n';
      failures++;
    }
  }

  if (failures != 0) {
    return EXIT_FAILURE;
  }
  std::cout << "OK: scenario traces (" << names.size() << ")\n";
  return EXIT_SUCCESS;
}
