#include "ring_buffer.h"

#include <algorithm>
#include <cstdio>
#include <cstring>

namespace stockbot {

namespace {

uint32_t crc32Update(uint32_t crc, const uint8_t* data, size_t len) {
  crc = ~crc;
  for (size_t i = 0; i < len; ++i) {
    crc ^= data[i];
    for (int bit = 0; bit < 8; ++bit) {
      const uint32_t mask = -(crc & 1u);
      crc = (crc >> 1) ^ (0xEDB88320u & mask);
    }
  }
  return ~crc;
}

}  // namespace

RingBuffer::RingBuffer(RingStorage& storage) : storage_(storage) {}

uint32_t RingBuffer::crc32(const RingScanRecord& rec) {
  RingScanRecord copy = rec;
  copy.crc32 = 0;
  return crc32Update(0, reinterpret_cast<const uint8_t*>(&copy), sizeof(copy));
}

RingScanRecord RingBuffer::makeRecord(const std::string& id, const std::string& barcode,
                                      Direction direction) {
  RingScanRecord rec{};
  std::strncpy(rec.client_scan_id, id.c_str(), sizeof(rec.client_scan_id) - 1);
  std::strncpy(rec.barcode, barcode.c_str(), sizeof(rec.barcode) - 1);
  rec.direction = direction == Direction::StockIn ? 1 : 0;
  rec.crc32 = crc32(rec);
  return rec;
}

bool RingBuffer::persist() {
  return storage_.writeAll(records_);
}

bool RingBuffer::recoverFromStorage() {
  records_.clear();
  std::vector<RingScanRecord> loaded;
  if (!storage_.readAll(loaded)) {
    return false;
  }
  for (const auto& rec : loaded) {
    if (crc32(rec) != rec.crc32) {
      continue;
    }
    records_.push_back(rec);
  }
  return true;
}

bool RingBuffer::enqueue(const std::string& barcode, Direction direction, uint64_t now_ms,
                         std::string& out_client_scan_id) {
  (void)now_ms;
  if (isFull()) {
    return false;
  }
  char buf[48];
  std::snprintf(buf, sizeof(buf), "scan-%08x-%08x", id_counter_++, static_cast<unsigned>(now_ms & 0xffffffffu));
  out_client_scan_id = buf;
  records_.push_back(makeRecord(out_client_scan_id, barcode, direction));
  return persist();
}

std::vector<RingScanRecord> RingBuffer::peekBatch(size_t max_count) const {
  std::vector<RingScanRecord> batch;
  const size_t n = std::min(max_count, records_.size());
  batch.assign(records_.begin(), records_.begin() + static_cast<std::ptrdiff_t>(n));
  return batch;
}

bool RingBuffer::acknowledge(const std::string& client_scan_id) {
  for (auto it = records_.begin(); it != records_.end(); ++it) {
    if (client_scan_id == it->client_scan_id) {
      records_.erase(it);
      return persist();
    }
  }
  return false;
}

void RingBuffer::clearAll() {
  records_.clear();
  persist();
}

}  // namespace stockbot
