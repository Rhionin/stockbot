#pragma once

#include "ring_buffer.h"

namespace stockbot {

class LittleFsRingStorage : public RingStorage {
 public:
  bool begin();
  bool readAll(std::vector<RingScanRecord>& out) override;
  bool writeAll(const std::vector<RingScanRecord>& records) override;

 private:
  static constexpr const char* kPath = "/scans.ring";
};

}  // namespace stockbot
