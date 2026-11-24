#pragma once

#include <cstddef>
#include <string>
#include <string_view>

namespace stockbot {

std::string trimAscii(std::string_view raw);

// Uppercase ASCII a-z; map O→0, I/L→1. Returns empty and ok=false on non-ASCII.
bool normalizeScan(std::string_view raw, std::string& out);

bool isAsciiDigitProductBarcode(std::string_view raw);

}  // namespace stockbot
