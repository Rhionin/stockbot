#pragma once

#include <cstdint>
#include <optional>
#include <string>

#include "scanner_state.h"

namespace stockbot {

enum class UiScreen {
  S0_Unprovisioned,
  S1_Pairing,
  S2_Idle,
  S3_ScanResult,
  S4_UnknownBarcode,
  S5_OfflineBuffered,
  S6_DockReminder,
  S7_HardError,
};

enum class AudioCue {
  Silent,
  AscendingPairSuccess,
  RisingTwoTone,
  FallingTwoTone,
  HighBeep,
  MediumBeep,
  DoubleBeep,
  SoftDoublePulse,
  QuestionTone,
  ScanBeepLowTick,
  TripleErrorBuzz,
  ModeRevertChime,
  DockTick,
  OffDockChirp,
};

enum class ScanStatusKind {
  None,
  Queued,
  Committed,
  Flagged,
};

struct ScanResultView {
  ScanStatusKind status = ScanStatusKind::None;
  bool known = false;
  std::string product_name;
  std::string barcode;
  Direction direction = Direction::StockOut;
  int queued_count = 0;
  int on_hand = 0;
  int delta = 0;
  bool backfilled = false;
};

struct DisplayModelInput {
  UiScreen screen = UiScreen::S2_Idle;
  OperationalState operational = OperationalState::Paired;
  Direction active_direction = Direction::StockOut;
  Direction default_direction = Direction::StockOut;
  CommitMode commit_mode = CommitMode::Queue;
  uint32_t inactivity_revert_seconds = 300;
  uint64_t last_activity_ms = 0;
  uint64_t now_ms = 0;
  bool wifi_online = true;
  std::optional<int> battery_pct;
  bool charging = false;
  bool vbus_present = false;
  size_t offline_queue_count = 0;
  bool buffer_full = false;
  bool device_revoked = false;
  std::string pairing_ap_suffix;
  std::string connected_label;
  ScanResultView last_scan;
  bool show_revert_countdown = false;
};

struct DisplayBands {
  std::string band1;
  std::string band1_bg;
  std::string band2_line1;
  std::string band2_line2;
  std::string band3;
  AudioCue audio = AudioCue::Silent;
};

DisplayBands buildDisplayModel(const DisplayModelInput& in);

std::pair<std::string, std::string> wrapProductNameTwoLines(const std::string& name, size_t max_chars_per_line = 20);

std::string formatRevertCountdown(uint32_t inactivity_revert_seconds, uint64_t last_activity_ms, uint64_t now_ms);

}  // namespace stockbot
