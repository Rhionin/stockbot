#pragma once

#include <cstdint>
#include <optional>

namespace stockbot {

enum class ModeButtonHoldPhase {
  None,
  WifiSetupHold,
  EraseWarning,
};

enum class ModeButtonReleaseAction {
  None,
  ShortPress,
  WifiSetup,
  FactoryReset,
};

// Tracks mode-button press/hold for Wi-Fi setup (5–10 s) and factory reset (release after ≥10 s).
class ButtonHoldTracker {
 public:
  void onPress(uint64_t now_ms);
  ModeButtonReleaseAction onRelease(uint64_t now_ms);
  std::optional<ModeButtonHoldPhase> onHoldProgress(uint64_t now_ms);

  bool isDown() const { return down_; }
  uint64_t heldMs(uint64_t now_ms) const;

 private:
  bool down_ = false;
  uint64_t down_ms_ = 0;
  ModeButtonHoldPhase phase_ = ModeButtonHoldPhase::None;
};

}  // namespace stockbot
