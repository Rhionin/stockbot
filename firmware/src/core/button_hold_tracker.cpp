#include "button_hold_tracker.h"

namespace stockbot {

void ButtonHoldTracker::onPress(uint64_t now_ms) {
  down_ = true;
  down_ms_ = now_ms;
  phase_ = ModeButtonHoldPhase::None;
}

uint64_t ButtonHoldTracker::heldMs(uint64_t now_ms) const {
  if (!down_) {
    return 0;
  }
  return now_ms - down_ms_;
}

std::optional<ModeButtonHoldPhase> ButtonHoldTracker::onHoldProgress(uint64_t now_ms) {
  if (!down_) {
    return std::nullopt;
  }
  const uint64_t held = now_ms - down_ms_;
  if (held < 5000) {
    return std::nullopt;
  }
  if (held >= 10000) {
    if (phase_ != ModeButtonHoldPhase::EraseWarning) {
      phase_ = ModeButtonHoldPhase::EraseWarning;
      return ModeButtonHoldPhase::EraseWarning;
    }
    return std::nullopt;
  }
  if (phase_ != ModeButtonHoldPhase::WifiSetupHold) {
    phase_ = ModeButtonHoldPhase::WifiSetupHold;
    return ModeButtonHoldPhase::WifiSetupHold;
  }
  return std::nullopt;
}

ModeButtonReleaseAction ButtonHoldTracker::onRelease(uint64_t now_ms) {
  if (!down_) {
    return ModeButtonReleaseAction::None;
  }
  const uint64_t held = now_ms - down_ms_;
  down_ = false;
  phase_ = ModeButtonHoldPhase::None;
  if (held < 5000) {
    return ModeButtonReleaseAction::ShortPress;
  }
  if (held >= 10000) {
    return ModeButtonReleaseAction::FactoryReset;
  }
  return ModeButtonReleaseAction::WifiSetup;
}

}  // namespace stockbot
