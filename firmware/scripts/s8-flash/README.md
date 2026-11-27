# StickS3 Windows flash flow (slice S8)

Scripted, read-first workflow for flashing `sticks3_u173` from Windows (Windows PowerShell 5.1 or pwsh 7). **No hardware is exercised in CI**; bench flashing is CJ-gated.

## Safety rules

- **Never** writes bootloader (`0x0`), partition table (`0x8000`), **otadata**, or **NVS**.
- **Never** uses `espflash flash` or `pio upload` (PlatformIO can write `boot_app0.bin` at `0xE000`).
- On any STOP, the script writes nothing further; use **Restore (R1)** from your local backup folder to recover the app slot.

## USB connection (StickS3 on Windows)

espflash needs a `--before` reset to talk to StickS3 reliably: plain `board-info` on a silent COM port often fails until **`--before usb-reset`** is used. That reset can **re-enumerate** the port (e.g. COM3 → COM4), so the script **re-resolves the COM port before every espflash call** (VID `303A`, PIDs in `targets.json`), polling up to ~10 seconds after a reset.

**Connect ladder** (logged): `usb-reset` → `default-reset` → `no-reset` (chip already in ROM bootloader). If all fail, the script prints manual download-mode steps (hold power until the **green LED blinks**, then use a USB-C **data** cable) and STOPs.

On the first successful `board-info`, the script checks the chip (`esp32s3`) and flash size (8MB) and records the **MAC** from espflash's `MAC address:` line. After that, every `board-info`, `read-flash` and `write-bin` result must print the same MAC, and the espefuse summary must report it too. A missing or different MAC is a STOP (guards against a different device on the re-enumerated port). The MAC is saved in `state.json`, so later phases in a new process check against it. Optional `-ExpectedMac` adds an explicit check.

All non-monitor espflash calls use **`--before usb-reset`** (default) and **`--after no-reset`** so the chip stays in the ROM bootloader for scripted read/write.

## Quick start (read-only)

```powershell
cd firmware\scripts\s8-flash
.\Invoke-StockbotFlash.ps1 -Phase All -ImagePath C:\path\to\firmware.bin
```

`All` runs **Prereqs → Download (or `-ImagePath`) → ReadOnly → Plan** and never enters Write.

For a CI artifact:

```powershell
.\Invoke-StockbotFlash.ps1 -Phase All -RunId <github_run_id> -ExpectedSha256 <sha256 from PR/CI>
```

Plan, Write and Restore reuse the newest `<timestamp>_<RunId or local>` folder that has a `state.json`, so run them with the same `-RunId` (or none) as the ReadOnly run. If there is no such folder they STOP.

Write requires a prior successful Plan in that folder, **mandatory** `-ExpectedSha256`, and the typed confirmation passed as `-Confirmation 'WRITE 0x<offset> <SHA8>'` (case-sensitive; no `-Force`). Plan prints the exact line to type. Restore takes `-RestoreConfirmation 'RESTORE 0x<offset> <SHA8>'`.

## Phases

| Phase | Purpose |
|-------|---------|
| Prereqs | COM port autodetect (VID_303A&PID_1001). espflash 3.3.0 and esptool 4.8.1 must already be installed; `-InstallPrereqs` does nothing yet |
| Download | `-ImagePath` only; `-RunId` names the run folder but `gh run download` is not implemented yet |
| ReadOnly | board-info (chip, flash size, MAC), espefuse eFuse check, full 8MB backup + MD5 check, `state.json` |
| Plan | Table allow-list, otadata/slot, image checks, prints `WRITE 0x… SHA8` |
| Write | Typed confirm → live re-read of table/NVS/otadata (must equal backup) → `write-bin --after no-reset` → read-back → full re-read compare → monitor |
| Restore | Verifies `backup.bin` against its ReadOnly sha256, typed `RESTORE …`, writes the full slot, reads it back |

## eFuse check

espflash 3.3.0 `board-info` does not report Secure Boot or Flash Encryption, so the script runs espefuse itself on the resolved port:

```
python -m espefuse --chip esp32s3 --port <COMn> --before usb_reset summary --format json --file <run>\efuse_summary.json
```

Install it with `pip install esptool==4.8.1` (the command is `espefuseCommand` in `targets.json`). It STOPs unless `SECURE_BOOT_EN` is `false` and `SPI_BOOT_CRYPT_CNT` is `Disable` (or `0`). Any other value, a missing field, a failed espefuse run or output that is not JSON is a STOP. The summary's `MAC` must match the bound device. The test fixture `tests/fixtures/espefuse_summary_virt_esp32s3.json` is real espefuse 4.8.1 output from `--virt` (a blank virtual esp32s3) with only the MAC value replaced.

## Post-write compare

`write-bin` erases whole 4 KiB sectors. The compare therefore checks the image bytes exactly, accepts `0xFF` or the old byte from the end of the image to the end of its last sector, and requires every other byte of the 8MB flash (rest of the slot included) to be unchanged.

Full-chip R2 restore is **manual** only (documented here, not scripted).

## STOP conditions

Wrong chip/flash size; missing or changed MAC; secure boot or flash encryption; unreadable eFuse; backup MD5 mismatch; unknown partition table; otadata NEW/PENDING_VERIFY; table/NVS/otadata changed since ReadOnly; image/header/hash mismatch; compare failure; ambiguous COM port; run folder ACL could not be set.

## Privacy

Run output lives under `%LOCALAPPDATA%\StockbotFlash\<timestamp>\` with ACLs restricted to the current user. **Do not** commit NVS dumps, serial logs with tokens, or raw flash beyond the allowed summary (boot header + 16-byte slot prefix).

## Partition table allow-list

`targets.json` pins SHA256 `8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1` for the 3072-byte table compiled from `firmware/partitions_8mb_ota.csv` via `tests/fixtures/gen_partition_table.py`. Before the C++ tree was removed, this was byte-identical to the C++ 4.4.7 reference `partitions.bin` and to espflash 3.3.0 `partition-table` output. Default_8MB-style tables (e.g. app0 at `0x10000`) are **not** allow-listed; Plan STOPs on them.

## Tests

```powershell
Install-Module Pester -RequiredVersion 5.6.1 -Force
Invoke-Pester -Path .\tests\*.Tests.ps1
pwsh -NoProfile -File .\tests\run-mutants.ps1
```