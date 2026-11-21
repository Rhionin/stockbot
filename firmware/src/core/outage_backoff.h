#pragma once

#include <algorithm>
#include <cstdint>

namespace stockbot {

// #718 slice S-b will replace this with the shared retry_policy module.
constexpr uint64_t kOutageBackoffBaseMs = 1000;
constexpr uint64_t kOutageBackoffFactor = 2;
constexpr uint64_t kOutageBackoffCapMs = 30000;

enum class OutageBackoffTrigger { Flush, Heartbeat, InlineScan };

struct OutageBackoffState {
  uint32_t streak = 0;
  uint64_t flush_not_before_ms = 0;
  uint64_t heartbeat_not_before_ms = 0;
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

inline bool outageMaySendFlush(const OutageBackoffState& state, uint64_t now_ms) {
  return now_ms >= state.flush_not_before_ms;
}

inline bool outageMaySendHeartbeat(const OutageBackoffState& state, uint64_t now_ms) {
  return now_ms >= state.heartbeat_not_before_ms;
}

inline void outageBackoffReset(OutageBackoffState& state) {
  state.streak = 0;
  state.flush_not_before_ms = 0;
  state.heartbeat_not_before_ms = 0;
}

inline void outageBackoffIncrementStreak(OutageBackoffState& state) {
  if (state.streak < UINT32_MAX) {
    ++state.streak;
  }
}

inline void outageBackoffOnNoCode401(OutageBackoffState& state, uint64_t now_ms,
                                     OutageBackoffTrigger trigger) {
  const uint64_t wait = outageBackoffEnvelopeMs(state.streak);
  switch (trigger) {
    case OutageBackoffTrigger::Flush:
    case OutageBackoffTrigger::InlineScan:
      state.flush_not_before_ms = now_ms + wait;
      break;
    case OutageBackoffTrigger::Heartbeat:
      state.heartbeat_not_before_ms = now_ms + wait;
      break;
  }
  outageBackoffIncrementStreak(state);
}

}  // namespace stockbot