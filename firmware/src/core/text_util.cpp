#include "text_util.h"

#include <cctype>
#include <cstring>

namespace stockbot {

namespace {

constexpr const char* kAsciiSpace = " \t\n\v\f\r";

}  // namespace

std::string trimAscii(std::string_view raw) {
  while (!raw.empty() && strchr(kAsciiSpace, raw.front()) != nullptr) {
    raw.remove_prefix(1);
  }
  while (!raw.empty() && strchr(kAsciiSpace, raw.back()) != nullptr) {
    raw.remove_suffix(1);
  }
  return std::string(raw);
}

bool normalizeScan(std::string_view raw, std::string& out) {
  out.clear();
  const std::string trimmed = trimAscii(raw);
  out.reserve(trimmed.size());
  for (unsigned char ch : trimmed) {
    if (ch > 127) {
      out.clear();
      return false;
    }
    if (ch >= 'a' && ch <= 'z') {
      ch = static_cast<unsigned char>(ch - 'a' + 'A');
    }
    switch (ch) {
      case 'O':
        ch = '0';
        break;
      case 'I':
      case 'L':
        ch = '1';
        break;
      default:
        break;
    }
    out.push_back(static_cast<char>(ch));
  }
  return true;
}

bool isAsciiDigitProductBarcode(std::string_view raw) {
  const std::string value = trimAscii(raw);
  if (value.empty() || value.size() > 14) {
    return false;
  }
  for (char ch : value) {
    if (ch < '0' || ch > '9') {
      return false;
    }
  }
  return true;
}

}  // namespace stockbot
