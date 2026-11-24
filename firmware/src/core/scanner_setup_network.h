#pragma once

#include <cctype>
#include <string>

namespace stockbot {

inline constexpr const char kScannerSetupApPrefix[] = "Scanner-Setup-";

inline std::string scannerSetupSsid(const std::string& ap_suffix) {
  return std::string(kScannerSetupApPrefix) + ap_suffix;
}

inline std::string apSuffixFromMacAddress(const std::string& mac) {
  std::string hex;
  hex.reserve(12);
  for (char ch : mac) {
    if (std::isxdigit(static_cast<unsigned char>(ch))) {
      hex += static_cast<char>(std::toupper(static_cast<unsigned char>(ch)));
    }
  }
  if (hex.size() < 4) {
    return "0000";
  }
  return hex.substr(hex.size() - 4);
}

}  // namespace stockbot
