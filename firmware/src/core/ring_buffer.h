#pragma once

#include <cstddef>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#include "scanner_state.h"

namespace stockbot {

struct RingScanRecord {
  char client_scan_id[36];
  char barcode[16];
  uint8_t direction;
  uint8_t reserved[3];
  uint32_t scanned_ms;
  uint32_t crc32;
};

static_assert(sizeof(RingScanRecord) == 64, "RingScanRecord must be 64 bytes");

uint32_t ringScanAgeMs(const RingScanRecord& record, uint64_t now_ms);

class RingStorage {
 public:
  virtual ~RingStorage() = default;
  virtual bool readAll(std::vector<RingScanRecord>& out) = 0;
  virtual bool writeAll(const std::vector<RingScanRecord>& records) = 0;
};

class RingBuffer {
 public:
  static constexpr size_t kCapacity = 500;

  explicit RingBuffer(RingStorage& storage);

  bool recoverFromStorage();
  void seedIdCounterFromRecords(const std::string& boot_id);
  bool enqueue(const std::string& barcode, Direction direction, uint64_t now_ms,
               const std::string& boot_id, std::string& out_client_scan_id);
  std::vector<RingScanRecord> peekBatch(size_t max_count = 20) const;
  bool acknowledge(const std::string& client_scan_id);
  void clearAll();

  bool isFull() const { return records_.size() >= kCapacity; }
  size_t count() const { return records_.size(); }

 private:
  RingStorage& storage_;
  std::vector<RingScanRecord> records_;
  uint32_t id_counter_ = 0;

  static uint32_t crc32(const RingScanRecord& rec);
  static RingScanRecord makeRecord(const std::string& id, const std::string& barcode, Direction direction,
                                   uint32_t scanned_ms);
  bool persist();
};

}  // namespace stockbot
