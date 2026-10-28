#include <cstdlib>
#include <cstring>
#include <functional>
#include <iostream>
#include <vector>

#include "barcode_filter.h"
#include "captive_portal.h"
#include "display_model.h"
#include "hal_display.h"
#include "hal_feedback.h"
#include "hal_power.h"
#include "hal_scanner.h"
#include "hid_keycode.h"
#include "nvs_config.h"
#include "protocol_json.h"
#include "ring_buffer.h"
#include "scanner_controller.h"
#include "scanner_state.h"
#include "stockbot_client.h"
#include "wifi_station.h"

namespace {

int failures = 0;

void expect(bool cond, const char* msg) {
  if (!cond) {
    std::cerr << "FAIL: " << msg << '\n';
    ++failures;
  }
}

#define EXPECT(cond) expect((cond), #cond)

class MemoryRingStorage : public stockbot::RingStorage {
 public:
  bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
    out = records_;
    return true;
  }

  bool writeAll(const std::vector<stockbot::RingScanRecord>& records) override {
    records_ = records;
    return true;
  }

 private:
  std::vector<stockbot::RingScanRecord> records_;
};

void test_barcode_filter() {
  using stockbot::BarcodeKind;
  using stockbot::OperationalState;
  using stockbot::classifyBarcode;
  using stockbot::isPairingCode;

  EXPECT(classifyBarcode("https://example.com", OperationalState::Paired) == BarcodeKind::RejectedNon1D);
  EXPECT(classifyBarcode("http://foo", OperationalState::Paired) == BarcodeKind::RejectedNon1D);
  EXPECT(classifyBarcode(R"({"v":1,"ssid":"x"})", OperationalState::Paired) == BarcodeKind::RejectedNon1D);
  EXPECT(classifyBarcode("abc123", OperationalState::Paired) == BarcodeKind::RejectedNon1D);
  EXPECT(classifyBarcode("", OperationalState::Paired) == BarcodeKind::RejectedNon1D);
  EXPECT(classifyBarcode("123456789012345", OperationalState::Paired) == BarcodeKind::RejectedNon1D);

  EXPECT(classifyBarcode("012345678905", OperationalState::Paired) == BarcodeKind::Product);
  EXPECT(classifyBarcode("4011", OperationalState::Paired) == BarcodeKind::Product);
  EXPECT(classifyBarcode("12345678901234", OperationalState::Paired) == BarcodeKind::Product);

  EXPECT(classifyBarcode("STOCK-IN", OperationalState::Paired) == BarcodeKind::ControlStockIn);
  EXPECT(classifyBarcode("stock_in", OperationalState::Paired) == BarcodeKind::ControlStockIn);
  EXPECT(classifyBarcode("STOCK-OUT", OperationalState::Paired) == BarcodeKind::ControlStockOut);
  EXPECT(classifyBarcode("STOCK_OUT", OperationalState::Paired) == BarcodeKind::ControlStockOut);

  EXPECT(isPairingCode("SBP-0123-ABCD-G"));
  EXPECT(!isPairingCode("SBP-0123-ABCD-0"));
  EXPECT(classifyBarcode("SBP-0123-ABCD-G", OperationalState::Pairing) == BarcodeKind::PairingCode);
  EXPECT(classifyBarcode("SBP-0123-ABCD-G", OperationalState::Paired) == BarcodeKind::RejectedNon1D);

  EXPECT(classifyBarcode(R"({"v":1,"ssid":"Home"})", OperationalState::Unprovisioned) ==
         BarcodeKind::SetupPayload);
  EXPECT(classifyBarcode(R"({"v":1,"ssid":"Home"})", OperationalState::Pairing) == BarcodeKind::RejectedNon1D);
}

void test_duplicate_cooldown() {
  using stockbot::Direction;
  using stockbot::DuplicateCooldownState;
  using stockbot::acceptWithDuplicateCooldown;

  DuplicateCooldownState state;
  EXPECT(acceptWithDuplicateCooldown(state, "4011", Direction::StockOut, 1000, false));
  EXPECT(!acceptWithDuplicateCooldown(state, "4011", Direction::StockOut, 2500, false));
  EXPECT(acceptWithDuplicateCooldown(state, "4011", Direction::StockOut, 3001, false));
  EXPECT(acceptWithDuplicateCooldown(state, "4012", Direction::StockOut, 3100, false));
  EXPECT(acceptWithDuplicateCooldown(state, "4011", Direction::StockIn, 3200, false));
  EXPECT(acceptWithDuplicateCooldown(state, "4011", Direction::StockOut, 3300, true));
}

void test_scanner_state() {
  using stockbot::BacklightLevel;
  using stockbot::ButtonHoldGesture;
  using stockbot::Direction;
  using stockbot::ModeChime;
  using stockbot::ScannerState;

  ScannerState st;
  EXPECT(st.active_direction == Direction::StockOut);
  st.backlight = BacklightLevel::Dim20;
  auto wake = st.onModeButtonPress(1000);
  EXPECT(wake.woke_backlight && !wake.toggled_direction);
  EXPECT(st.backlight == BacklightLevel::Full100);
  auto toggle = st.onModeButtonPress(1100);
  EXPECT(toggle.toggled_direction);
  EXPECT(st.active_direction == Direction::StockIn);

  st.last_activity_ms = 1000;
  st.inactivity_revert_seconds = 300;
  st.active_direction = Direction::StockIn;
  st.default_direction = Direction::StockOut;
  st.onScanOrActivity(2000);
  auto no_revert = st.tickInactivityRevert(2000 + 299999);
  EXPECT(!no_revert.has_value());
  auto revert = st.tickInactivityRevert(2000 + 300000);
  EXPECT(revert.has_value() && *revert == ModeChime::ModeRevert);
  EXPECT(st.active_direction == Direction::StockOut);

  st.onVbusTransition(true, 5000);
  EXPECT(st.isScanSuppressed(6000));
  EXPECT(!st.isScanSuppressed(7000));

  EXPECT(st.classifyButtonHold(2000) == ButtonHoldGesture::ShortPress);
  EXPECT(st.classifyButtonHold(5000) == ButtonHoldGesture::WifiUpdate);
  EXPECT(st.classifyButtonHold(10000) == ButtonHoldGesture::FactoryReset);
}

void test_ring_buffer() {
  MemoryRingStorage storage;
  stockbot::RingBuffer ring(storage);
  EXPECT(ring.recoverFromStorage());
  std::string id;
  for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
    EXPECT(ring.enqueue("4011", stockbot::Direction::StockOut, static_cast<uint64_t>(i), id));
  }
  EXPECT(ring.isFull());
  EXPECT(!ring.enqueue("9999", stockbot::Direction::StockOut, 9999, id));
  auto batch = ring.peekBatch(20);
  EXPECT(batch.size() == 20);
  EXPECT(std::string(batch.front().barcode) == "4011");
  EXPECT(ring.acknowledge(batch.front().client_scan_id));
  EXPECT(ring.count() == stockbot::RingBuffer::kCapacity - 1);

  MemoryRingStorage storage2;
  {
    stockbot::RingBuffer r1(storage2);
    r1.enqueue("123", stockbot::Direction::StockIn, 1, id);
  }
  stockbot::RingBuffer r2(storage2);
  EXPECT(r2.recoverFromStorage());
  EXPECT(r2.count() == 1);
}

void test_display_model() {
  using stockbot::CommitMode;
  using stockbot::Direction;
  using stockbot::DisplayModelInput;
  using stockbot::ScanResultView;
  using stockbot::ScanStatusKind;
  using stockbot::UiScreen;
  using stockbot::buildDisplayModel;
  using stockbot::wrapProductNameTwoLines;

  DisplayModelInput in;
  in.screen = UiScreen::S2_Idle;
  in.commit_mode = CommitMode::Queue;
  auto idle = buildDisplayModel(in);
  EXPECT(idle.band3 == "Queues for review");

  in.commit_mode = CommitMode::Immediate;
  idle = buildDisplayModel(in);
  EXPECT(idle.band3 == "Updates stock now");

  in.screen = UiScreen::S3_ScanResult;
  in.commit_mode = CommitMode::Queue;
  in.last_scan.known = true;
  in.last_scan.status = ScanStatusKind::Queued;
  in.last_scan.product_name = "Kirkland Organic Diced Tomatoes 14.5 oz";
  in.last_scan.direction = Direction::StockOut;
  in.last_scan.on_hand = 6;
  in.last_scan.queued_count = 1;
  auto q = buildDisplayModel(in);
  EXPECT(q.band3 == "Queued -1 · 6 on hand");

  in.last_scan.direction = Direction::StockIn;
  in.last_scan.on_hand = 4;
  in.last_scan.queued_count = 2;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "Queued +2 · 4 on hand");

  in.commit_mode = CommitMode::Immediate;
  in.last_scan.direction = Direction::StockIn;
  in.last_scan.on_hand = 5;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "+1 · 5 on hand");

  in.last_scan.direction = Direction::StockOut;
  in.last_scan.on_hand = 3;
  in.last_scan.backfilled = false;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "-1 · 3 left");

  in.last_scan.on_hand = 0;
  in.last_scan.backfilled = false;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "-1 · 0 left (Out of stock)");

  in.last_scan.backfilled = true;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "0 on hand · Added & used 1");
  EXPECT(q.band3.find("backfill") == std::string::npos);

  in.screen = UiScreen::S4_UnknownBarcode;
  in.last_scan.barcode = "999888777";
  auto unk = buildDisplayModel(in);
  EXPECT(unk.band1 == "NEW BARCODE");
  EXPECT(unk.band2_line1 == "Unknown item");
  EXPECT(unk.band3 == "In Scan Queue to name");

  in.screen = UiScreen::S5_OfflineBuffered;
  in.offline_queue_count = 7;
  in.last_scan.barcode = "4011";
  auto off = buildDisplayModel(in);
  EXPECT(off.band3 == "7 waiting to send");

  in.screen = UiScreen::S7_HardError;
  in.device_revoked = true;
  auto err = buildDisplayModel(in);
  EXPECT(err.band1 == "SCANNER ALERT");
  EXPECT(err.band2_line1 == "Scanner disconnected");

  auto wrapped = wrapProductNameTwoLines(
      "Kirkland Organic Diced Tomatoes Extra Long Product Name Here");
  EXPECT(!wrapped.first.empty());
  EXPECT(wrapped.second.find(u8"…") != std::string::npos || !wrapped.second.empty());
}

class MockScanner : public stockbot::HalScanner {
 public:
  int one_d_calls = 0;
  bool one_d_enabled = false;
  bool continuous = false;
  void begin() override {}
  void poll(uint64_t, const std::function<void(const stockbot::ScanLine&)>&) override {}
  void enable1DOnly(bool enabled) override {
    ++one_d_calls;
    one_d_enabled = enabled;
  }
  void setContinuousMode(bool enabled) override { continuous = enabled; }
};

class MockDisplay : public stockbot::HalDisplay {
 public:
  stockbot::DisplayBands last;
  void begin() override {}
  void render(const stockbot::DisplayBands& bands) override { last = bands; }
  void setBacklight(stockbot::BacklightLevel) override {}
};

class MockFeedback : public stockbot::HalFeedback {
 public:
  std::vector<stockbot::AudioCue> cues;
  void begin() override {}
  void play(stockbot::AudioCue cue) override { cues.push_back(cue); }
  void flashLedForScan(bool) override {}
};

class MockPower : public stockbot::HalPower {
 public:
  stockbot::PowerStatus status;
  void begin() override {}
  stockbot::PowerStatus read() override { return status; }
  void setGrovePower(bool) override {}
  void enterDeepSleepWakeOnButton() override {}
};

class MockNvs : public stockbot::NvsConfigStore {
 public:
  stockbot::NvsConfig cfg;
  bool load(stockbot::NvsConfig& out) override {
    out = cfg;
    return true;
  }
  bool save(const stockbot::NvsConfig& in) override {
    cfg = in;
    return true;
  }
  void clearDeviceToken() override { cfg.device_token.clear(); }
  void factoryReset() override { cfg = stockbot::NvsConfig{}; }
};

class MockPortal : public stockbot::CaptivePortal {
 public:
  bool running = false;
  stockbot::CaptivePortalSubmission pending;
  void start(const std::string&) override { running = true; }
  void stop() override { running = false; }
  void loop() override {}
  bool takeSubmission(stockbot::CaptivePortalSubmission& out) override {
    if (!pending.ready) {
      return false;
    }
    out = pending;
    pending.ready = false;
    return true;
  }
};

class MockWifi : public stockbot::WifiStation {
 public:
  std::string last_ssid;
  int connect_calls = 0;
  bool connected = false;
  void begin() override {}
  void connect(const std::string& ssid, const std::string&) override {
    last_ssid = ssid;
    ++connect_calls;
    connected = !ssid.empty();
  }
  bool isConnected() const override { return connected; }
  void tick() override {}
  void disconnectStation() override {
    connected = false;
    last_ssid.clear();
  }
};

class MockClient : public stockbot::StockbotClient {
 public:
  std::string token;
  stockbot::HttpCallResult next_pair;
  stockbot::PairCompleteParsed pair_body;
  stockbot::HttpCallResult next_scan;
  stockbot::ParsedDeviceScanResponse scan_body;
  stockbot::HttpCallResult next_hb;
  stockbot::HeartbeatParsed hb_body;

  void setServerUrl(const std::string&) override {}
  void setDeviceToken(const std::string& t) override { token = t; }

  stockbot::HttpCallResult completePairing(const std::string&, const std::string&, const std::string&,
                                           const std::string&, stockbot::PairCompleteParsed& out) override {
    out = pair_body;
    return next_pair;
  }

  stockbot::HttpCallResult sendScan(const stockbot::RingScanRecord&, const std::string&, uint64_t,
                                    stockbot::ParsedDeviceScanResponse& out) override {
    out = scan_body;
    return next_scan;
  }

  stockbot::HttpCallResult flushBatch(const std::vector<stockbot::RingScanRecord>&,
                                      const std::string&, stockbot::ParsedDeviceScanResponse& out) override {
    out = scan_body;
    return next_scan;
  }

  stockbot::HttpCallResult sendHeartbeat(const std::string&, const std::string&, uint32_t, size_t, int, bool,
                                         stockbot::HeartbeatParsed& out) override {
    out = hb_body;
    return next_hb;
  }
};

void test_protocol_json() {
  const std::string pair_req = stockbot::buildPairCompleteRequestJson("SBP-0123-ABCD-G", "Pantry", "1.0.0", "boot-1");
  EXPECT(pair_req.find("esp32_scanner") != std::string::npos);
  stockbot::PairCompleteParsed parsed;
  const std::string pair_resp =
      R"({"deviceId":"dev-1","token":"sbd_testtoken","config":{"label":"Room","defaultDirection":"stock_out","inactivityRevertSeconds":300,"commitMode":"queue","configVersion":2}})";
  EXPECT(stockbot::parsePairCompleteResponseJson(201, pair_resp, parsed));
  EXPECT(parsed.device_token == "sbd_testtoken");
  EXPECT(parsed.config.config_version == 2);

  stockbot::RingScanRecord rec{};
  std::strncpy(rec.client_scan_id, "scan-1", sizeof(rec.client_scan_id));
  std::strncpy(rec.barcode, "4011", sizeof(rec.barcode));
  rec.direction = 0;
  const std::string scan_req = stockbot::buildSingleScanRequestJson(rec, "boot-1", 100, true);
  EXPECT(scan_req.find("\"unitCount\":1") != std::string::npos);

  stockbot::ParsedDeviceScanResponse scan_out;
  const std::string scan_resp =
      R"({"id":"scan-1","status":"created","scanStatus":"queued","known":true,"productName":"Milk","barcode":"4011","direction":"stock_out","queuedCount":1,"onHand":6,"delta":0,"backfilled":false})";
  EXPECT(stockbot::parseDeviceScanResponseJson(200, scan_resp, scan_out));
  EXPECT(scan_out.status == "created");
  EXPECT(scan_out.on_hand == 6);

  stockbot::HeartbeatParsed hb;
  const std::string hb_resp =
      R"({"config":{"defaultDirection":"stock_in","inactivityRevertSeconds":600,"commitMode":"immediate","configVersion":9},"rotateToken":false})";
  EXPECT(stockbot::parseHeartbeatResponseJson(200, hb_resp, hb));
  EXPECT(hb.config.default_direction == "stock_in");

  stockbot::SetupQrParsed setup;
  EXPECT(stockbot::parseSetupQrPayload(R"({"v":1,"ssid":"Home","pass":"pw","url":"https://x","code":"SBP-0123-ABCD-G"})", setup));
  EXPECT(setup.ssid == "Home");
}

void test_hid_keycode() {
  stockbot::HidKeycodeDecoder decoder;
  uint8_t report[8] = {0, 0, 0x1e, 0x27, 0, 0, 0, 0};  // '1' '0'
  decoder.consumeReport(report);
  uint8_t enter[8] = {0, 0, 0x28, 0, 0, 0, 0, 0};
  auto line = decoder.consumeReport(enter);
  EXPECT(line.has_value() && *line == "10");
}

void test_scanner_controller_flow() {
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

  controller.begin(1000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Unprovisioned);

  nvs.cfg.wifi_ssid = "Home";
  nvs.cfg.server_url = "https://stockbot.test";
  controller.begin(2000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Pairing);

  client.next_pair = {true, 201, ""};
  client.pair_body.device_token = "sbd_abc";
  client.pair_body.config.commit_mode = "queue";
  controller.onBarcodeLine("SBP-0123-ABCD-G", true, 3000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Paired);
  EXPECT(scanner.one_d_enabled);

  client.next_scan = {true, 200, ""};
  client.scan_body = {};
  client.scan_body.status = "created";
  client.scan_body.scan_status = "queued";
  client.scan_body.known = true;
  client.scan_body.product_name = "Milk";
  client.scan_body.on_hand = 6;
  client.scan_body.parse_ok = true;
  controller.onBarcodeLine("4011", true, 4000);
  EXPECT(display.last.band3.find("Queued -1") != std::string::npos);

  stockbot::RingBuffer ring2(storage);
  stockbot::ScannerController revoked(scanner, display, feedback, power, nvs, portal, client, wifi,
                                       ring2);
  nvs.cfg.device_token = "sbd_abc";
  revoked.begin(5000);
  client.next_scan = {true, 401, ""};
  client.scan_body = {};
  client.scan_body.unauthorized = true;
  client.scan_body.parse_ok = true;
  revoked.onBarcodeLine("4012", true, 5100);
  EXPECT(revoked.uiScreen() == stockbot::UiScreen::S7_HardError);

  stockbot::RingBuffer ring3(storage);
  stockbot::ScannerController mode(scanner, display, feedback, power, nvs, portal, client, wifi, ring3);
  mode.begin(6000);
  mode.onModeButtonShortPress(6100);
  EXPECT(mode.activeDirection() == stockbot::Direction::StockIn);

  stockbot::RingBuffer ring4(storage);
  stockbot::ScannerController hb_ctl(scanner, display, feedback, power, nvs, portal, client, wifi, ring4);
  nvs.cfg.wifi_ssid = "Home";
  nvs.cfg.device_token = "sbd_abc";
  hb_ctl.begin(0);
  client.next_hb = {true, 200, R"({"config":{"defaultDirection":"stock_in","configVersion":99}})"};
  client.hb_body.parse_ok = true;
  client.hb_body.config.default_direction = "stock_in";
  client.hb_body.config.config_version = 99;
  hb_ctl.tick(70000);
  EXPECT(nvs.cfg.config_version == 99);
}

void test_portal_submission_handoff() {
  MemoryRingStorage storage;
  MockScanner scanner;
  MockDisplay display;
  MockFeedback feedback;
  MockPower power;
  MockNvs nvs;
  MockPortal portal;
  MockClient client;
  MockWifi wifi;
  stockbot::RingBuffer ring(storage);
  stockbot::ScannerController controller(scanner, display, feedback, power, nvs, portal, client, wifi,
                                         ring);

  controller.begin(1000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Unprovisioned);
  EXPECT(portal.running);

  portal.pending.ready = true;
  portal.pending.wifi_ssid = "PantryWiFi";
  portal.pending.wifi_pass = "secret";
  portal.pending.server_url = "https://stockbot.test";
  controller.tick(2000);

  EXPECT(nvs.cfg.wifi_ssid == "PantryWiFi");
  EXPECT(wifi.last_ssid == "PantryWiFi");
  EXPECT(controller.operationalState() == stockbot::OperationalState::Pairing);
  EXPECT(!portal.running);

  nvs.cfg.wifi_ssid = "PantryWiFi";
  nvs.cfg.device_token = "sbd_preserve";
  stockbot::RingBuffer ring2(storage);
  stockbot::ScannerController update(scanner, display, feedback, power, nvs, portal, client, wifi, ring2);
  update.begin(3000);
  EXPECT(update.operationalState() == stockbot::OperationalState::Paired);
  update.onModeButtonHold(5500, 6000);
  EXPECT(portal.running);
  portal.pending.ready = true;
  portal.pending.wifi_ssid = "NewWiFi";
  portal.pending.wifi_pass = "newsecret";
  update.tick(7000);
  EXPECT(nvs.cfg.device_token == "sbd_preserve");
  EXPECT(update.operationalState() == stockbot::OperationalState::Paired);
  EXPECT(nvs.cfg.wifi_ssid == "NewWiFi");
}

void test_trigger_button_wiring() {
  MemoryRingStorage storage;
  MockScanner scanner;
  MockDisplay display;
  MockFeedback feedback;
  MockPower power;
  MockNvs nvs;
  MockPortal portal;
  MockClient client;
  MockWifi wifi;
  stockbot::RingBuffer ring(storage);
  stockbot::ScannerController controller(scanner, display, feedback, power, nvs, portal, client, wifi,
                                         ring);
  nvs.cfg.wifi_ssid = "Home";
  nvs.cfg.device_token = "sbd_x";
  controller.begin(1000);

  controller.onTriggerButtonChanged(true, 2000);
  EXPECT(controller.manualRetriggerPending());
  EXPECT(scanner.continuous);

  power.status.vbus_present = false;
  controller.onTriggerButtonChanged(false, 2100);
  EXPECT(!scanner.continuous);
}

}  // namespace

int main() {
  test_barcode_filter();
  test_duplicate_cooldown();
  test_scanner_state();
  test_ring_buffer();
  test_display_model();
  test_protocol_json();
  test_hid_keycode();
  test_scanner_controller_flow();
  test_portal_submission_handoff();
  test_trigger_button_wiring();

  if (failures != 0) {
    std::cerr << failures << " test assertion(s) failed\n";
    return EXIT_FAILURE;
  }
  std::cout << "OK: firmware scanner core tests\n";
  return EXIT_SUCCESS;
}
