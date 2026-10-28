#pragma once

#if defined(STOCKBOT_ENV_STICKS3_U173)
#define TARGET_STICKS3 1
#define PIN_BTN_TRIGGER 11
#define PIN_BTN_MODE 12
#define PIN_GROVE_SDA 2
#define PIN_GROVE_SCL 1
#define PIN_GROVE_G1 1
#define PIN_GROVE_G2 2
#elif defined(STOCKBOT_ENV_ATOMS3_BENCH) || defined(STOCKBOT_ENV_ATOMS3_SYMcode_HID)
#define TARGET_ATOMS3LITE 1
#define PIN_BTN_TRIGGER 41
#define PIN_BTN_MODE 41
#define PIN_GROVE_SDA 2
#define PIN_GROVE_SCL 1
#define PIN_GROVE_G1 1
#define PIN_GROVE_G2 2
#define PIN_RGB_LED 35
#if defined(STOCKBOT_ENV_ATOMS3_BENCH)
#define USE_U120_I2C_DISPLAY 1
#endif
#if defined(STOCKBOT_ENV_ATOMS3_SYMcode_HID)
#define USE_U120_I2C_DISPLAY 1
#define USE_USB_HID_SCANNER 1
#endif
#endif

#if defined(U173_UART_SPLIT_PIN_MODE) && TARGET_STICKS3
#define USE_U173_UART 1
#else
#define USE_U173_I2C 1
#endif
