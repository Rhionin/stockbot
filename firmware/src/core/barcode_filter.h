#pragma once

#include <cstdint>
#include <optional>
#include <string>

#include "scanner_state.h"

namespace stockbot {

enum class BarcodeKind {
  Product,
  ControlStockOut,
  ControlStockIn,
  PairingCode,
  SetupPayload,
  RejectedNon1D,
};

constexpr uint32_t RESCAN_COOLDOWN_MS = 2000;

BarcodeKind classifyBarcode(const std::string& raw, OperationalState state);

bool looksLikePairingCode(const std::string& raw);
bool isPairingCode(const std::string& raw);
char pairingCheckCharacter(const std::string& payload8);

struct DuplicateCooldownState {
  std::string last_barcode;
  Direction last_direction = Direction::StockOut;
  uint64_t last_accept_ms = 0;
};

bool acceptWithDuplicateCooldown(DuplicateCooldownState& state, const std::string& barcode,
                                 Direction direction, uint64_t now_ms, bool manual_retrigger);

}  // namespace stockbot
