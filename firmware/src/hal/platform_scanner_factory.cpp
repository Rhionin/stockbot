#include "hal_scanner.h"

#ifdef ARDUINO

#include "board_pins.h"

namespace stockbot {

HalScanner* createPlatformScanner() {
#if USE_USB_HID_SCANNER
  return createUsbHidScanner();
#elif USE_U173_UART
  return createU173UartScanner();
#else
  return createU173I2cScanner();
#endif
}

}  // namespace stockbot

#endif
