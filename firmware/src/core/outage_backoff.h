#pragma once

#include <algorithm>
#include <cstdint>

namespace stockbot {

// #718 slice S-b will replace this with the shared retry_policy module.
constexpr uint64_t kOutageBackoffBaseMs = 1000;
constexpr uint64_t kOutageBackoffFactor = 2;
constexpr uint64_t kOutageBackoffCapMs = 30000;

struct OutageBackoffState {
  uint32_t streak = 0;
  uint64_t not_before_ms = 0;
};

inline uint64_t outageBackoffEnvelopeMs(uint32_t streak) {
  uint64_t d = kOutageBackoffBaseMs;
  for (uint32_t i = 0; i < streak; ++i) {
    d = std::min(d * kOutageBackoffFactor, kOutageBackoffCapMs);
    if (d >= kOutageBackoffCapMs) {
      return kOutageBackoffCapMs;
    }
  }
  return std::min(d, kOutageBackoffCapMs);
}

inline bool outageMaySend(const OutageBackoffState& state, uint64_t now_ms) {
  return now_ms >= state.not_before_ms;
}

inline void outageBackoffReset(OutageBackoffState& state) {
  state.streak = 0;
  state.not_before_ms = 0;
}

inline void outageBackoffOnNoCode401(OutageBackoffState& state, uint64_t now_ms) {
  const uint64_t wait = outageBackoffEnvelopeMs(state.streak);
  state.not_before_ms = now_ms + wait;
  state.streak = std::min(state.streak + 1, UINT32_MAX);
}

}  // namespace stockbot