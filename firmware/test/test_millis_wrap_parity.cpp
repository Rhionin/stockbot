// TEST-ONLY: prints millis-wrap expectations (uint64_t subtraction, same as core C++).
// Built via firmware/run_host_tests.sh (millis wrap parity target).

#include <cstdint>
#include <cstdio>

#include "button_hold_tracker.h"
#include "scanner_state.h"

int main() {
  constexpr uint64_t kBeforeWrap = 4294967000ULL;
  constexpr uint64_t kAfterWrap = 100ULL;

  const uint64_t held = kAfterWrap - kBeforeWrap;
  std::printf("held_delta=%llu\n", static_cast<unsigned long long>(held));

  stockbot::ButtonHoldTracker tracker;
  tracker.onPress(kBeforeWrap);
  const auto phase = tracker.onHoldProgress(kAfterWrap);
  const auto release = tracker.onRelease(kAfterWrap);
  std::printf("hold_phase_erase=%d\n", phase.has_value() && *phase == stockbot::ModeButtonHoldPhase::EraseWarning ? 1 : 0);
  std::printf("release_factory=%d\n", release == stockbot::ModeButtonReleaseAction::FactoryReset ? 1 : 0);

  stockbot::ScannerState st;
  st.active_direction = stockbot::Direction::StockIn;
  st.default_direction = stockbot::Direction::StockOut;
  st.inactivity_revert_seconds = 300;
  st.last_activity_ms = kBeforeWrap;
  const auto revert = st.tickInactivityRevert(kAfterWrap);
  std::printf("inactivity_revert=%d\n", revert.has_value() && *revert == stockbot::ModeChime::ModeRevert ? 1 : 0);

  stockbot::ScannerState dock;
  dock.onVbusTransition(true, 1000);
  std::printf("suppress_1499=%d\n", dock.isScanSuppressed(2499) ? 1 : 0);
  std::printf("suppress_1500=%d\n", dock.isScanSuppressed(2500) ? 1 : 0);

  dock.vbus_transition_ms = kBeforeWrap;
  std::printf("suppress_after_wrap=%d\n", dock.isScanSuppressed(kAfterWrap) ? 1 : 0);
  return 0;
}
