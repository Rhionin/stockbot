# StickS3 Windows flash flow (slice S8)

Scripted, read-first workflow for flashing `sticks3_u173` from Windows (Windows PowerShell 5.1 or pwsh 7). **No hardware is exercised in CI**; bench flashing is CJ-gated.

## Safety rules

- **Never** writes bootloader (`0x0`), partition table (`0x8000`), **otadata**, or **NVS**.
- **Never** uses `espflash flash` or `pio upload` (PlatformIO can write `boot_app0.bin` at `0xE000`).
- On any STOP, the script writes nothing further; use **Restore (R1)** from your local backup folder to recover the app slot.

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

Write requires a prior successful Plan on the same run folder, **mandatory** `-ExpectedSha256`, and an interactive typed confirmation (no `-Force`).

## Phases

| Phase | Purpose |
|-------|---------|
| Prereqs | espflash 3.3.0, Python/espefuse, gh; COM port autodetect (VID_303A&PID_1001) |
| Download | `gh run download` or `-ImagePath` |
| ReadOnly | board-info, eFuse, full 8MB backup + MD5 check, `state.json` |
| Plan | Table allow-list, otadata/slot, image checks, prints `WRITE 0x… SHA8` |
| Write | Typed confirm → `write-bin --after no-reset` → read-back → full re-read compare |
| Restore | R1 slot restore from backup (typed `RESTORE …`) |

Full-chip R2 restore is **manual** only (documented here, not scripted).

## STOP conditions

Wrong chip/flash size; secure boot or flash encryption; unreadable eFuse; backup MD5 mismatch; unknown partition table; otadata NEW/PENDING_VERIFY; image/header/hash mismatch; compare failure; ambiguous COM port.

## Privacy

Run output lives under `%LOCALAPPDATA%\StockbotFlash\<timestamp>\` with ACLs restricted to the current user. **Do not** commit NVS dumps, serial logs with tokens, or raw flash beyond the allowed summary (boot header + 16-byte slot prefix).

## Partition table allow-list

`targets.json` pins SHA256 `8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1` for the 3072-byte table (C++ 4.4.7 reference and `firmware/partitions_8mb_ota.csv` via `tests/fixtures/gen_partition_table.py`).

## Tests

```powershell
Install-Module Pester -RequiredVersion 5.6.1 -Force
Invoke-Pester -Path .\tests\*.Tests.ps1
```