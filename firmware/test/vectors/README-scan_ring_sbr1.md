# `scan_ring_sbr1.json`

Golden SBR1 (`scan_ring.bin`) blobs produced by the C++ ring codec in `firmware/src/core/ring_buffer.cpp` and the TEST-ONLY encoder in `firmware/test/sbr1_file_codec.h` (same layout as `littlefs_ring_storage.cpp`).

## Regenerate

From `firmware/`:

```bash
g++ -std=c++17 -Wall -Wextra -Werror -Isrc/core \
  src/core/ring_buffer.cpp test/gen_scan_ring_sbr1_vectors.cpp -o /tmp/gen_scan_ring_sbr1
/tmp/gen_scan_ring_sbr1
```

Commit the updated `test/vectors/scan_ring_sbr1.json`. Rust integration tests decode each `bytesHex` case and re-encode byte-identically; they also assert the case count (10).
