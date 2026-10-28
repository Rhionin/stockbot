#include <cstdlib>
#include <cstring>
#include <iostream>
#include <vector>

#include "barcode_filter.h"
#include "display_model.h"
#include "ring_buffer.h"
#include "scanner_state.h"

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

}  // namespace

int main() {
  test_barcode_filter();
  test_duplicate_cooldown();
  test_scanner_state();
  test_ring_buffer();
  test_display_model();

  if (failures != 0) {
    std::cerr << failures << " test assertion(s) failed\n";
    return EXIT_FAILURE;
  }
  std::cout << "OK: firmware scanner core tests\n";
  return EXIT_SUCCESS;
}
