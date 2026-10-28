#include "littlefs_ring_storage.h"

#ifdef ARDUINO

#include <LittleFS.h>

namespace stockbot {

namespace {

constexpr uint32_t kMagic = 0x53425231;  // 'SBR1'

struct RingFileHeader {
  uint32_t magic;
  uint32_t count;
};

}  // namespace

bool LittleFsRingStorage::begin() {
  if (!LittleFS.begin(true)) {
    return false;
  }
  return true;
}

bool LittleFsRingStorage::readAll(std::vector<RingScanRecord>& out) {
  out.clear();
  if (!LittleFS.exists(kPath)) {
    return true;
  }
  File f = LittleFS.open(kPath, "r");
  if (!f) {
    return false;
  }
  RingFileHeader header{};
  if (f.read(reinterpret_cast<uint8_t*>(&header), sizeof(header)) != sizeof(header)) {
    f.close();
    return false;
  }
  if (header.magic != kMagic) {
    f.close();
    return false;
  }
  out.resize(header.count);
  if (header.count > 0) {
    const size_t expected = static_cast<size_t>(header.count) * sizeof(RingScanRecord);
    const size_t read_bytes =
        f.read(reinterpret_cast<uint8_t*>(out.data()), expected);
    if (read_bytes != expected) {
      f.close();
      return false;
    }
  }
  f.close();
  return true;
}

bool LittleFsRingStorage::writeAll(const std::vector<RingScanRecord>& records) {
  File f = LittleFS.open(kPath, "w");
  if (!f) {
    return false;
  }
  RingFileHeader header{kMagic, static_cast<uint32_t>(records.size())};
  f.write(reinterpret_cast<const uint8_t*>(&header), sizeof(header));
  if (!records.empty()) {
    f.write(reinterpret_cast<const uint8_t*>(records.data()),
            records.size() * sizeof(RingScanRecord));
  }
  f.close();
  return true;
}

}  // namespace stockbot

#endif
