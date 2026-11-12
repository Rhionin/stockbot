#pragma once

// TEST-ONLY: SBR1 blob encoder matching firmware/src/storage/littlefs_ring_storage.cpp
// writeAll() layout (no LittleFS). Do not include from firmware/src/.

#include <cstddef>
#include <cstdint>
#include <vector>

#include "ring_buffer.h"

namespace stockbot {
namespace testonly {

constexpr uint32_t kSbr1Magic = 0x53425231;  // 'SBR1'

struct RingFileHeader {
  uint32_t magic;
  uint32_t count;
};

static_assert(sizeof(stockbot::RingScanRecord) == 64, "RingScanRecord layout");
static_assert(sizeof(RingFileHeader) == 8, "RingFileHeader layout");
static_assert(offsetof(RingFileHeader, magic) == 0, "RingFileHeader.magic offset");
static_assert(offsetof(RingFileHeader, count) == 4, "RingFileHeader.count offset");

inline std::vector<uint8_t> encodeSbr1File(const std::vector<RingScanRecord>& records) {
  std::vector<uint8_t> out;
  out.resize(sizeof(RingFileHeader) + records.size() * sizeof(RingScanRecord));
  RingFileHeader header{kSbr1Magic, static_cast<uint32_t>(records.size())};
  std::memcpy(out.data(), &header, sizeof(header));
  if (!records.empty()) {
    std::memcpy(out.data() + sizeof(header), records.data(),
                records.size() * sizeof(RingScanRecord));
  }
  return out;
}

}  // namespace testonly
}  // namespace stockbot
