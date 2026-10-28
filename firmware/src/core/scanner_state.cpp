#include "scanner_state.h"

namespace stockbot {

void ScannerState::wakeBacklight(uint64_t now_ms) {
  backlight = BacklightLevel::Full100;
  last_activity_ms = now_ms;
}

ModeButtonResult ScannerState::onModeButtonPress(uint64_t now_ms) {
  ModeButtonResult result;
  if (backlight == BacklightLevel::Dim20 || backlight == BacklightLevel::Off0) {
    wakeBacklight(now_ms);
    result.woke_backlight = true;
    return result;
  }
  if (active_direction == Direction::StockOut) {
    setDirection(Direction::StockIn, now_ms, true);
    result.chime = ModeChime::RisingStockIn;
  } else {
    setDirection(Direction::StockOut, now_ms, true);
    result.chime = ModeChime::FallingStockOut;
  }
  result.toggled_direction = true;
  return result;
}

void ScannerState::onScanOrActivity(uint64_t now_ms) {
  last_activity_ms = now_ms;
  backlight = BacklightLevel::Full100;
}

void ScannerState::setDirection(Direction dir, uint64_t now_ms, bool play_chime) {
  active_direction = dir;
  onScanOrActivity(now_ms);
  if (play_chime) {
    if (dir == Direction::StockIn) {
      (void)ModeChime::RisingStockIn;
    } else {
      (void)ModeChime::FallingStockOut;
    }
  }
}

std::optional<ModeChime> ScannerState::tickInactivityRevert(uint64_t now_ms) {
  if (active_direction == default_direction) {
    return std::nullopt;
  }
  if (last_activity_ms == 0) {
    last_activity_ms = now_ms;
    return std::nullopt;
  }
  const uint64_t threshold_ms = static_cast<uint64_t>(inactivity_revert_seconds) * 1000ULL;
  if (now_ms - last_activity_ms >= threshold_ms) {
    active_direction = default_direction;
    last_activity_ms = now_ms;
    return ModeChime::ModeRevert;
  }
  return std::nullopt;
}

void ScannerState::onVbusTransition(bool present, uint64_t now_ms) {
  vbus_present = present;
  vbus_transition_ms = now_ms;
}

bool ScannerState::isScanSuppressed(uint64_t now_ms) const {
  if (vbus_transition_ms == 0) {
    return false;
  }
  return now_ms - vbus_transition_ms < DOCK_SUPPRESS_MS;
}

ButtonHoldGesture ScannerState::classifyButtonHold(uint64_t hold_ms) {
  if (hold_ms >= 10000) {
    return ButtonHoldGesture::FactoryReset;
  }
  if (hold_ms >= 5000) {
    return ButtonHoldGesture::WifiUpdate;
  }
  return ButtonHoldGesture::ShortPress;
}

}  // namespace stockbot
