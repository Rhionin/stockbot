#ifdef ARDUINO

#include <Arduino.h>

#include "barcode_filter.h"
#include "display_model.h"
#include "scanner_state.h"

void setup() {
  Serial.begin(115200);
  stockbot::ScannerState state;
  state.operational = stockbot::OperationalState::Unprovisioned;
  stockbot::DisplayModelInput view;
  view.screen = stockbot::UiScreen::S0_Unprovisioned;
  view.pairing_ap_suffix = "ABCD";
  const auto bands = stockbot::buildDisplayModel(view);
  Serial.println(bands.band1.c_str());
}

void loop() {
  delay(1000);
}

#else

int main() {
  return 0;
}

#endif
