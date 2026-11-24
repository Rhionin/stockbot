# M5StickS3 hardware facts

Read from M5Stack's public M5Unified and M5GFX sources on 2026-11-24, for the driver tickets #741 to #745. "Verbatim" means a value or code excerpt read from the named file. "Summarised" means read from a summary of the source, so check it on the bench before relying on it. Source names are paths in the master branch of <https://github.com/m5stack/M5Unified> and <https://github.com/m5stack/M5GFX>.

Nothing here has been checked on a real board yet.

## Internal I2C bus (used by #741)

| Fact | Value | Source | Read as |
|---|---|---|---|
| Internal SDA / SCL | GPIO47 / GPIO48 | M5Unified `src/M5Unified.inl`, `_pin_table_i2c_ex_in`: `{ board_M5StickS3, GPIO_NUM_48, GPIO_NUM_47, GPIO_NUM_10, GPIO_NUM_9 }`, column order "In SCL,SDA, EX SCL,SDA" | verbatim |
| Internal port, PMIC speed | I2C1, 100 kHz | M5GFX `generated/esp32s3_board_data.hpp`: `internal_i2c_port = 1`, pmic `i2c_freq = 100000` | verbatim |
| External Grove SCL / SDA | GPIO10 / GPIO9 | same table row as above | verbatim; ticket #743 must check it against the old C++ map (GPIO2/GPIO1), which may have been wrong |

## M5PM1 power chip (used by #741)

Sources: M5Unified `src/utility/power/M5PM1_Class.inl` and `.hpp`, M5GFX `pmic_ops.hpp`. Registers have 8-bit addresses; 16-bit values are little endian (low byte first). All rows are verbatim unless noted.

| Fact | Value |
|---|---|
| I2C address | `0x6E` |
| `DEVICE_ID` | `0x00`; M5GFX expects `0x2050` |
| `PWR_SRC` | `0x04`; bit0 5V VIN valid, bit1 5VINOUT valid (0 while the boost is on), bit2 VBAT node valid |
| `PWR_CFG` | `0x06`; bit0 CHG_EN, bit1 DCDC_EN, bit2 LDO_EN, bit3 BOOST_EN (the 5 V Grove output), bit4 LED_EN |
| `I2C_CFG` | `0x09`; write `0x00` to disable I2C idle sleep |
| `WDT_CNT` | `0x0A`; write `0x00` to disable the watchdog |
| `SYS_CMD` | `0x0C`; `0xA1` shuts down |
| `GPIO_MODE` | `0x10`; 1 = output |
| `GPIO_OUT` | `0x11`; output level per bit |
| `GPIO_IN` | `0x12`; input level per bit |
| `GPIO_DRV` | `0x13`; 0 = push-pull |
| `GPIO_FUNC0` | `0x16`; a cleared bit is the plain GPIO function, for GPIO0..3 as M5Unified uses it |
| `VBAT_L` | `0x22`; u16 LE millivolts; 0 means the read failed or there is no battery |
| `VIN_L` | `0x24`; u16 LE millivolts (USB 5 V input) |
| `5VOUT_L` | `0x26`; u16 LE millivolts |
| PM1 GPIO0 | Input carrying the charge state. M5Unified `Power_Class::begin` does `setGPIOFunction(gpio0, gpio); setGPIOMode(gpio0, input)`, and its charge-state table says `board_M5StickS3: // PM1 G0`. **The polarity is UNVERIFIED**; the driver assumes active low (`CHARGE_PIN_ACTIVE_LOW`) |
| PM1 GPIO2 | LCD power. M5GFX `pmic_ops::sticks3_power_on` writes `I2C_CFG` = 0, then for bit `0x04`: FUNC0 off, MODE on, DRV off, OUT on (drives it high) |
| PM1 GPIO3 | Speaker amplifier enable (M5Unified `_begin`, "PA Control"): for bit `0x08`, FUNC0 off, MODE on, DRV off, OUT off. It boots low; the speaker ticket #744 drives it high |
| Charge status register | None. M5Unified's `M5PM1_Class::isCharging` is a permanent stub that returns false |
| Battery percent | `level = (mv - 3300) * 100 / (4150 - 3350)`, clamped to 0..100; `mv <= 0` means unknown. **Summarised**, not verbatim. The 3300 versus 3350 mismatch is copied as read, not "fixed" |

## Display (for the display ticket)

All summarised from M5GFX board data unless the source file says otherwise; verify on the bench.

| Fact | Value |
|---|---|
| Panel | ST7789V2, 135x240, offset_x 52, offset_y 40, invert on |
| SPI | SPI3_HOST, 40 MHz write, 3-wire |
| Pins | SCLK GPIO40, MOSI GPIO39, CS GPIO41, DC GPIO45, RST GPIO21 |
| Backlight | PWM on GPIO38 (freq 256, channel 7, offset 16) |
| Panel power | PM1 GPIO2 high (see above) |

Source: M5GFX `src/M5GFX.cpp` and `generated/esp32s3_board_data.hpp`.

## Buttons (for the input ticket)

| Fact | Value | Read as |
|---|---|---|
| BtnA / BtnB | GPIO11 / GPIO12, active low | pins verbatim from M5Unified `src/M5Unified.inl`; the button `update` code is **summarised** |

## Speaker and microphone (for #744)

| Fact | Value | Read as |
|---|---|---|
| Codec | ES8311 at I2C `0x18` on the internal bus | summarised |
| Speaker I2S | I2S0: MCK GPIO18, BCK GPIO17, WS GPIO15, DOUT GPIO14 | summarised (I2S pins) |
| Amplifier enable | PM1 GPIO3 high | see the PM1 table |
| Codec enable sequence | `00=80, 01=B5, 02=18, 0D=01, 12=00, 13=10, 32=BF, 37=08` (register=value, hex) | summarised |
| Microphone | I2S1, DIN GPIO16 | summarised (I2S pins) |

Source: M5Unified `src/M5Unified.inl` (speaker and mic setup).

## IMU

Not found in the source that was read. Verify on the bench.
