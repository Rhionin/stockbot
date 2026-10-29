# ADR-001: Over-the-air firmware updates for ESP32-S3 scanners

- **Status:** Accepted. The product decisions were made by CJ on 2026-10-29. The technical rulings are the Architect's.
- **Issue:** [#452](https://github.com/Rhionin/stockbot/issues/452), ticket T1.
- **Related:** [#445](https://github.com/Rhionin/stockbot/issues/445), the Rust firmware rewrite (P0.5).
- **Design:** [firmware-ota.md](firmware-ota.md).
- **Contract:** [api-contract.md, Firmware updates](api-contract.md#firmware-updates).

This is the repo's first ADR. Later ADRs use `docs/design/adr-NNN-<slug>.md` with a three-digit number.

## Context

CJ wants to send firmware fixes to scanners remotely, before the second household is onboarded: *"Surely there will be things we want to change in the firmware, and we won't get it right on our first try. I want the ability to send those fixes remotely."*

These facts are from master at `bd8d4a9`. The full list, with file and line citations, is in #452 §0.

- `firmware/partitions_8mb_ota.csv` already reserves two 3 MB app slots (`ota_0`, `ota_1`) and `otadata`. All three device envs use it.
- `espressif32@6.9.0` brings Arduino-ESP32 2.0.17 on ESP-IDF 4.4.x. Its precompiled ESP32-S3 bootloader has app rollback enabled (`CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE=y`). Secure Boot, signed apps, and anti-rollback are off.
- The Arduino core marks a newly booted app valid on its own unless the app overrides the weak `verifyRollbackLater()` to return `true`.
- None of the device envs build on master today (#452 F1, F2). CI only runs host tests and publishes no firmware.
- The heartbeat contract already reserves `update?: {version,url,sha256,sig}`. The server does not send it yet.
- The Pi hub verifies releases with Ed25519 (`internal/hub/update.go`), but its signed form does not bind a target.
- The firmware will be rewritten in Rust (#445) right after the P0 list. The C++ firmware must be able to install the first Rust image over the air.

## Decisions

### Product decisions (CJ, 2026-10-29)

1. **Rollout is bench first, then one-click promote (#452 Q1, option b).** Every new release lands on the `beta` channel and installs automatically on CJ's bench scanner. When it reports healthy, CJ promotes the version to `stable` with one click. Household devices follow `stable`. Decided by CJ on 2026-10-29, relayed by Stockbot Manager.
2. **The signing key is a GitHub Actions environment secret (#452 Q2, option a).**
   - The Ed25519 signing seed is the secret `FW_SIGNING_KEY_ED25519` in the GitHub environment `firmware-release`.
   - Deployments to that environment are limited to `master`, and CI signs automatically.
   - An offline backup of the seed is kept.
   - Decided by CJ on 2026-10-29, relayed by Stockbot Manager.

### Architect technical rulings

3. **Signing uses Ed25519 (RFC 8032, pure) over a canonical text manifest.**
   - The signed bytes are the text manifest `stockbot-fw-manifest-v1` defined in [firmware-ota.md §4](firmware-ota.md#4-manifest-and-signature). There is no JSON canonicalization.
   - The firmware key is separate from `HUB_RELEASE_PUBKEY`.
   - Go `crypto/ed25519`, C++ (libsodium or Monocypher), and Rust (`ed25519-dalek`) all verify these bytes the same way.
   - The app checks the signature itself, before it switches the boot partition.
4. **Firmware ships inside the server image.**
   - CI bundles the signed binaries into `ghcr.io/rhionin/pantry`.
   - At startup the server verifies each release and imports it into the `pantry-data` volume and the `firmware_releases` table.
   - Devices download only from `GET /api/device/firmware/{releaseId}` on their own server. There is no upload endpoint and no new Pi secret.
5. **No Secure Boot, no flash encryption, and no eFuse burns.** Anti-rollback stays off. USB flashing must always be able to recover a device, and the bootloader is never changed over the air.
6. **Downgrades are allowed and audited.** A downgrade happens only when an operator yanks a release or pins a device. Each one writes an `audit_log` row. Downgrades are needed for a Rust to C++ fallback.
7. **The image is valid only after an authenticated heartbeat.** A new image becomes valid after its first `200` from `POST /api/device/heartbeat` with its stored device token. If that has not happened 10 minutes after boot, it marks itself invalid and reboots into the previous slot.
8. **Install only when it is safe.** The device must be paired, the offline ring empty, and the device idle for 120 s. A StickS3 must also be docked or at 50% battery or more. A scan cancels a download in progress.
9. **Migration `029_firmware_ota.sql` is additive only.** It is PROPOSED here and needs CJ's approval at T2 PR time. This ADR does not add it.

## Options considered

| Topic | Chosen | Rejected, and why |
|---|---|---|
| Rollout | Beta bench canary, then one-click promote (CJ) | Promoting every release automatically: a bad build reaches households. Manual approval per household: too much effort for a fleet of under 10. |
| Key custody | GitHub environment secret limited to master, plus an offline backup (CJ) | Approval on every run: a click per release. Offline-only signing: no remote fixes while CJ is away. |
| Signature | Ed25519 | ECDSA P-256: DER encoding ambiguity and nonce risk. Espressif Secure Boot v2 (RSA-3072): not in the precompiled Arduino build, needs eFuses burned, and is tied to IDF. |
| Binary storage | Bundled in the server image | Server pulls from GitHub Releases: needs a token on the Pi and a runtime egress dependency. Operator upload: needs a larger request-body cap and a new upload surface. |
| Update signal | `update` on the heartbeat response, reusing the reserved key | A separate poll endpoint: more traffic. Putting it in `DeviceHubConfig`: every release would bump `configVersion`. |
| Version policy | Signed images allowed in both directions, downgrades audited | eFuse anti-rollback: irreversible and blocks Rust to C++ fallback. |
| Framework | Stay on `framework = arduino` with the stock bootloader | `arduino, espidf` with a custom sdkconfig and bootloader: more build risk, and a bootloader we could never update in the field. |

## Consequences

- Every server deploy also delivers the current firmware release. The image grows by about 10 MB per release.
- Old releases stay on the volume, so pins and downgrades keep working after an image update. The server keeps the last 5 releases per target.
- With no Secure Boot, someone with physical USB access can flash anything. This is accepted for a home pantry device, and the signature check protects the network path.
- If the GitHub secret leaks, an attacker can sign images. They still need control of the server to offer one. The recovery is to rotate the key (design §4).
- T0 has to fix the device builds and the missing `m5stack-sticks3` board before any OTA work is testable.
- The bench scanner is an AtomS3 (`atoms3lite_u173_bench`). StickS3 targets have no canary unless a StickS3 is also put on `beta`. Promote reports `uncoveredTargets` so CJ sees this before clicking.

## Language independence and C++ to Rust handoff

The contract, manifest, signature, version reporting, rollback signals, partition table, and NVS keys are frozen for both languages. Changing them needs a new ADR. Details are in [firmware-ota.md §9](firmware-ota.md#9-c-to-rust-handoff).

- **Partition table:**
  - Offset `0x8000`.
  - `nvs` 0x9000/0x6000, `otadata` 0xf000/0x2000, `ota_0` 0x20000/0x300000, `ota_1` 0x320000/0x300000, `littlefs` 0x620000/0x100000.
  - An image is 3 MB at most, and CI fails above 2.4 MB.
- **Bootloader:**
  - The IDF 4.4.x bootloader from Arduino-ESP32 2.0.17, with rollback on and anti-rollback, Secure Boot, and signed apps off. Flash mode DIO.
  - A Rust (IDF 5.x) app must be proven to boot under it in #445 Phase 0.
  - Rust USB flashing uses the archived `bootloader.bin` and `partitions.bin`.
- **NVS:**
  - Namespace `stockbot`.
  - `str` keys: `wifi_ssid`, `wifi_pass`, `server_url`, `device_token`, `label`, `default_dir`, `commit_mode`, `ota_rel`, `ota_err`, `ota_supp`.
  - `u32` keys: `inact_rev`, `cfg_ver`.
- **Signing:** Ed25519 over `stockbot-fw-manifest-v1`. Both languages use the same key and `keyId` table in `firmware/keys/`, and pass the same golden vectors.
- **Reporting:**
  - `version` is semver and `fwTarget` uses the fixed target ids.
  - `otaState`, `otaError`, `otaReleaseId`, and `rollback` are reported the same way.
  - The image is marked valid after its first authenticated `200` heartbeat. The check-in deadline is 10 minutes.
- **Image:** a standard ESP-IDF app image, served raw.
- **Scan ring:** an install requires an empty ring, so Rust does not need to read the `SBR1` file format.
