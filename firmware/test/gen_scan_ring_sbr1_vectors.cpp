// TEST-ONLY: writes firmware/test/vectors/scan_ring_sbr1.json from the real C++ ring codec.
//
// Build (from firmware/, same sources as run_host_tests.sh); see README-scan_ring_sbr1.md.
//
// Re-run after changing ring record layout or SBR1 header rules.

#include <cstdio>
#include <cstring>
#include <fstream>
#include <iostream>
#include <string>
#include <vector>

#include "ring_buffer.h"
#include "sbr1_file_codec.h"

namespace {

std::string hexEncode(const std::vector<uint8_t>& bytes) {
  static const char* kHex = "0123456789abcdef";
  std::string out;
  out.reserve(bytes.size() * 2);
  for (uint8_t b : bytes) {
    out.push_back(kHex[b >> 4]);
    out.push_back(kHex[b & 0x0f]);
  }
  return out;
}

void pushCase(std::vector<std::string>& lines, const char* name, const std::vector<uint8_t>& bytes,
              const char* decode_error, int recover_count) {
  lines.push_back("  {");
  lines.push_back(std::string("    \"name\": \"") + name + "\",");
  lines.push_back(std::string("    \"bytesHex\": \"") + hexEncode(bytes) + "\",");
  if (decode_error) {
    lines.push_back(std::string("    \"decodeError\": \"") + decode_error + "\",");
    lines.push_back("    \"recoverCount\": null");
  } else {
    lines.push_back("    \"decodeError\": null,");
    lines.push_back(std::string("    \"recoverCount\": ") + std::to_string(recover_count));
  }
  lines.push_back("  }");
}

std::vector<stockbot::RingScanRecord> makeRecordsFromRing(stockbot::RingBuffer& ring) {
  std::vector<stockbot::RingScanRecord> out;
  const auto batch = ring.peekBatch(stockbot::RingBuffer::kCapacity);
  out.insert(out.end(), batch.begin(), batch.end());
  return out;
}

}  // namespace

int main() {
  std::vector<std::string> json_lines;
  json_lines.push_back("[");
  bool first = true;

  auto emit = [&](const char* name, const std::vector<uint8_t>& bytes, const char* decode_error,
                  int recover_count) {
    if (!first) {
      json_lines.push_back(",");
    }
    first = false;
    pushCase(json_lines, name, bytes, decode_error, recover_count);
  };

  // Empty ring file.
  emit("empty", stockbot::testonly::encodeSbr1File({}), nullptr, 0);

  // Single entry via RingBuffer.
  {
    struct MemStore : stockbot::RingStorage {
      std::vector<stockbot::RingScanRecord> records;
      bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
        out = records;
        return true;
      }
      bool writeAll(const std::vector<stockbot::RingScanRecord>& r) override {
        records = r;
        return true;
      }
    } store;
    stockbot::RingBuffer ring(store);
    ring.recoverFromStorage();
    std::string id;
    ring.enqueue("4011", stockbot::Direction::StockIn, 42, 3, id);
    emit("one-entry", stockbot::testonly::encodeSbr1File(makeRecordsFromRing(ring)), nullptr, 1);
  }

  // Several entries.
  {
    struct MemStore : stockbot::RingStorage {
      std::vector<stockbot::RingScanRecord> records;
      bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
        out = records;
        return true;
      }
      bool writeAll(const std::vector<stockbot::RingScanRecord>& r) override {
        records = r;
        return true;
      }
    } store;
    stockbot::RingBuffer ring(store);
    ring.recoverFromStorage();
    std::string id;
    ring.enqueue("123", stockbot::Direction::StockOut, 1, 1, id);
    ring.enqueue("456", stockbot::Direction::StockIn, 2, 1, id);
    ring.enqueue("7890123456789", stockbot::Direction::StockOut, 3, 1, id);
    emit("three-entries", stockbot::testonly::encodeSbr1File(makeRecordsFromRing(ring)), nullptr, 3);
  }

  // Full capacity (500).
  {
    struct MemStore : stockbot::RingStorage {
      std::vector<stockbot::RingScanRecord> records;
      bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
        out = records;
        return true;
      }
      bool writeAll(const std::vector<stockbot::RingScanRecord>& r) override {
        records = r;
        return true;
      }
    } store;
    stockbot::RingBuffer ring(store);
    ring.recoverFromStorage();
    std::string id;
    for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
      ring.enqueue("4011", stockbot::Direction::StockOut, static_cast<uint64_t>(i), 1, id);
    }
    emit("full-500", stockbot::testonly::encodeSbr1File(makeRecordsFromRing(ring)), nullptr,
         static_cast<int>(stockbot::RingBuffer::kCapacity));
  }

  // Wrap-around / churn: fill, ack first 20, add 20 more (still 500).
  {
    struct MemStore : stockbot::RingStorage {
      std::vector<stockbot::RingScanRecord> records;
      bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
        out = records;
        return true;
      }
      bool writeAll(const std::vector<stockbot::RingScanRecord>& r) override {
        records = r;
        return true;
      }
    } store;
    stockbot::RingBuffer ring(store);
    ring.recoverFromStorage();
    std::string id;
    for (size_t i = 0; i < stockbot::RingBuffer::kCapacity; ++i) {
      ring.enqueue("4011", stockbot::Direction::StockOut, static_cast<uint64_t>(i), 1, id);
    }
    const auto front = ring.peekBatch(20);
    for (const auto& rec : front) {
      ring.acknowledge(rec.client_scan_id);
    }
    for (size_t i = 0; i < 20; ++i) {
      ring.enqueue("9999", stockbot::Direction::StockIn, 9000 + static_cast<uint64_t>(i), 1, id);
    }
    emit("post-ack-refill-500", stockbot::testonly::encodeSbr1File(makeRecordsFromRing(ring)), nullptr,
         static_cast<int>(stockbot::RingBuffer::kCapacity));
  }

  // Max field lengths (barcode 15 chars, long client id via boot/seq/ms).
  {
    struct MemStore : stockbot::RingStorage {
      std::vector<stockbot::RingScanRecord> records;
      bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
        out = records;
        return true;
      }
      bool writeAll(const std::vector<stockbot::RingScanRecord>& r) override {
        records = r;
        return true;
      }
    } store;
    stockbot::RingBuffer ring(store);
    ring.recoverFromStorage();
    std::string id;
    ring.enqueue("123456789012345", stockbot::Direction::StockIn, 4294967295ULL, 4294967295U, id);
    emit("max-length-fields", stockbot::testonly::encodeSbr1File(makeRecordsFromRing(ring)), nullptr, 1);
  }

  // Corrupt CRC in otherwise valid file.
  {
    struct MemStore : stockbot::RingStorage {
      std::vector<stockbot::RingScanRecord> records;
      bool readAll(std::vector<stockbot::RingScanRecord>& out) override {
        out = records;
        return true;
      }
      bool writeAll(const std::vector<stockbot::RingScanRecord>& r) override {
        records = r;
        return true;
      }
    } store;
    stockbot::RingBuffer ring(store);
    ring.recoverFromStorage();
    std::string id;
    ring.enqueue("4011", stockbot::Direction::StockOut, 500, 7, id);
    auto records = makeRecordsFromRing(ring);
    records[0].crc32 ^= 0x00ffffffu;
    auto bytes = stockbot::testonly::encodeSbr1File(records);
    emit("corrupt-crc", bytes, nullptr, 0);
  }

  // Bad magic.
  {
    auto bytes = stockbot::testonly::encodeSbr1File({});
    bytes[0] ^= 0xff;
    emit("bad-magic", bytes, "bad_magic", -1);
  }

  // Count over capacity.
  {
    auto bytes = stockbot::testonly::encodeSbr1File({});
    uint32_t bad_count = stockbot::RingBuffer::kCapacity + 1;
    std::memcpy(bytes.data() + 4, &bad_count, sizeof(bad_count));
    emit("count-over-capacity", bytes, "count_over_capacity", -1);
  }

  // Truncated payload (header says 1 record, file ends after header).
  {
    auto bytes = stockbot::testonly::encodeSbr1File({});
    uint32_t one = 1;
    bytes.resize(8);
    std::memcpy(bytes.data() + 4, &one, sizeof(one));
    emit("truncated-records", bytes, "truncated", -1);
  }

  json_lines.push_back("]");

  const std::string out_path = "test/vectors/scan_ring_sbr1.json";
  std::ofstream out(out_path);
  if (!out) {
    std::cerr << "Failed to open " << out_path << "\n";
    return 1;
  }
  for (size_t i = 0; i < json_lines.size(); ++i) {
    out << json_lines[i];
    if (i + 1 < json_lines.size()) {
      out << "\n";
    }
  }
  out << "\n";

  // Sanity: case count is locked by Rust tests.
  const size_t case_count = 10;
  size_t count_emitted = 0;
  for (const auto& line : json_lines) {
    if (line.find("\"name\"") != std::string::npos) {
      ++count_emitted;
    }
  }
  if (count_emitted != case_count) {
    std::cerr << "Expected " << case_count << " cases, got " << count_emitted << "\n";
    return 1;
  }

  std::cout << "Wrote " << out_path << " (" << case_count << " cases)\n";
  return 0;
}
