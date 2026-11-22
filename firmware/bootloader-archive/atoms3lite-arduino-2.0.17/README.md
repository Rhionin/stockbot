# AtomS3 Lite reference bootloader and partition table (Arduino-ESP32 2.0.17)

This directory holds the **bootloader** and **partition table** binaries that the C++ Stockbot firmware on AtomS3 Lite devices is built with: **Arduino-ESP32 2.0.17** on **ESP-IDF 4.4.7**, with an image header of **DIO / 80 MHz / 8 MB**. All four PlatformIO environments (`sticks3_u173`, `sticks3_u173_uart_split`, `atoms3lite_u173_bench`, `atoms3lite_symcode_hid`) use the same `bootloader_qio_80m.elf` converted with the same `esptool` flags, so this single `bootloader.bin` applies to every target.

## Why these files are in git

CI uploads `fw-cpp-<target>` artifacts that include these binaries, but those artifacts **expire after 30 days** and the bootloader/partition blobs are not otherwise versioned in the repository. They are kept here for **S8 bench boot recovery** (when CI artifacts are gone) and for **comparing** against a dump read from a bench unit. This is an interim measure until firmware releases routinely ship these files.

## Files

| File | Size (bytes) | SHA256 |
| --- | ---: | --- |
| `bootloader.bin` | 15104 | `1776e4dd896a69d0a5c2e79957b0e2a88aa4129b1381d6478683515a1f6af343` |
| `partitions.bin` | 3072 | `8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1` |

`SHA256SUMS` lists the same digests in standard `sha256sum` format (`sha256sum -c SHA256SUMS`).

### Bootloader image header

First four bytes of `bootloader.bin`: `e9 03 02 3f`

- **0xE9** — ESP image magic  
- **0x03** — three segments  
- **0x02** — SPI flash mode 2 = **DIO**  
- **0x3F** — flash size / frequency byte = **8 MB** / **80 MHz**

## How these binaries were produced

Pinned versions (from `firmware/platformio.ini` and PlatformIO):

- `platform = espressif32@6.9.0` → `framework-arduinoespressif32 ~3.20017.0` (Arduino-ESP32 2.0.17, ESP-IDF 4.4.7) and `tool-esptoolpy ~1.40501.0` (**esptool 4.5.1**)

**bootloader.bin** — from the framework file:

`tools/sdk/esp32s3/bin/bootloader_qio_80m.elf`  
(SHA256 of ELF: `560840d6c79821041dba7ce7a9a3a4fb98bc196b9017d66f9d4bc63f6da3a4c3`)

```bash
esptool.py --chip esp32s3 elf2image \
  --flash_mode dio --flash_freq 80m --flash_size 8MB \
  -o bootloader.bin bootloader_qio_80m.elf
```

**partitions.bin** — from `firmware/partitions_8mb_ota.csv` at master `c3abfc4a477f6ff7d0d04a9c27679152691d75bd`, e.g.:

```bash
python3 tools/gen_esp32part.py firmware/partitions_8mb_ota.csv partitions.bin
```

(`espflash partition-table --to-binary` with **espflash 3.3.0**, as pinned in `firmware/rust/esp-toolchain.env`, yields the same bytes.)

## What is NOT verified yet

1. These are **reference files regenerated from pinned public sources**. They have **not** yet been compared byte-for-byte with the `bootloader.bin` inside the CI artifact `fw-cpp-atoms3lite_u173_bench`, nor with a dump read from a real bench unit.
2. `partitions.bin` reflects `firmware/partitions_8mb_ota.csv` at commit `c3abfc4a`. If that CSV changes, this file must be **regenerated** and the checksums in this README and `SHA256SUMS` updated.
3. The PlatformIO framework pin is a floating range (`~3.20017.0`), not a content hash. That is why the exact bytes are archived here.

## Usage cautions

- **Do not commit device flash dumps** into this tree (they can contain NVS and other device-specific data).
- **Do not flash** from this directory without following the Architect’s S8 bench recovery procedure.