#include <cstdlib>
#include <cstring>
#include <functional>
#include <iostream>
#include <vector>

#include "barcode_filter.h"
#include "button_hold_tracker.h"
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
#include "scanner_setup_network.h"
#include "setup_portal_copy.h"

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
  EXPECT(isPairingCode("SBP-0000-0014-U"));
  EXPECT(!isPairingCode("SBP-0000-0014-G"));
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
  EXPECT(!acceptWithDuplicateCooldown(state, "4011", Direction::StockOut, 2999, false));
  EXPECT(acceptWithDuplicateCooldown(state, "4011", Direction::StockOut, 3000, false));
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
  EXPECT(st.classifyButtonHold(9990) == ButtonHoldGesture::WifiUpdate);
  EXPECT(st.classifyButtonHold(10000) == ButtonHoldGesture::FactoryReset);
}

void test_ring_buffer() {
  MemoryRingStorage storage;
  stockbot::RingBuffer ring(storage);
  EXPECT(ring.recoverFromStorage());
  std::string id;
  for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
    EXPECT(ring.enqueue("4011", stockbot::Direction::StockOut, static_cast<uint64_t>(i), 1, id));
  }
  EXPECT(ring.isFull());
  EXPECT(!ring.enqueue("9999", stockbot::Direction::StockOut, 9999, 1, id));
  auto batch = ring.peekBatch(20);
  EXPECT(batch.size() == 20);
  EXPECT(std::string(batch.front().barcode) == "4011");
  EXPECT(ring.acknowledge(batch.front().client_scan_id));
  EXPECT(ring.count() == stockbot::RingBuffer::kCapacity - 1);

  MemoryRingStorage storage2;
  {
    stockbot::RingBuffer r1(storage2);
    r1.enqueue("123", stockbot::Direction::StockIn, 1, 1, id);
  }
  stockbot::RingBuffer r2(storage2);
  EXPECT(r2.recoverFromStorage());
  EXPECT(r2.count() == 1);

  MemoryRingStorage storage3;
  {
    stockbot::RingBuffer writer(storage3);
    writer.enqueue("4011", stockbot::Direction::StockOut, 500, 7, id);
  }
  std::vector<stockbot::RingScanRecord> persisted;
  storage3.readAll(persisted);
  EXPECT(!persisted.empty());
  persisted[0].crc32 ^= 0x00ffffffu;
  storage3.writeAll(persisted);
  stockbot::RingBuffer corrupt(storage3);
  EXPECT(corrupt.recoverFromStorage());
  EXPECT(corrupt.count() == 0);
}

void test_display_model() {
  using stockbot::CommitMode;
  using stockbot::Direction;
  using stockbot::DisplayModelInput;
  using stockbot::ScanResultView;
  using stockbot::ScanStatusKind;
  using stockbot::UiScreen;
  using stockbot::AudioCue;
  using stockbot::buildDisplayModel;
  using stockbot::wrapProductNameTwoLines;
  using stockbot::formatBand2BarcodeKeepEnd;
  using stockbot::kBand2CharsPerLine;

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
  EXPECT(q.band3 == "Queued -1 - 6 on hand");

  in.last_scan.on_hand = 0;
  in.last_scan.queued_count = 1;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "Queued -1 - 0 on hand - Check app");

  in.last_scan.direction = Direction::StockIn;
  in.last_scan.on_hand = 4;
  in.last_scan.queued_count = 2;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "Queued +2 - 4 on hand");

  in.commit_mode = CommitMode::Immediate;
  in.last_scan.direction = Direction::StockIn;
  in.last_scan.on_hand = 5;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "+1 - 5 on hand");

  in.last_scan.direction = Direction::StockOut;
  in.last_scan.on_hand = 3;
  in.last_scan.backfilled = false;
  in.last_scan.delta = -1;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "-1 - 3 left");
  EXPECT(q.audio == AudioCue::MediumBeep);

  in.last_scan.on_hand = 0;
  in.last_scan.backfilled = false;
  in.last_scan.delta = -1;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "-1 - 0 left (Out of stock)");
  EXPECT(q.audio == AudioCue::DoubleBeep);

  in.last_scan.backfilled = true;
  in.last_scan.delta = -1;
  q = buildDisplayModel(in);
  EXPECT(q.band3 == "0 on hand - Added & used 1");
  EXPECT(q.audio == AudioCue::SoftDoublePulse);
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
  EXPECT(err.band2_line1 == "Disconnected");
  EXPECT(err.band2_line2 == "from household");
  EXPECT(err.band3 == "Needs a new setup sheet");

  in.screen = UiScreen::S0_Unprovisioned;
  in.pairing_ap_suffix = "ABCD";
  auto s0 = buildDisplayModel(in);
  EXPECT(s0.band3 == "Then follow the steps on your phone");
  EXPECT(s0.band2_line2 == "Scanner-Setup-ABCD");
  if (s0.band1.find("QR") != std::string::npos || s0.band2_line1.find("QR") != std::string::npos ||
      s0.band2_line2.find("QR") != std::string::npos || s0.band3.find("QR") != std::string::npos) {
    expect(false, "S0 strings must not mention QR");
  }

  in.screen = UiScreen::S1_Pairing;
  auto s1 = buildDisplayModel(in);
  EXPECT(s1.band2_line1 == "Scan the pairing");
  EXPECT(s1.band2_line2 == "barcode to connect");

  in.screen = UiScreen::S_Connected;
  in.household_name = "Test Household";
  auto connected = buildDisplayModel(in);
  EXPECT(connected.band1 == "CONNECTED");
  EXPECT(connected.band3 == "Ready to scan");
  EXPECT(connected.band2_line1 == "Test Household");

  in.screen = UiScreen::S2_Idle;
  in.default_direction = Direction::StockIn;
  in.active_direction = Direction::StockOut;
  in.show_revert_countdown = true;
  in.inactivity_revert_seconds = 180;
  in.last_activity_ms = 1000;
  in.now_ms = 1000 + 60000;
  auto countdown_in = buildDisplayModel(in);
  EXPECT(countdown_in.band3 == "Back to Stock in: 2:00");

  in.screen = UiScreen::S_PairingExpired;
  auto expired = buildDisplayModel(in);
  EXPECT(expired.band1 == "PAIRING CODE EXPIRED");
  EXPECT(expired.band2_line1 == "Print a new");
  EXPECT(expired.band2_line2 == "setup sheet");

  in.screen = UiScreen::S_WifiButtonHold;
  auto hold = buildDisplayModel(in);
  EXPECT(hold.band1 == "HOLDING");
  EXPECT(hold.band2_line1 == "Let go now to");
  EXPECT(hold.band2_line2 == "update Wi-Fi");
  EXPECT(hold.band3 == "Keep holding to erase everything");

  in.screen = UiScreen::S_WifiButtonHoldErase;
  auto erase_hold = buildDisplayModel(in);
  EXPECT(erase_hold.band1 == "LET GO TO ERASE");
  EXPECT(erase_hold.band2_line1 == "Erases Wi-Fi,");
  EXPECT(erase_hold.band2_line2 == "pairing & scans");
  EXPECT(erase_hold.band3 == "Then use a setup sheet");

  in.screen = UiScreen::S7_HardError;
  in.device_revoked = false;
  in.buffer_full = true;
  auto full = buildDisplayModel(in);
  const std::string capacity_line =
      std::to_string(stockbot::RingBuffer::kCapacity) + " scans waiting";
  EXPECT(full.band1 == "MEMORY FULL");
  EXPECT(full.band2_line1 == capacity_line);
  EXPECT(full.band2_line2 == "Scans won't save");
  EXPECT(full.band3 == "Connect Wi-Fi to send");
  EXPECT(full.audio == AudioCue::Silent);

  in.screen = UiScreen::S7_OfflineBufferRefused;
  in.last_scan.barcode = "4011";
  auto refused = buildDisplayModel(in);
  EXPECT(refused.band1 == "NOT SAVED");
  EXPECT(refused.band2_line1 == "Memory is full");
  EXPECT(refused.band2_line2 == "4011");
  EXPECT(refused.band3 == "Connect Wi-Fi to send");
  EXPECT(refused.audio == AudioCue::Silent);

  in.last_scan.barcode = "4000000000011";
  refused = buildDisplayModel(in);
  EXPECT(refused.band2_line2 == "4000000000011");

  in.last_scan.barcode = "012345678905";
  refused = buildDisplayModel(in);
  EXPECT(refused.band2_line2 == "012345678905");
}

void test_display_band2_text() {
  using stockbot::DisplayModelInput;
  using stockbot::UiScreen;
  using stockbot::buildDisplayModel;
  using stockbot::formatBand2BarcodeKeepEnd;
  using stockbot::kBand2CharsPerLine;
  using stockbot::kEllipsis;
  using stockbot::wrapProductNameTwoLines;

  EXPECT(kBand2CharsPerLine == 18);
  EXPECT(std::string(kEllipsis) == "...");

  const std::string bc18 = "012345678901234567";
  EXPECT(bc18.size() == 18);
  EXPECT(formatBand2BarcodeKeepEnd(bc18) == bc18);

  const std::string bc19 = "0123456789012345678";
  EXPECT(bc19.size() == 19);
  EXPECT(formatBand2BarcodeKeepEnd(bc19) == "...456789012345678");

  DisplayModelInput in;
  in.screen = UiScreen::S7_OfflineBufferRefused;
  in.last_scan.barcode = bc18;
  EXPECT(buildDisplayModel(in).band2_line2 == bc18);
  in.last_scan.barcode = bc19;
  EXPECT(buildDisplayModel(in).band2_line2 == "...456789012345678");

  in.screen = UiScreen::S_Connected;
  in.household_name = "Test Household";
  auto one_line = buildDisplayModel(in);
  EXPECT(one_line.band2_line1 == "Test Household");
  EXPECT(one_line.band2_line2.empty());

  in.household_name = "North Test Household";
  auto wrapped_house = buildDisplayModel(in);
  EXPECT(wrapped_house.band2_line1 == "North Test");
  EXPECT(wrapped_house.band2_line2 == "Household");

  in.household_name = "North Test Housexx";
  EXPECT(in.household_name.size() == 18);
  auto eighteen = buildDisplayModel(in);
  EXPECT(eighteen.band2_line1 == "North Test Housexx");
  EXPECT(eighteen.band2_line2.empty());

  const std::string overflow_name = std::string(18, 'A') + std::string(19, 'B');
  in.household_name = overflow_name;
  auto overflow = buildDisplayModel(in);
  EXPECT(overflow.band2_line1 == std::string(18, 'A'));
  EXPECT(overflow.band2_line2 == std::string(15, 'B') + "...");

  const std::string multibyte = "Ch\u00e2teau du Supermarch\u00e9 du Nord";
  in.household_name = multibyte;
  auto mb = buildDisplayModel(in);
  EXPECT(mb.band2_line1 == "Ch\u00e2teau du");
  EXPECT(mb.band2_line2 == "Supermarch\u00e9 du ...");

  auto product_wrap = wrapProductNameTwoLines(
      "Kirkland Organic Diced Tomatoes Extra Long Product Name Here That Keeps Going");
  EXPECT(product_wrap.first == "Kirkland Organic");
  EXPECT(product_wrap.second == "Diced Tomatoes ...");
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

  stockbot::HttpCallResult sendScan(const stockbot::RingScanRecord& rec, const std::string&, uint64_t age_ms,
                                    bool time_approx, stockbot::ParsedDeviceScanResponse& out) override {
    last_send_barcode = rec.barcode;
    last_send_scan_id = rec.client_scan_id;
    last_send_age_ms = age_ms;
    last_send_time_approx = time_approx;
    send_order.push_back(rec.barcode);
    out = scan_body;
    return next_scan;
  }

  std::string last_send_barcode;
  std::string last_send_scan_id;
  uint64_t last_send_age_ms = 0;
  bool last_send_time_approx = false;
  std::vector<std::string> send_order;

  stockbot::HttpCallResult flushBatch(const std::vector<stockbot::RingScanRecord>& batch,
                                      const std::string& boot_id, uint64_t now_ms,
                                      stockbot::ParsedDeviceScanResponse& out) override {
    last_flush_batch = batch;
    last_flush_boot = boot_id;
    last_flush_now_ms = now_ms;
    last_flush_body = stockbot::buildBatchScansRequestJson(batch, boot_id, now_ms);
    out = scan_body;
    return next_scan;
  }

  std::vector<stockbot::RingScanRecord> last_flush_batch;
  std::string last_flush_boot;
  uint64_t last_flush_now_ms = 0;
  std::string last_flush_body;
  std::string last_heartbeat_body;

  stockbot::HttpCallResult sendHeartbeat(const std::string& hub_version, const std::string& boot_id,
                                         uint32_t uptime_s, size_t queued, int battery_pct, bool charging,
                                         stockbot::HeartbeatParsed& out) override {
    last_heartbeat_body =
        stockbot::buildHeartbeatRequestJson(hub_version, boot_id, uptime_s, queued, battery_pct, charging,
                                            true);
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
  const std::string pair_with_name =
      R"({"deviceId":"dev-1","token":"sbd_testtoken","householdName":"Pantry","config":{"label":"Room","defaultDirection":"stock_out","inactivityRevertSeconds":300,"commitMode":"queue","configVersion":2}})";
  EXPECT(stockbot::parsePairCompleteResponseJson(201, pair_with_name, parsed));
  EXPECT(parsed.household_name == "Pantry");

  stockbot::RingScanRecord rec{};
  std::strncpy(rec.client_scan_id, "scan-1", sizeof(rec.client_scan_id));
  std::strncpy(rec.barcode, "4011", sizeof(rec.barcode));
  rec.direction = 0;
  rec.scanned_ms = 1000;
  const std::string scan_req = stockbot::buildSingleScanRequestJson(rec, "boot-1", 5500, false);
  EXPECT(scan_req.find("\"unitCount\":1") != std::string::npos);
  EXPECT(scan_req.find("\"ageMs\":5500") != std::string::npos);

  std::vector<stockbot::RingScanRecord> batch{rec};
  stockbot::RingScanRecord rec2 = rec;
  std::strncpy(rec2.client_scan_id, "scan-2", sizeof(rec2.client_scan_id));
  rec2.scanned_ms = 2000;
  batch.push_back(rec2);
  const std::string batch_req = stockbot::buildBatchScansRequestJson(batch, "boot-1", 5000);
  EXPECT(batch_req.find("\"ageMs\":4000") != std::string::npos);
  EXPECT(batch_req.find("\"ageMs\":3000") != std::string::npos);
  EXPECT(batch_req.find("\"ageMs\":4000") < batch_req.find("\"ageMs\":3000"));

  stockbot::ParsedDeviceScanResponse scan_out;
  const std::string scan_resp =
      R"({"id":"scan-1","status":"created","scanStatus":"queued","known":true,"productName":"Milk","barcode":"4011","direction":"stock_out","queuedCount":1,"onHand":6,"delta":0,"backfilled":false})";
  EXPECT(stockbot::parseDeviceScanResponseJson(200, scan_resp, scan_out));
  EXPECT(scan_out.status == "created");
  EXPECT(scan_out.on_hand == 6);

  const std::string batch_resp =
      R"({"results":[{"id":"scan-a","status":"created","onHand":1},{"id":"scan-b","status":"rejected","onHand":0}],"config":{}})";
  std::vector<stockbot::BatchScanResultItem> batch_items;
  EXPECT(stockbot::parseDeviceScanBatchResponseJson(200, batch_resp, batch_items));
  EXPECT(batch_items.size() == 2);
  EXPECT(batch_items[0].id == "scan-a");
  EXPECT(batch_items[1].status == "rejected");

  const std::string bounded =
      R"({"results":[{"id":"inner","status":"created","onHand":2}],"onHand":99})";
  EXPECT(stockbot::parseDeviceScanResponseJson(200, bounded, scan_out));
  EXPECT(scan_out.on_hand == 2);

  stockbot::HeartbeatParsed hb;
  const std::string hb_resp =
      R"({"config":{"defaultDirection":"stock_in","inactivityRevertSeconds":600,"commitMode":"immediate","configVersion":9},"rotateToken":false})";
  EXPECT(stockbot::parseHeartbeatResponseJson(200, hb_resp, hb));
  EXPECT(hb.config.default_direction == "stock_in");

  const std::string hb_req = stockbot::buildHeartbeatRequestJson("1.0.0-esp32", "boot-1", 60, 3, 80, true, true);
  EXPECT(hb_req.find("\"droppedCount\":0") != std::string::npos);
  EXPECT(hb_req.find("\"queued\":3") != std::string::npos);

  stockbot::SetupQrParsed setup;
  EXPECT(stockbot::parseSetupQrPayload(R"({"v":1,"ssid":"Home","pass":"pw","url":"https://x","code":"SBP-0123-ABCD-G"})", setup));
  EXPECT(setup.ssid == "Home");

  EXPECT(stockbot::pairingCompleteFailureIsExpired(true, 404, R"({"code":"not_found"})"));
  EXPECT(!stockbot::pairingCompleteFailureIsExpired(false, 0, ""));
  EXPECT(!stockbot::pairingCompleteFailureIsExpired(true, 503, R"({"code":"busy"})"));
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
  wifi.connected = true;
  controller.tick(3500);
  controller.onBarcodeLine("4011", true, 4000);
  EXPECT(display.last.band3.find("Queued -1") != std::string::npos);

  stockbot::RingBuffer ring2(storage);
  stockbot::ScannerController revoked(scanner, display, feedback, power, nvs, portal, client, wifi,
                                       ring2);
  nvs.cfg.device_token = "sbd_abc";
  revoked.begin(5000);
  wifi.connected = true;
  revoked.tick(5050);
  client.next_scan = {true, 401, ""};
  client.scan_body = {};
  client.scan_body.unauthorized = true;
  client.scan_body.parse_ok = true;
  revoked.onBarcodeLine("4012", true, 5100);
  EXPECT(revoked.operationalState() == stockbot::OperationalState::Paired);
  EXPECT(revoked.uiScreen() == stockbot::UiScreen::S7_HardError);
  EXPECT(display.last.band1 == "SCANNER ALERT");
  EXPECT(ring2.count() == 0);

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

void test_offline_fifo_flush_order() {
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
  nvs.cfg.commit_mode = stockbot::CommitMode::Immediate;
  controller.begin(0);

  client.next_scan = {false, 0, ""};
  controller.onBarcodeLine("011111111111", false, 1000);
  controller.onBarcodeLine("022222222222", false, 2000);
  EXPECT(ring.count() == 2);

  const auto pending = ring.peekBatch(2);
  EXPECT(pending.size() == 2);
  const std::string id_a(pending[0].client_scan_id);
  const std::string id_b(pending[1].client_scan_id);

  wifi.connected = true;
  const std::string batch_body = std::string(R"({"results":[{"id":")") + id_a +
                               R"(","status":"created"},{"id":")" + id_b + R"(","status":"created"}]})";
  client.next_scan = {true, 200, batch_body};
  client.scan_body.status = "created";
  controller.tick(5000);

  EXPECT(client.last_flush_batch.size() == 2);
  EXPECT(std::string(client.last_flush_batch[0].barcode) == "011111111111");
  EXPECT(std::string(client.last_flush_batch[1].barcode) == "022222222222");
  EXPECT(client.last_flush_body.find("\"ageMs\":4000") != std::string::npos);
  EXPECT(client.last_flush_body.find("\"ageMs\":3000") != std::string::npos);
  EXPECT(ring.count() == 0);
}

void test_client_scan_id_unique_across_reboot() {
  MemoryRingStorage storage_a;
  MemoryRingStorage storage_b;
  MockScanner scanner;
  MockDisplay display;
  MockFeedback feedback;
  MockPower power;
  MockNvs nvs;
  MockPortal portal;
  MockClient client;
  MockWifi wifi;
  wifi.connected = true;

  nvs.cfg.wifi_ssid = "Home";
  nvs.cfg.device_token = "sbd_test";

  stockbot::RingBuffer ring_a(storage_a);
  stockbot::ScannerController first(scanner, display, feedback, power, nvs, portal, client, wifi, ring_a);
  first.begin(100);
  first.tick(150);
  client.next_scan = {true, 200, R"({"id":"x","status":"created","scanStatus":"committed","known":true})"};
  client.scan_body.status = "created";
  client.scan_body.scan_status = "committed";
  client.scan_body.known = true;
  client.scan_body.parse_ok = true;
  first.onBarcodeLine("4011", false, 500);
  const std::string id_first = client.last_send_scan_id;
  EXPECT(!id_first.empty());
  EXPECT(ring_a.count() == 0);

  stockbot::RingBuffer ring_b(storage_b);
  stockbot::ScannerController second(scanner, display, feedback, power, nvs, portal, client, wifi, ring_b);
  second.begin(100);
  second.tick(150);
  second.onBarcodeLine("4011", false, 500);
  const std::string id_second = client.last_send_scan_id;
  EXPECT(!id_second.empty());
  EXPECT(id_first != id_second);
}

struct ControllerFixture {
  MemoryRingStorage storage;
  stockbot::RingBuffer ring{storage};
  MockScanner scanner;
  MockDisplay display;
  MockFeedback feedback;
  MockPower power;
  MockNvs nvs;
  MockPortal portal;
  MockClient client;
  MockWifi wifi;
  stockbot::ScannerController controller{scanner, display, feedback, power, nvs, portal, client, wifi,
                                         ring};

  void beginPaired(uint64_t now_ms = 0) {
    nvs.cfg.wifi_ssid = "Home";
    nvs.cfg.device_token = "sbd_offline";
    nvs.cfg.commit_mode = stockbot::CommitMode::Immediate;
    controller.begin(now_ms);
    controller.tick(now_ms + 10);
  }

  void setOfflineScanTransport() { client.next_scan = {false, 0, ""}; }

  std::string barcodeAt(size_t index) const {
    char bc[16];
    std::snprintf(bc, sizeof(bc), "%012zu", index + 1);
    return bc;
  }

  void fillRingToCapacity(uint64_t start_ms) {
    setOfflineScanTransport();
    for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
      controller.onBarcodeLine(barcodeAt(i), false, start_ms + static_cast<uint64_t>(i));
    }
  }

  size_t tripleErrorBuzzCount() const {
    size_t n = 0;
    for (const auto cue : feedback.cues) {
      if (cue == stockbot::AudioCue::TripleErrorBuzz) {
        ++n;
      }
    }
    return n;
  }

  std::string capacityWaitingLine() const {
    return std::to_string(stockbot::RingBuffer::kCapacity) + " scans waiting";
  }

  void expectMemoryFullIdleScreen() const {
    EXPECT(display.last.band1 == "MEMORY FULL");
    EXPECT(display.last.band2_line1 == capacityWaitingLine());
    EXPECT(display.last.band2_line2 == "Scans won't save");
    EXPECT(display.last.band3 == "Connect Wi-Fi to send");
    EXPECT(display.last.audio == stockbot::AudioCue::Silent);
  }

  void expectBufferRefusedScreen(const std::string& barcode_line) const {
    EXPECT(display.last.band1 == "NOT SAVED");
    EXPECT(display.last.band2_line1 == "Memory is full");
    EXPECT(display.last.band2_line2 == barcode_line);
    EXPECT(display.last.band3 == "Connect Wi-Fi to send");
    EXPECT(display.last.audio == stockbot::AudioCue::Silent);
  }
};

void test_offline_buffer_full_refuses_without_eviction() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.controller.tick(8000);
  EXPECT(fx.ring.count() == stockbot::RingBuffer::kCapacity);
  EXPECT(fx.ring.isFull());
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_HardError);
  fx.expectMemoryFullIdleScreen();

  const auto before = fx.ring.peekBatch(stockbot::RingBuffer::kCapacity);
  EXPECT(before.size() == stockbot::RingBuffer::kCapacity);
  const size_t buzz_before = fx.tripleErrorBuzzCount();
  fx.controller.onBarcodeLine("999999999999", false, 9000);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("999999999999");
  // E6: fails if feedbackOfflineBufferRefused stops setting S7_OfflineBufferRefused (render()
  // from isFull() alone would leave S7_HardError / MEMORY FULL copy, not NOT SAVED).
  EXPECT(fx.ring.count() == stockbot::RingBuffer::kCapacity);
  EXPECT(fx.ring.isFull());
  const auto after = fx.ring.peekBatch(stockbot::RingBuffer::kCapacity);
  EXPECT(after.size() == before.size());
  for (size_t i = 0; i < before.size(); ++i) {
    EXPECT(std::string(after[i].barcode) == std::string(before[i].barcode));
    EXPECT(std::string(after[i].client_scan_id) == std::string(before[i].client_scan_id));
  }
  EXPECT(fx.tripleErrorBuzzCount() > buzz_before);
}

void test_offline_buffer_full_clears_after_flush_ack() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  EXPECT(fx.ring.isFull());

  fx.wifi.connected = true;
  const auto pending = fx.ring.peekBatch(50);
  EXPECT(pending.size() == 50);
  std::string batch_body = R"({"results":[)";
  for (size_t i = 0; i < pending.size(); ++i) {
    if (i > 0) {
      batch_body += ',';
    }
    batch_body += R"({"id":")" + std::string(pending[i].client_scan_id) + R"(","status":"created"})";
  }
  batch_body += "]}";
  fx.client.next_scan = {true, 200, batch_body};
  fx.client.scan_body.status = "created";
  fx.controller.tick(20000);

  EXPECT(!fx.ring.isFull());
  EXPECT(fx.ring.count() == stockbot::RingBuffer::kCapacity - 50);
  EXPECT(fx.controller.uiScreen() != stockbot::UiScreen::S7_HardError);

  fx.client.next_scan = {false, 0, ""};
  fx.controller.onBarcodeLine("4011", false, 21000);
  EXPECT(fx.ring.count() == stockbot::RingBuffer::kCapacity - 49);
}

void test_offline_buffer_full_persists_across_reboot_until_flush() {
  MemoryRingStorage storage;
  MockScanner scanner;
  MockDisplay display;
  MockFeedback feedback;
  MockPower power;
  MockNvs nvs;
  MockPortal portal;
  MockClient client;
  MockWifi wifi;

  nvs.cfg.wifi_ssid = "Home";
  nvs.cfg.device_token = "sbd_persist";

  stockbot::RingBuffer ring_a(storage);
  stockbot::ScannerController first(scanner, display, feedback, power, nvs, portal, client, wifi, ring_a);
  first.begin(0);
  client.next_scan = {false, 0, ""};
  for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
    char bc[16];
    std::snprintf(bc, sizeof(bc), "%012zu", i + 1);
    first.onBarcodeLine(bc, false, 1000 + static_cast<uint64_t>(i));
  }
  EXPECT(ring_a.isFull());

  stockbot::RingBuffer ring_b(storage);
  stockbot::ScannerController second(scanner, display, feedback, power, nvs, portal, client, wifi, ring_b);
  second.begin(50000);
  second.tick(50010);
  EXPECT(ring_b.isFull());
  EXPECT(second.uiScreen() == stockbot::UiScreen::S7_HardError);

  wifi.connected = true;
  const auto pending = ring_b.peekBatch(20);
  std::string batch_body = R"({"results":[)";
  for (size_t i = 0; i < pending.size(); ++i) {
    if (i > 0) {
      batch_body += ',';
    }
    batch_body += R"({"id":")" + std::string(pending[i].client_scan_id) + R"(","status":"created"})";
  }
  batch_body += "]}";
  client.next_scan = {true, 200, batch_body};
  client.scan_body.status = "created";
  second.tick(60000);
  EXPECT(!ring_b.isFull());
}

void test_heartbeat_dropped_count_zero() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.client.next_hb = {true, 200, R"({"config":{"configVersion":1}})"};
  fx.client.hb_body.parse_ok = true;
  fx.controller.tick(70000);
  EXPECT(fx.client.last_heartbeat_body.find("\"droppedCount\":0") != std::string::npos);
  EXPECT(fx.client.last_heartbeat_body.find("\"queued\":500") != std::string::npos);
}

void test_offline_buffer_refusal_buzz_immediately_after_boot() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.setOfflineScanTransport();
  for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
    fx.controller.onBarcodeLine(fx.barcodeAt(i), false, static_cast<uint64_t>(i));
  }
  fx.feedback.cues.clear();
  fx.controller.onBarcodeLine("999999999999", false, 500);
  EXPECT(fx.tripleErrorBuzzCount() == 1);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("999999999999");
}

void test_offline_buffer_control_barcodes_refused_while_full() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.controller.tick(8000);
  fx.feedback.cues.clear();

  fx.controller.onBarcodeLine("STOCK-IN", false, 9000);
  EXPECT(fx.tripleErrorBuzzCount() == 1);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("STOCK-IN");

  fx.controller.onBarcodeLine("STOCK-OUT", false, 9100);
  EXPECT(fx.tripleErrorBuzzCount() == 1);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("STOCK-OUT");
}

void test_offline_buffer_refusal_feedback_rate_limited() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.feedback.cues.clear();
  fx.controller.onBarcodeLine("999999999999", false, 5000);
  fx.controller.onBarcodeLine("888888888888", false, 5200);
  fx.controller.onBarcodeLine("777777777777", false, 7000);
  EXPECT(fx.tripleErrorBuzzCount() == 2);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("777777777777");
}

void test_offline_buffer_refusal_rate_limit_boundary() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.controller.tick(8000);
  fx.feedback.cues.clear();
  const uint64_t t0 = 10000;
  fx.controller.onBarcodeLine("999999999999", false, t0);
  EXPECT(fx.tripleErrorBuzzCount() == 1);
  fx.controller.onBarcodeLine("888888888888", false, t0 + 1499);
  EXPECT(fx.tripleErrorBuzzCount() == 1);
  fx.controller.onBarcodeLine("777777777777", false, t0 + 1500);
  EXPECT(fx.tripleErrorBuzzCount() == 2);
}

void test_offline_buffer_refused_transient_timeout() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.controller.tick(8000);
  fx.controller.onBarcodeLine("999999999999", false, 10000);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("999999999999");
  fx.controller.tick(13999);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.controller.tick(14000);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_HardError);
  fx.expectMemoryFullIdleScreen();
}

void test_offline_buffer_refused_transient_restarts() {
  ControllerFixture fx;
  fx.beginPaired(0);
  fx.fillRingToCapacity(1000);
  fx.controller.tick(8000);
  fx.controller.onBarcodeLine("999999999999", false, 10000);
  fx.controller.onBarcodeLine("888888888888", false, 12000);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.expectBufferRefusedScreen("888888888888");
  fx.controller.tick(15999);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_OfflineBufferRefused);
  fx.controller.tick(16000);
  EXPECT(fx.controller.uiScreen() == stockbot::UiScreen::S7_HardError);
  fx.expectMemoryFullIdleScreen();
}

void test_repair_after_disconnect() {
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
  nvs.cfg.device_token = "sbd_abc";
  controller.begin(1000);
  wifi.connected = true;
  controller.tick(1100);
  client.next_scan = {true, 401, ""};
  client.scan_body.unauthorized = true;
  client.scan_body.parse_ok = true;
  feedback.cues.clear();
  controller.onBarcodeLine("4011", false, 2000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Paired);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S7_HardError);
  EXPECT(display.last.band1 == "SCANNER ALERT");
  EXPECT(display.last.band2_line1 == "Disconnected");
  EXPECT(!feedback.cues.empty());
  EXPECT(feedback.cues.back() == stockbot::AudioCue::TripleErrorBuzz);
  EXPECT(nvs.cfg.device_token.empty());
  EXPECT(client.token.empty());

  client.next_pair = {true, 201, ""};
  client.pair_body = {};
  client.pair_body.device_token = "sbd_new";
  client.pair_body.household_name = "Test Household";
  feedback.cues.clear();
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 3000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Paired);
  EXPECT(display.last.band1 == "CONNECTED");
  EXPECT(display.last.band2_line1 == "Test Household");
  EXPECT(!feedback.cues.empty());
  EXPECT(feedback.cues.back() == stockbot::AudioCue::AscendingPairSuccess);
}

void test_ap_suffix_from_mac() {
  EXPECT(stockbot::apSuffixFromMacAddress("AA:BB:CC:DD:EE:FF") == "EEFF");
  EXPECT(stockbot::apSuffixFromMacAddress("aa-bb-cc-dd-ee-ff") == "EEFF");
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
  stockbot::ScannerController a(scanner, display, feedback, power, nvs, portal, client, wifi, ring);
  a.begin(0, 0, "AA:BB:CC:DD:11:22");
  stockbot::RingBuffer ring2(storage);
  stockbot::ScannerController b(scanner, display, feedback, power, nvs, portal, client, wifi, ring2);
  b.begin(0, 0, "AA:BB:CC:DD:33:44");
  EXPECT(a.apSuffixForTest() == "1122");
  EXPECT(b.apSuffixForTest() == "3344");
  EXPECT(a.apSuffixForTest() != b.apSuffixForTest());
}

void test_wifi_button_hold_feedback() {
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
  controller.onModeButtonHoldTick(5500, 2000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiButtonHold);
  EXPECT(display.last.band1 == "HOLDING");
  EXPECT(display.last.band2_line1 == "Let go now to");
  EXPECT(display.last.band2_line2 == "update Wi-Fi");
  EXPECT(display.last.band3 == "Keep holding to erase everything");

  controller.onModeButtonHoldTick(9999, 2100);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiButtonHold);

  controller.onModeButtonHoldTick(10000, 2200);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiButtonHoldErase);
  EXPECT(display.last.band1 == "LET GO TO ERASE");

  controller.onModeButtonHold(5500, 2300);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiSetup);
  EXPECT(display.last.band1 == "WI-FI SETUP");
  EXPECT(display.last.band2_line2 == "Scanner-Setup-0000");

  stockbot::ScannerController erase_ctl(scanner, display, feedback, power, nvs, portal, client, wifi, ring);
  nvs.cfg.wifi_ssid = "Home";
  nvs.cfg.device_token = "sbd_x";
  erase_ctl.begin(3000);
  erase_ctl.onModeButtonHoldTick(10000, 4000);
  EXPECT(erase_ctl.uiScreen() == stockbot::UiScreen::S_WifiButtonHoldErase);
  erase_ctl.onModeButtonHold(10000, 4100);
  EXPECT(erase_ctl.operationalState() == stockbot::OperationalState::Unprovisioned);
}

void test_pairing_code_expired_screen() {
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
  controller.begin(1000);
  client.next_pair = {true, 404, R"({"code":"not_found","error":"not found"})"};
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 2000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingExpired);
  EXPECT(display.last.band1 == "PAIRING CODE EXPIRED");
  EXPECT(display.last.band2_line1 == "Print a new");
  EXPECT(display.last.band2_line2 == "setup sheet");
}

void test_pairing_cant_connect_screen() {
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
  controller.begin(1000);

  client.next_pair = {false, 0, ""};
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 2000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingCantConnect);
  EXPECT(display.last.band1 == "CAN'T CONNECT");
  EXPECT(display.last.band2_line1 == "Couldn't reach");
  EXPECT(display.last.band2_line2 == "the server");
  EXPECT(display.last.band3 == "Check Wi-Fi, then scan again");

  controller.begin(3000);
  client.next_pair = {true, 503, R"({"code":"busy","error":"busy"})"};
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 3100);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingCantConnect);
}

void test_disconnect_clears_persisted_ring() {
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
  controller.begin(0);
  client.next_scan = {false, 0, ""};
  controller.onBarcodeLine("4011", false, 1000);
  EXPECT(ring.count() == 1);
  std::vector<stockbot::RingScanRecord> persisted;
  storage.readAll(persisted);
  EXPECT(!persisted.empty());
  wifi.connected = true;
  controller.tick(1500);
  client.next_scan = {true, 401, ""};
  client.scan_body.unauthorized = true;
  client.scan_body.parse_ok = true;
  controller.onBarcodeLine("4012", false, 2000);
  EXPECT(ring.count() == 0);
  storage.readAll(persisted);
  EXPECT(persisted.empty());
}

void test_pairing_rate_limited_and_rejected_screens() {
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
  controller.begin(1000);

  client.next_pair = {true, 429, R"({"code":"rate_limited"})"};
  feedback.cues.clear();
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 2000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingTooManyTries);
  EXPECT(display.last.band1 == "TOO MANY TRIES");
  EXPECT(feedback.cues.back() == stockbot::AudioCue::ScanBeepLowTick);

  controller.begin(3000);
  client.next_pair = {true, 400, R"({"code":"validation"})"};
  feedback.cues.clear();
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 3100);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingCodeNotAccepted);
  EXPECT(display.last.band1 == "CODE NOT ACCEPTED");
  EXPECT(feedback.cues.back() == stockbot::AudioCue::TripleErrorBuzz);

  controller.begin(4000);
  client.next_pair = {true, 404, R"({"code":"busy"})"};
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 4100);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingCodeNotAccepted);
}

void test_pairing_error_transient_returns_to_pairing() {
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
  controller.begin(1000);
  client.next_pair = {true, 404, R"({"code":"not_found"})"};
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 2000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingExpired);
  controller.tick(5999);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_PairingExpired);
  controller.tick(6000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S1_Pairing);
}

void test_device_revoked_flush_and_heartbeat_401() {
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
  wifi.connected = true;
  std::string id;
  ring.enqueue("4011", stockbot::Direction::StockOut, 1100, 1, id);
  client.next_scan = {true, 401, R"({"code":"device_revoked"})"};
  client.scan_body.unauthorized = true;
  client.scan_body.parse_ok = true;
  controller.tick(1200);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S7_HardError);
  EXPECT(nvs.cfg.device_token.empty());

  nvs.cfg.device_token = "sbd_y";
  client.setDeviceToken("sbd_y");
  controller.begin(5000);
  wifi.connected = true;
  client.next_hb = {true, 401, R"({"code":"device_revoked"})"};
  controller.tick(70000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S7_HardError);
  EXPECT(nvs.cfg.device_token.empty());
}

void test_finish_pairing_clears_ring_before_token() {
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
  controller.begin(1000);
  std::string id;
  ring.enqueue("4011", stockbot::Direction::StockOut, 1100, 1, id);
  EXPECT(ring.count() == 1);
  client.next_pair = {true, 201, ""};
  client.pair_body.device_token = "sbd_new";
  controller.onBarcodeLine("SBP-0123-ABCD-G", false, 2000);
  EXPECT(ring.count() == 0);
  EXPECT(nvs.cfg.device_token == "sbd_new");
}

void test_factory_reset_clears_ring() {
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
  std::string id;
  ring.enqueue("4011", stockbot::Direction::StockOut, 1100, 1, id);
  controller.onModeButtonHold(10000, 2000);
  EXPECT(ring.count() == 0);
  std::vector<stockbot::RingScanRecord> persisted;
  storage.readAll(persisted);
  EXPECT(persisted.empty());
}

void test_wifi_button_hold_tick_at_5000_boundary() {
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
  controller.onModeButtonHoldTick(4999, 2000);
  EXPECT(controller.uiScreen() != stockbot::UiScreen::S_WifiButtonHold);
  controller.onModeButtonHoldTick(5000, 2100);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiButtonHold);
}

void test_protocol_json_pairing_failures() {
  EXPECT(stockbot::pairingCompleteFailureIsRateLimited(true, 429, R"({"code":"rate_limited"})"));
  EXPECT(stockbot::pairingCompleteFailureIsClientRejected(true, 410, R"({"code":"reset_expired"})"));
  EXPECT(!stockbot::pairingCompleteFailureIsExpired(true, 404, R"({"code":"busy"})"));
  EXPECT(stockbot::pairingCompleteFailureIsClientRejected(true, 404, R"({"code":"busy"})"));
}

void test_scanner_setup_network_and_portal_copy() {
  EXPECT(std::string(stockbot::kScannerSetupApPrefix) == "Scanner-Setup-");
  EXPECT(stockbot::scannerSetupSsid("ABCD") == "Scanner-Setup-ABCD");
  EXPECT(std::string(stockbot::kPortalWifiNameLabel) == "Your home Wi-Fi name");
  EXPECT(std::string(stockbot::kPortalWifiPasswordLabel) == "Wi-Fi password");
  EXPECT(std::string(stockbot::kPortalConnectingMessage) == "Connecting to your Wi-Fi...");
  EXPECT(std::string(stockbot::kPortalSetupHtml).find("<meta charset=\"utf-8\">") != std::string::npos);
  EXPECT(std::string(stockbot::kPortalSetupHtml).find("Wi-Fi password") != std::string::npos);
}

void assertDisplayBandLimits(const stockbot::DisplayBands& bands, const char* context) {
  if (stockbot::displayUtf8CharCount(bands.band2_line1) > stockbot::kBand2CharsPerLine) {
    std::cerr << "FAIL: band2 line1 overflow (" << context << "): [" << bands.band2_line1 << "]\n";
    ++failures;
  }
  if (stockbot::displayUtf8CharCount(bands.band2_line2) > stockbot::kBand2CharsPerLine) {
    std::cerr << "FAIL: band2 line2 overflow (" << context << "): [" << bands.band2_line2 << "]\n";
    ++failures;
  }
  if (stockbot::displayUtf8CharCount(bands.band3) > stockbot::kBand3CharsPerLine) {
    std::cerr << "FAIL: band3 overflow (" << context << "): [" << bands.band3 << "]\n";
    ++failures;
  }
}

void test_display_all_screens_within_band_limits() {
  using stockbot::CommitMode;
  using stockbot::Direction;
  using stockbot::DisplayModelInput;
  using stockbot::ScanStatusKind;
  using stockbot::UiScreen;
  using stockbot::buildDisplayModel;

  const std::string long_household(40, 'H');
  const std::string long_barcode(30, '9');
  const std::string long_product(60, 'P');

  auto stress = [&](UiScreen screen) {
    DisplayModelInput in;
    in.screen = screen;
    in.pairing_ap_suffix = "WXYZ";
    in.household_name = long_household;
    in.connected_label = long_household;
    in.offline_queue_count = stockbot::RingBuffer::kCapacity;
    in.buffer_full = true;
    in.device_revoked = true;
    in.vbus_present = true;
    in.battery_pct = 100;
    in.charging = true;
    in.wifi_update_mode = true;
    in.show_revert_countdown = true;
    in.inactivity_revert_seconds = 599;
    in.last_activity_ms = 0;
    in.now_ms = 1000;
    in.commit_mode = CommitMode::Queue;
    in.active_direction = Direction::StockOut;
    in.default_direction = Direction::StockIn;
    in.last_scan.known = true;
    in.last_scan.status = ScanStatusKind::Queued;
    in.last_scan.product_name = long_product;
    in.last_scan.barcode = long_barcode;
    in.last_scan.direction = Direction::StockOut;
    in.last_scan.on_hand = 0;
    in.last_scan.queued_count = 1;
    return in;
  };

  const UiScreen screens[] = {
      UiScreen::S0_Unprovisioned,      UiScreen::S1_Pairing,         UiScreen::S2_Idle,
      UiScreen::S3_ScanResult,         UiScreen::S4_UnknownBarcode, UiScreen::S5_OfflineBuffered,
      UiScreen::S6_DockReminder,       UiScreen::S7_HardError,       UiScreen::S7_OfflineBufferRefused,
      UiScreen::S_Connected,           UiScreen::S_PairingExpired,   UiScreen::S_PairingTooManyTries,
      UiScreen::S_PairingCodeNotAccepted, UiScreen::S_PairingCantConnect,
      UiScreen::S_WifiButtonHold,      UiScreen::S_WifiButtonHoldErase, UiScreen::S_WifiSetup,
  };
  for (UiScreen screen : screens) {
    auto in = stress(screen);
    assertDisplayBandLimits(buildDisplayModel(in), "stress screen");
  }

  {
    auto in = stress(UiScreen::S3_ScanResult);
    in.commit_mode = CommitMode::Immediate;
    in.active_direction = Direction::StockIn;
    in.last_scan.direction = Direction::StockIn;
    in.last_scan.product_name = "Ch\u00e2teau du Supermarch\u00e9 du Nord";
    assertDisplayBandLimits(buildDisplayModel(in), "S3 immediate utf8 product name");
  }

  {
    auto in = stress(UiScreen::S7_HardError);
    in.device_revoked = false;
    in.buffer_full = true;
    assertDisplayBandLimits(buildDisplayModel(in), "S7 buffer full");
    in.buffer_full = false;
    in.device_revoked = false;
    assertDisplayBandLimits(buildDisplayModel(in), "S7 disconnected");
  }
  {
    auto in = stress(UiScreen::S6_DockReminder);
    in.vbus_present = false;
    in.battery_pct = 100;
    assertDisplayBandLimits(buildDisplayModel(in), "S6 off dock");
  }
}

void test_firmware_strings_no_unicode_ellipsis() {
  const char* literals[] = {
      stockbot::kPortalConnectingMessage,
      stockbot::kPortalWifiPasswordLabel,
      stockbot::kPortalWifiNameLabel,
  };
  for (const char* lit : literals) {
    EXPECT(std::string(lit).find("\xE2\x80\xA6") == std::string::npos);
  }
  stockbot::DisplayModelInput in;
  in.screen = stockbot::UiScreen::S_WifiButtonHold;
  EXPECT(stockbot::buildDisplayModel(in).band1 == "HOLDING");
}

void test_button_hold_tracker() {
  using stockbot::ButtonHoldTracker;
  using stockbot::ModeButtonHoldPhase;
  using stockbot::ModeButtonReleaseAction;

  ButtonHoldTracker tracker;
  tracker.onPress(0);
  EXPECT(tracker.onRelease(4999) == ModeButtonReleaseAction::ShortPress);

  tracker.onPress(0);
  EXPECT(tracker.onRelease(9999) == ModeButtonReleaseAction::WifiSetup);

  tracker.onPress(0);
  EXPECT(tracker.onHoldProgress(10000) == ModeButtonHoldPhase::EraseWarning);
  EXPECT(tracker.onRelease(10000) == ModeButtonReleaseAction::FactoryReset);

  tracker.onPress(0);
  tracker.onHoldProgress(10000);
  tracker.onHoldProgress(30000);
  EXPECT(tracker.isDown());
  EXPECT(tracker.onRelease(30000) == ModeButtonReleaseAction::FactoryReset);

  tracker.onPress(0);
  tracker.onHoldProgress(5500);
  EXPECT(tracker.onRelease(5500) == ModeButtonReleaseAction::WifiSetup);
}

void test_wifi_button_hold_no_erase_while_held() {
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
  controller.onModeButtonDown(2000);
  controller.onModeButtonHoldPoll(12000);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiButtonHoldErase);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Paired);
  controller.onModeButtonHoldPoll(50000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Paired);
  controller.onModeButtonUp(50000);
  EXPECT(controller.operationalState() == stockbot::OperationalState::Unprovisioned);
}

void test_wifi_button_hold_release_at_9999_no_factory_reset() {
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
  controller.onModeButtonDown(9000);
  controller.onModeButtonHoldPoll(18999);
  controller.onModeButtonUp(18999);
  EXPECT(controller.uiScreen() == stockbot::UiScreen::S_WifiSetup);
  EXPECT(nvs.cfg.device_token == "sbd_x");
}

bool isPrintableAscii(const std::string& text) {
  for (unsigned char ch : text) {
    if (ch < 0x20 || ch > 0x7e) {
      return false;
    }
  }
  return true;
}

void collectDisplayStrings(const stockbot::DisplayBands& bands, std::vector<std::string>& out) {
  out.push_back(bands.band1);
  out.push_back(bands.band1_right);
  out.push_back(bands.band2_line1);
  out.push_back(bands.band2_line2);
  out.push_back(bands.band3);
}

void test_display_strings_printable_ascii() {
  using stockbot::CommitMode;
  using stockbot::Direction;
  using stockbot::DisplayModelInput;
  using stockbot::ScanStatusKind;
  using stockbot::UiScreen;
  using stockbot::buildDisplayModel;

  const UiScreen screens[] = {
      UiScreen::S0_Unprovisioned,      UiScreen::S1_Pairing,         UiScreen::S2_Idle,
      UiScreen::S3_ScanResult,         UiScreen::S4_UnknownBarcode, UiScreen::S5_OfflineBuffered,
      UiScreen::S6_DockReminder,       UiScreen::S7_HardError,       UiScreen::S7_OfflineBufferRefused,
      UiScreen::S_Connected,           UiScreen::S_PairingExpired,   UiScreen::S_PairingTooManyTries,
      UiScreen::S_PairingCodeNotAccepted, UiScreen::S_PairingCantConnect,
      UiScreen::S_WifiButtonHold,      UiScreen::S_WifiButtonHoldErase, UiScreen::S_WifiSetup,
  };

  for (UiScreen screen : screens) {
    DisplayModelInput in;
    in.screen = screen;
    in.commit_mode = CommitMode::Queue;
    in.active_direction = Direction::StockOut;
    in.default_direction = Direction::StockIn;
    in.last_scan.known = true;
    in.last_scan.status = ScanStatusKind::Queued;
    in.last_scan.direction = Direction::StockIn;
    in.last_scan.on_hand = 2;
    in.last_scan.queued_count = 1;
    in.show_revert_countdown = true;
    in.inactivity_revert_seconds = 120;
    in.last_activity_ms = 0;
    in.now_ms = 30000;
    in.device_revoked = screen == UiScreen::S7_HardError;
    in.buffer_full = screen == UiScreen::S7_HardError;
    std::vector<std::string> strings;
    collectDisplayStrings(buildDisplayModel(in), strings);
    for (const std::string& s : strings) {
      EXPECT(isPrintableAscii(s));
    }
  }

  const char* copy_literals[] = {
      stockbot::kPortalConnectingMessage,
      stockbot::kPortalWifiPasswordLabel,
      stockbot::kPortalWifiNameLabel,
      stockbot::kScannerSetupApPrefix,
  };
  for (const char* lit : copy_literals) {
    EXPECT(isPrintableAscii(lit));
  }
}

void test_ota_install_gate_placeholder_until_firmware_452() {
  // Documents #452 install precondition (ring empty). No production OTA gate in firmware yet.
  const auto policy_ring_empty_required = [](size_t queued) { return queued == 0; };
  EXPECT(!policy_ring_empty_required(stockbot::RingBuffer::kCapacity));
  EXPECT(policy_ring_empty_required(0));
}

}  // namespace

int main() {
  test_barcode_filter();
  test_duplicate_cooldown();
  test_scanner_state();
  test_ring_buffer();
  test_display_model();
  test_display_band2_text();
  test_display_all_screens_within_band_limits();
  test_firmware_strings_no_unicode_ellipsis();
  test_protocol_json();
  test_protocol_json_pairing_failures();
  test_hid_keycode();
  test_scanner_controller_flow();
  test_portal_submission_handoff();
  test_trigger_button_wiring();
  test_offline_fifo_flush_order();
  test_client_scan_id_unique_across_reboot();
  test_offline_buffer_full_refuses_without_eviction();
  test_offline_buffer_full_clears_after_flush_ack();
  test_offline_buffer_full_persists_across_reboot_until_flush();
  test_heartbeat_dropped_count_zero();
  test_offline_buffer_refusal_buzz_immediately_after_boot();
  test_offline_buffer_control_barcodes_refused_while_full();
  test_offline_buffer_refusal_feedback_rate_limited();
  test_offline_buffer_refusal_rate_limit_boundary();
  test_offline_buffer_refused_transient_timeout();
  test_offline_buffer_refused_transient_restarts();
  test_repair_after_disconnect();
  test_ap_suffix_from_mac();
  test_button_hold_tracker();
  test_wifi_button_hold_no_erase_while_held();
  test_wifi_button_hold_release_at_9999_no_factory_reset();
  test_wifi_button_hold_tick_at_5000_boundary();
  test_wifi_button_hold_feedback();
  test_pairing_code_expired_screen();
  test_pairing_cant_connect_screen();
  test_pairing_rate_limited_and_rejected_screens();
  test_pairing_error_transient_returns_to_pairing();
  test_device_revoked_flush_and_heartbeat_401();
  test_finish_pairing_clears_ring_before_token();
  test_factory_reset_clears_ring();
  test_disconnect_clears_persisted_ring();
  test_scanner_setup_network_and_portal_copy();
  test_display_strings_printable_ascii();
  test_ota_install_gate_placeholder_until_firmware_452();

  if (failures != 0) {
    std::cerr << failures << " test assertion(s) failed\n";
    return EXIT_FAILURE;
  }
  std::cout << "OK: firmware scanner core tests\n";
  return EXIT_SUCCESS;
}
