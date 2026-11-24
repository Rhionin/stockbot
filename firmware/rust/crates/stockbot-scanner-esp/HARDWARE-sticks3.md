# M5StickS3 hardware facts

Read from M5Stack's public M5Unified and M5GFX sources on 2026-11-24, for the driver tickets #741 to #745. "Verbatim" means a value or code excerpt read from the named file. "Summarised" means read from a summary of the source, so check it on the bench before relying on it. Source names are paths in the master branch of <https://github.com/m5stack/M5Unified> and <https://github.com/m5stack/M5GFX>.

Nothing here has been checked on a real board yet.

## Internal I2C bus (used by #741)

| Fact | Value | Source | Read as |
|---|---|---|---|
| Internal SDA / SCL | GPIO47 / GPIO48 | M5Unified `src/M5Unified.inl`, `_pin_table_i2c_ex_in`: `{ board_M5StickS3, GPIO_NUM_48, GPIO_NUM_47, GPIO_NUM_10, GPIO_NUM_9 }`, column order "In SCL,SDA, EX SCL,SDA" | verbatim |
| Internal port, PMIC speed | I2C1, 100 kHz | M5GFX `generated/esp32s3_board_data.hpp`: `internal_i2c_port = 1`, pmic `i2c_freq = 100000` | verbatim |
| External Grove SCL / SDA | GPIO10 / GPIO9, I2C port 0 | same table row as above | verbatim. The old C++ map said GPIO2/GPIO1; that is wrong for the StickS3 (those are the AtomS3 Grove pins). Grove 5 V comes from the M5PM1 boost |

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

## U173 barcode reader (used by #743)

Source: M5Stack's official library <https://github.com/m5stack/M5Unit-QRCode> and its I2C protocol sheet V2 (2023-09-07), read from the library and a summary of the sheet. **Corrects the register table in ticket #743**, which came from deleted, never-run C++. Nothing here has been checked on a real reader.

| Fact | Value |
|---|---|
| I2C address / speed | `0x21`, 100 kHz, on the Grove bus (I2C port 0, SDA GPIO9, SCL GPIO10) |
| Register address | 16 bits, sent **low byte first**; a read is a write of the 2 address bytes, then a read |
| `0x0000` | W/R trigger, only effective in manual trigger mode: 1 starts continuous decoding, 0 stops |
| `0x0010` | R/W status: 0 not ready, 1 data good, 2 need to read again. Resets after the data is read, or when 0 is written |
| `0x0020` | R length, 2 bytes little endian |
| `0x0030` | W/R **trigger mode**: 0 auto (default), 1 manual. It is not a symbology register |
| `0x0040` | R the unit's own button: 0 pressed, 1 not pressed |
| `0x00FE` | R firmware version, 1 byte (the sheet's row label says `0x00F0`; the library uses `0x00FE`) |
| `0x00FF` | R/W I2C address |
| `0x1000` to `0x11FF` | R barcode data, at most 512 bytes |
| Start-up | the library waits 10 ms after bus start before talking to the unit |
| Symbologies | **No register** for 1D-only or QR-disable. `enable_1d_only` is a logged no-op; the core barcode classifier drops QR codes in software |

The firmware puts the unit in manual trigger mode and writes `0x0000` from `set_continuous_mode`, so it decodes only while docked or while the trigger button is held.

**UART variant (`sticks3_u173_uart`), not built.** From the library: 115200 8N1; handshake `23 61 41` answered by `24 61 41`; manual mode `21 61 41 00`; auto mode `21 61 41 05`; trigger on `32 75 01`, trigger off `32 75 02`; any other frame is barcode text. Which Grove pin is RX and which is TX is not known, so that target keeps the serial stand-in.

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

What `src/display_sticks3.rs` (#742) uses: `mipidsi` 0.9.0 with the ST7789 model, display size 135x240, offset 52/40, inverted colours, SPI3 at 40 MHz write only (mode 0, no MISO), and `Rotation::Deg90` for the 240x135 landscape screens drawn by `stockbot_scanner_core::screen`. The backlight is LEDC timer 0 and channel 0 at 1 kHz with 8-bit duty (255 for 100 %, 51 for 20 %). The M5GFX backlight offset of 16 and frequency of 256 are not copied.

**Unverified on the bench:** the rotation direction (`Deg90` against `Deg270`), the colour order (RGB assumed), SPI mode 0, whether the offsets 52/40 land on the visible area after rotation, and the backlight polarity. The README's first-bench-flash step 9 says what to look at and which constant to change. The panel starts on its first draw, after the M5PM1 driver has switched the LCD power on, because the controller calls `display.begin()` before `power.begin()`.

## Buttons (used by #744, part 1)

| Fact | Value | Read as |
|---|---|---|
| BtnA / BtnB | GPIO11 / GPIO12, active low | pins verbatim from M5Unified `src/M5Unified.inl`; the button `update` code is **summarised** |

`src/buttons_sticks3.rs` reads both as plain inputs with the internal pull-up on (harmless beside an external pull-up, and it helps if the board has none), pressed when the pin reads low. `stockbot_scanner_core::buttons` debounces them (20 ms) and turns the edges into controller calls: BtnA is the trigger, BtnB is the mode button. Unverified on the bench: whether the pins read low when pressed.

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
