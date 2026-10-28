#pragma once

#include <cstdint>
#include <optional>
#include <string>

namespace stockbot {

enum class OperationalState {
  Unprovisioned,
  Pairing,
  Paired,
};

enum class Direction {
  StockOut,
  StockIn,
};

enum class CommitMode {
  Queue,
  Immediate,
};

enum class BacklightLevel {
  Full100,
  Dim20,
  Off0,
};

enum class ButtonHoldGesture {
  ShortPress,
  WifiUpdate,
  FactoryReset,
};

enum class ModeChime {
  None,
  RisingStockIn,
  FallingStockOut,
  ModeRevert,
};

constexpr uint32_t DOCK_SUPPRESS_MS = 1500;

struct ModeButtonResult {
  bool woke_backlight = false;
  bool toggled_direction = false;
  ModeChime chime = ModeChime::None;
};

struct ScannerState {
  OperationalState operational = OperationalState::Unprovisioned;
  Direction active_direction = Direction::StockOut;
  Direction default_direction = Direction::StockOut;
  uint32_t inactivity_revert_seconds = 300;
  CommitMode commit_mode = CommitMode::Queue;
  uint32_t config_version = 0;

  BacklightLevel backlight = BacklightLevel::Full100;
  uint64_t last_activity_ms = 0;
  uint64_t vbus_transition_ms = 0;
  bool vbus_present = false;
  uint64_t mode_button_down_ms = 0;

  ModeButtonResult onModeButtonPress(uint64_t now_ms);
  void onScanOrActivity(uint64_t now_ms);
  void setDirection(Direction dir, uint64_t now_ms, bool play_chime);
  std::optional<ModeChime> tickInactivityRevert(uint64_t now_ms);
  void onVbusTransition(bool present, uint64_t now_ms);
  bool isScanSuppressed(uint64_t now_ms) const;
  static ButtonHoldGesture classifyButtonHold(uint64_t hold_ms);
  void wakeBacklight(uint64_t now_ms);
};

}  // namespace stockbot
