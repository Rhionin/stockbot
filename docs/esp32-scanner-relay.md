# Spike: ESP32 relay for the Symcode scanner

Findings for [issue #222](https://github.com/Rhionin/stockbot/issues/222). This is a research note. No firmware was built, no hardware was ordered, and no production behavior changed.

It lines up with [issue #221](https://github.com/Rhionin/stockbot/issues/221), the multi-tenant SaaS design. That issue's scan options are phone-camera scanning (the free-tier default), a Bluetooth scanner paired to a phone or tablet, and an optional always-on pantry device that relays barcodes to the hosted server. This note is that third option. The relay assumes #221's household tenant, device token, and server-shipped updates. It should not be bolted onto the current single-household Pi API ahead of that design.

## How to read this

- **Verified.** Checked against a cited manual, product listing, Espressif document, or this repository.
- **Inference.** Follows from those sources, and was not measured on CJ's scanner or on a board.
- **Hands-on.** Only a test with the scanner, and usually a board, can settle it.

Prices are distributor or marketplace quotes gathered on 16 October 2026. They move.

## Recommendation

Prototype an **ESP32-S3** relay that reads the scanner's **included 2.4 GHz USB dongle** as a USB keyboard and POSTs each barcode to the hosted API. Leave the scanner in instant-upload mode. Power the board from a USB charger. Pair the household from a phone browser (captive portal) using a short code the app shows, with a QR sticker on the device for the setup network.

That is the path that stays inside a roughly $5–10 parts budget once it is a small custom board, uses a radio mode the scanner ships ready for, and does not depend on Classic Bluetooth. Classic Bluetooth HID host exists only on the original ESP32, and the Symcode keyboard mode is very likely Classic. The S3, C3, and C6 cannot host that link.

The first hands-on gate is cheap to state: plug the dongle into a Linux machine (the Pi will do) and confirm it is a USB HID keyboard, then repeat on an ESP32-S3 USB host. If the dongle is not HID, the fallback order is at the end of this note.

## What the Pi does today

**Verified**, from this repo. A scan reaches the queue as keystrokes, not as a barcode protocol.

`deploy/scan-input.md` sets `SCAN_INPUT=device` and reads the evdev node at `/dev/input/pantry-scanner`. The udev rule matches the input parent's `ATTRS{id/vendor}` and `ATTRS{id/product}`. Those sysfs attributes exist for USB HID and for Bluetooth HID (`uhid`). `internal/scanlistener` assembles key events into a line, treats Enter as the end of the barcode, and does not put that Enter in the barcode. A line that equals the configured stock-in or stock-out control barcode (defaults `STOCK_IN` and `STOCK_OUT`, case-sensitive) switches mode and creates no scan entry. Any other line is queued for `HEADLESS_USER_ID`, which defaults to `user-1`. `internal/server/handler_scanner.go` returns stock-in to stock-out after `scannerIdleTimeout` (5 minutes) of no scan traffic. `POST /api/scans` does not know about control barcodes. The listener strips them before they ever hit that route.

On the public hostname, `HouseholdAuth` accepts a session cookie or the household Basic password. A request that is not marked as the public entry (the LAN listener) is not asked to sign in. There is no device token.

A household relay replaces that whole chain. It has to turn keystrokes into the same lines, decide direction at the moment of the scan, and authenticate as one household when the server is shared. It does not need a tunnel, a hostname, or an inbound port. Outbound HTTPS to the hosted API is the remote-access path, which is what #221 asks for when it says households should not depend on a Cloudflare account.

## The scanner

Amazon ASIN [B01M264K5L](https://www.amazon.com/dp/B01M264K5L), structured product data **model MJ-2030-B**, UPC 798220489650, sold by Symcode. **Verified** from that listing on 16 October 2026:

- Three connections: Bluetooth, 2.4 GHz wireless, and a USB cable. The box includes the scanner, a stand, a user manual, a wireless dongle, and a USB cable.
- Bluetooth is described as the phone and iPad path. The 2.4 GHz dongle is described as the PC path. The listing claims about 200 m open-air and 30 m with obstacles for 2.4 GHz.
- Battery: 1800 mAh lithium polymer, claimed 24 hours continuous scanning, 3.5 hours to charge, 60 days standby, 3.7 V operating voltage.
- It reads 1D and 2D symbols, including QR, and can store scans on the scanner (the listing says 16 MB and "50K+" codes) until someone scans an "upload data" setup code.
- The same listing body also says "NA-2030" and, in the multi-scanner blurb, "MJ-4590DB-A" with up to eight scanners per dongle. Those names disagree with the model field. Treat the model number on the label of CJ's unit as the one that counts, and treat "eight scanners per dongle" as unverified for this unit.

Customer reviews on that ASIN disagree about Bluetooth. Some say it connects as a USB keyboard through the dongle and types the digits plus a terminator. Others say it will not pair to a phone and is dongle-only. **Verified** as the state of the reviews. **Inference:** either revisions sold under one ASIN differ, or reviewers who never scanned the HID pairing barcode stayed on the 2.4 GHz dongle. The issue says CJ's unit pairs to the Pi as a keyboard. That operational fact stands next to the reviews. It does not, by itself, say whether the link is Classic or BLE. BlueZ creates a `uhid` keyboard for both.

### Classic Bluetooth, BLE, or both

No manual titled MJ-2030-B or B01M264K5L turned up. The Symcode manuals that document the same three-mode radio are listed in Sources. They agree with each other, and they are the best written evidence. **Verified** for those manuals (2877, BT30, R30, 1932DB, R50):

- The radio is described as a **Bluetooth 4.1 module** with three protocols selected by setup barcode: **HID**, **SPP**, and **BLE**.
- HID mode is the keyboard path. The phone or PC pairs a device named **"Barcode Scanner HID"**. Android, iOS, and Windows are listed for that mode. Linux is listed as a supported operating system for the Bluetooth protocol.
- SPP and BLE are separate setup barcodes, with different LED patterns (alternating blue lights for HID, both lights together for BLE, one light for SPP, in the 2877 and R30 manuals).
- The BT30 manual's troubleshooting says that if a phone or computer cannot find the scanner, confirm it is in **Bluetooth HID** mode, and that other modes need the supplier's testing tools.

**Verified** from the Bluetooth SIG, independent of Symcode: the Serial Port Profile (SPP) is a Classic BR/EDR profile. BLE has no SPP. A module that advertises SPP and BLE as two modes is a dual-mode radio.

**Inference.** HID mode, the one that shows up as "Barcode Scanner HID" and types into an OS, is Classic BR/EDR HID. It is documented separately from BLE, and the manuals point phones and PCs at HID mode specifically. BLE mode is unlikely to be the keyboard the Pi is using. It may be a GATT serial protocol rather than HID-over-GATT. That is why an ESP32-S3, which is BLE only, should not be asked to pair with the scanner in the mode the Pi uses today.

**Hands-on.** On the Pi, with the scanner connected the way Stockbot uses it:

```bash
bluetoothctl info <mac>
```

A Classic HID link shows a BR/EDR address and the HID profile, and it was discovered with a classic scan. An LE keyboard shows an LE address type and the HID-over-GATT service (`0x1812`). Also read the printed protocol page in the unit's own booklet, and the model string on the label. Those two checks close the gap this spike cannot close from the web.

## Which chips can host a BT keyboard

**Verified** from the ESP-IDF Bluetooth architecture overview (v6.1) and the ESP-FAQ USB page. Classic BR/EDR in the current ESP-IDF table is the original ESP32 only.

| Chip | Wi-Fi | Classic BR/EDR | BLE | USB host |
| --- | --- | --- | --- | --- |
| ESP32 (original) | yes | yes | yes | no |
| ESP32-S2 | yes | no | no | yes, full-speed OTG |
| ESP32-S3 | yes | no | yes | yes, full-speed OTG |
| ESP32-C3 | yes | no | yes | no (USB-Serial-JTAG device only) |
| ESP32-C6 | yes | no | yes | no (USB-Serial-JTAG device only) |
| ESP32-C5, C2, C61, H2 | varies | no | yes | not a candidate here |

The original ESP32 is the only chip in that table that can be the host of a Classic HID keyboard. ESP32-S3, C3, and C6 can host BLE HID. They cannot host the Classic HID session the Symcode HID barcode sets up. ESP32-S2 can host USB and join Wi-Fi, and it has no Bluetooth radio at all, so it cannot fall back to BLE if the dongle path fails.

### Libraries

**Verified.**

- **ESP-IDF Bluetooth HID Host** is the host stack. The API is `esp_bt_hid_host_init`, `esp_bt_hid_host_connect`, and the `ESP_HIDH_DATA_IND_EVT` report callback in `esp_hidh_api.h`. The example `examples/bluetooth/esp_hid_host` scans, connects, and prints reports for Classic, BLE, or dual mode. Its default sdkconfig turns on `CONFIG_BT_CLASSIC_ENABLED` and `CONFIG_BT_HID_HOST_ENABLED`, which builds for the original ESP32. The ESP32-S3 sdkconfig for that example is BLE only (`CONFIG_BT_BLE_ENABLED`, no Classic). Espressif closed [esp-idf#12400](https://github.com/espressif/esp-idf/issues/12400) by saying the S3 does not support Classic and to use an ESP32.
- **Arduino-ESP32 `BluetoothSerial`** is Classic SPP. A scanner in HID mode does not present an SPP stream, so this library will not deliver barcodes. The same core's `BLEHIDDevice` is the peripheral role: the ESP shows up as a keyboard. That is the opposite of a relay.
- Calling the ESP-IDF HID host API from an Arduino-ESP32 sketch is possible if the sketch includes `esp32-hal-alloc-bt-classic-mem.h` so the core keeps the Classic memory. That is a bridge, not a higher-level Arduino HID-host library. There is no Arduino-ESP32 class that pairs a Classic keyboard and returns keystrokes.

**Inference.** Product firmware should be ESP-IDF. The USB host example, the HID host example, HTTPS OTA, and app rollback are all maintained there. Arduino is a reasonable way to blink an LED during a first wire-up, and a poor place to own rollback and the scan buffer.

**Hands-on**, only if the Classic fallback is attempted. The HID host example has to be pointed at this scanner, left paired across power cycles, and watched while Wi-Fi and TLS are also up. The API existing is not the same as this scanner's report descriptor decoding cleanly. Reports arrive as raw HID bytes. Boot-protocol keyboards are a known shape (modifier byte plus six key slots). A scanner that uses a vendor report still needs a small decoder. Nothing in the manuals publishes the report descriptor.

## USB host, the path to prototype

**Verified.**

- ESP32-S2 and ESP32-S3 have a full-speed USB 2.0 OTG controller that can be the host. The original ESP32, C3, and C6 do not.
- ESP-IDF example `examples/peripherals/usb/host/hid` targets ESP32-S2, ESP32-S3, ESP32-P4, and ESP32-H4. It installs the `usb_host_hid` class driver and reads boot-protocol keyboards and mice. The example README says it supports the HID boot protocol, which most USB keyboards implement, and that it prints each key.
- On ESP32-S3 the USB-OTG controller and the USB-Serial-JTAG controller share one PHY. Host mode on the native USB port means the console moves to a UART. A board that wires both through a hub chip can avoid that. Confirm the schematic of whatever board is used.
- The Espressif ESP32-S3-USB-OTG board schematic switches 5 V to the USB-A receptacle and labels that rail as a 500 mA limit. The USB host stack does not do over-current protection in software. The board has to limit VBUS.

**Inference.** The 2.4 GHz dongle is the right thing to plug in. It is in the box, the listing and reviews describe it as a USB keyboard, and the scanner keeps its own battery. The cable is a worse default: the scanner charges over that cable, and a 500 mA VBUS limit may be tight. Related Symcode manuals (BT30) specify a 5 V / 1 A charge. This ASIN's listing does not. 1800 mAh in 3.5 hours is about 500 mA if the charge were a flat current, which a real charger is not. Measure it before anyone plans on wired mode.

A boot-protocol keyboard sends the same reports the Pi's evdev path already turns into characters. The relay assembles those characters until Enter, then drops the Enter, matching `scanlistener`.

**Hands-on.**

1. On the Pi, `lsusb` and `/proc/bus/input/devices` with the dongle inserted. Record vendor id, product id, and whether `bInterfaceSubClass` is boot (1) and `bInterfaceProtocol` is keyboard (1).
2. Confirm a scan types digits and a trailing Enter, and that `STOCK_IN` still matches exactly.
3. Repeat on an S3 host. If the interface is not boot protocol, the same host stack can still read interrupt reports, and the decoder gets a bit larger. That is still the S3. It does not force a move to Classic Bluetooth.

Cheap S3 boards (DevKitC-1, "Super Mini") expose the native USB-C as a device port. They do not provide a USB-A receptacle or a VBUS switch. An OTG adapter only works if 5 V is actually sourced to the dongle. The official USB-OTG devkit does this, at a higher board price. A custom board does it with a load switch. This is the main hardware trap in an otherwise straightforward path.

## Wi-Fi onboarding and household pairing

#221 requires install with no SSH and no `.env`, and a pairing flow where the app shows a code or a QR and the device receives a household-scoped token that can be revoked.

**Verified** ESP-IDF pieces that already exist:

- `examples/protocols/http_server/captive_portal` brings up a softAP, answers every DNS query with the softAP address, redirects HTTP to a page, and can advertise the portal URL in DHCP option 114. The README is explicit that it does not redirect HTTPS. Phone captive-portal probes are HTTP, which is why the DNS funnel works when it works.
- `wifi_prov_mgr` with `wifi_prov_scheme_softap` provisions Wi-Fi over HTTP on the softAP, with a proof-of-possession. The matching phone apps accept a QR whose payload is JSON of the form `{"ver":"v1","name":"PROV_…","pop":"…","transport":"softap"}`.

**Recommended flow**, design only:

1. Unprovisioned boot starts softAP `Stockbot-XXXX` (last bytes of the MAC). A sticker QR encodes the ESP-IDF softAP provisioning payload, including a per-device proof-of-possession. The QR identifies the setup network. It is not the household credential.
2. The household app shows a short one-time pairing code. The phone joins the softAP. The captive page asks for the home Wi-Fi name and password and that code. The QR path skips typing the softAP name. The code is still required.
3. The device calls `POST /api/device/pair` with the code. The server binds the device to the household that issued the code, stores a hash of a new random token, and returns the token plus the control-barcode strings.
4. The device writes Wi-Fi credentials and the token to NVS, drops the softAP, and joins the home network. Holding the boot button clears NVS and returns to step 1. Revocation is deleting the token hash in the app. The device learns it is revoked when the API answers 401, and it goes back to pairing.

**Inference.** Offer both the captive page and the QR. The page is the path with no extra app. The QR is how the phone finds the right softAP and proves the person is next to the sticker. iOS and Android captive detection is the flakiest part of this and is a hands-on test on current phones. The ESP provisioning QR is the backup when a phone refuses to pop the portal.

The device has no camera, so it cannot scan a QR that the app displays. The QR lives on the device sticker. The secret that ties the device to the household is the code the app displays.

## HTTPS POST, offline buffer, and API changes

**Verified** current contract. `POST /api/scans` takes JSON `barcode`, `userId`, and optional `direction`, `unitCount`, and `expiresAt`. Empty barcode or empty user id is a 400. A product barcode with no direction is stamped with the server's current scanner mode. `CreateScanEntry` merges into an existing pending or flagged row with the same user, barcode, and direction by adding to `unit_count`. A caller-supplied id is ignored when that merge happens. A retried POST of one scan therefore increments the count a second time. Control barcodes are not special on this route.

That contract is right for the Pi, which is on the LAN, signed in as the household, and calls `CreateScanEntry` once per real scan. It is wrong for a remote device that will retry.

**Design to add with #221**, not in the Pi build:

- `POST /api/device/pair` with the one-time code. Response: device token, household id, `stockInBarcode`, `stockOutBarcode`, and the idle timeout (today 5 minutes).
- `POST /api/device/scans` with `Authorization: Bearer <token>`. Body is a list of `{id, barcode, scannedAt, direction}`. The household comes from the token. The client does not send a `userId` that could point at another household. The scan is attributed to the household scanner actor (the same role as `HEADLESS_USER_ID`).
- Idempotency: a unique `(device, client id)` recorded before `CreateScanEntry`. A repeat of the same id returns the original entry and does not add to `unit_count`. Two different ids for the same barcode still merge, because those are two real scans.
- Direction is the mode at scan time, including the 5-minute return to stock-out. The device learns the control strings at pair time and on each successful check-in. It classifies control barcodes locally and does not POST them as products. A flush an hour later must send the direction it stamped, not the server's mode at flush time.
- `GET /api/device/firmware?version=` returns 204, or a version, an HTTPS URL, and a SHA-256. `DELETE` of the device revokes the token.
- TLS uses the ESP-IDF CA bundle and a hostname compiled into the firmware. SNTP runs before the first POST. A wrong clock fails the handshake, and the scans stay in the buffer.

**Buffer.** Append each accepted line to a small flash filesystem (a 256 KB LittleFS partition is more than enough: a barcode, an id, a timestamp, and a direction fit in well under 128 bytes). Delete a record only after a 2xx that names its id. Back off while the POST fails. Cap the file and drop the oldest records only if a household scans through a long outage. Say so in the device log. The scanner's own 50k storage is a different buffer. Leave the scanner in instant upload. Scanning its "upload data" code would dump a backlog through the relay and create a burst of real scans, which is what that feature is for, and it will surprise someone who did not mean to.

**Inference.** One TLS connection per flush, with the list endpoint, matters more than per-scan HTTP. A pantry session is dozens of scans, not thousands. The buffer is for Wi-Fi and server blips, not for a store-and-forward warehouse mode. The scanner already has that mode if someone wants it.

## OTA and rollback

**Verified** from the ESP-IDF OTA guide (v6.1):

- App rollback is `CONFIG_BOOTLOADER_APP_ROLLBACK_ENABLE`. It is off by default in that guide. With it on, a newly written app is pending until it calls `esp_ota_mark_app_valid_cancel_rollback()`. A reset before that call boots the previous app. `esp_ota_mark_app_invalid_rollback_and_reboot()` forces the revert. The new app gets one boot to prove itself.
- A later ESP-IDF page describes rollback as enabled by default and adds a choice between confirming during startup and confirming from application code. The Kconfig must be set explicitly in this firmware. Do not trust the default of whichever IDF a future tree happens to use.
- Anti-rollback (`CONFIG_BOOTLOADER_APP_ANTI_ROLLBACK`) burns an eFuse version and then refuses to boot an older security version. Use it only after the update path has already worked. Combined with rollback, the device can return to the previous image only when that image's security version is still allowed. A bad eFuse bump removes the escape hatch.
- `esp_https_ota` downloads an image over HTTPS into the inactive OTA slot. The partition table needs two app slots. An 8 MB flash module (ESP32-S3-WROOM-1-N8) holds two slots, NVS, and the scan buffer. 4 MB can also, with less room.

**Recommended confirm rule.** After OTA, the new image marks itself valid only after it has joined Wi-Fi and completed one authenticated heartbeat. A image that crashes, or that cannot speak TLS, reboots back to the previous image without a human and without SSH. That is the rollback #221 asks the device to have, and it is the same shape as the Pi's "update, and roll back if health fails," moved onto the bootloader.

Arduino OTA (the IDE's `ArduinoOTA`, and the various HTTP update libraries) is a local or URL download. Rollback still depends on the IDF bootloader and on a partition scheme with two OTA slots. Several Arduino partition schemes have one slot. That is another reason the product firmware is ESP-IDF.

Firmware is hosted by the Stockbot server and fetched by the device. The device does not need CJ's Cloudflare account, a tunnel, or an inbound port.

## BOM, enclosure, power

**Verified prices**, 16 October 2026:

| Part | Price | Source |
| --- | --- | --- |
| ESP32-S3-WROOM-1-N8 module | $5.49 each, $4.14 at 100, $3.52 at 1,950 | [DigiKey](https://www.digikey.com/en/products/detail/espressif-systems/ESP32-S3-WROOM-1-N8/15200089) |
| ESP32-S3-WROOM-1U-N8R8 (external antenna, 8 MB PSRAM) | $5.76 each, $3.67 at 1,300 | [LCSC C2980300](https://www.lcsc.com/product-image/C2980300.html) |
| ESP32-S3-DevKitC-1 class board | about $19 retail | [Adafruit 5336](https://www.adafruit.com/product/5336) $19.95, out of stock; Amazon listings around $19 |
| Espressif ESP32-S3-USB-OTG devkit (USB-A host, 500 mA VBUS) | $35 | [DigiKey / Mouser via Findchips](https://www.findchips.com/detail/ESP32-S3-USB-OTG/Espressif-Inc) |
| ESP32-S3 Super Mini style board | about $4 | marketplace anecdote, not a distributor quote |

PSRAM is optional. USB HID reports and a TLS session fit in internal SRAM for this workload. Buy the N8 module (8 MB flash, no PSRAM), not the N8R8, unless a later feature needs the extra RAM.

**Custom-board BOM, estimate, qty 1–10.** Module $3.50–5.50, USB-A receptacle and USB-C power connector under $1 together, 3.3 V regulator, a VBUS load switch set near 500 mA, one LED, one button, passives, and a small two-layer PCB. Parts land around $8–12 before assembly. At a few hundred, the module discount and a panelized board put the electronics under $8. A generic ABS box is a few dollars at qty 1 and less in bulk. The scanner, dongle, and a USB charger are already in the household. Assembly and a test fixture are extra and are not in the $5–10 chip budget.

**Dev-board prototype, when a purchase is approved.** The $35 USB-OTG devkit is the board that already has a USB-A port and a current limit. A $4 Super Mini plus an OTG adapter looks cheaper and often cannot source VBUS. Spend the prototype money on the board that matches the schematic, not on the smallest module.

**Power. Verified** from the ESP32-S3-WROOM-1 datasheet, 3.3 V, 25 °C, peak, 100% duty: Wi-Fi TX is 355 mA (802.11b, 1 Mbps, 20.5 dBm), 297 mA (802.11g, 54 Mbps), 286 mA (802.11n HT20 MCS7). RX is about 95 mA. Those are peaks. A relay that posts occasionally and otherwise sits in modem-sleep is far below that. The WROOM-2 datasheet's modem-sleep figures, same family, are on the order of 13–40 mA depending on CPU clock. A 5 V USB charger covers the peak including regulator loss. This is a plugged-in appliance, like the Pi, with a smaller supply. Do not battery-power the relay. The scanner already has the battery.

**Enclosure.** A small box, USB-C power in, USB-A for the dongle, one status LED (pairing, joined, posting, error), one recessed button (hold to forget Wi-Fi and the token). No screen. The dongle can live in the box. The scanner stays in the hand or on its stand and talks to the dongle over 2.4 GHz, which the listing rates at tens of meters indoors. The relay sits by an outlet, not in the scanner shell.

**Hands-on.** Measure dongle current from VBUS, and cable-mode charge current if wired mode is still interesting. Confirm the 2.4 GHz link across the actual pantry, including the refrigerator and the box.

## Fallback order

1. **ESP32-S3 plus the 2.4 GHz dongle**, USB HID host, ESP-IDF. This is the recommendation.
2. Same board, USB cable, only after measuring charge current against the VBUS limit.
3. **Original ESP32**, ESP-IDF `esp_hid_host` in Classic mode, only if the dongle is not a USB keyboard and `bluetoothctl` shows the Pi's link is BR/EDR. This chip has no USB host, so there is no second radio path on the same board. Budget a hands-on test of reconnect after the scanner sleeps, and of free heap with Wi-Fi and TLS running.
4. **ESP32-S3 as a BLE HID host**, same `esp_hid_host` example in its S3 sdkconfig, only if a setup barcode puts this unit into a mode that advertises HID-over-GATT and a phone receives keystrokes from that mode. The family manuals point phones at HID mode instead, so this is the surprise outcome, not the plan.

C3 and C6 lose on purpose. They are BLE only, they cannot host USB, and they are smaller than this job needs once the dongle is the scanner interface. They become interesting only if step 4 is the one that works, because a BLE HID host does not need the S3's USB controller. Even then the S3 is the board already on the bench.

## Open questions only a hands-on test can answer

1. Label model, and which protocol page is in this unit's booklet. Is the live Pi session BR/EDR HID or LE HID (`bluetoothctl info`)?
2. Dongle `lsusb` identity and boot-protocol bit. Does a scan arrive as characters plus Enter?
3. On an S3 host with a real VBUS switch, do those same characters arrive, and does the link survive the scanner's sleep and wake?
4. Dongle VBUS current, and cable-mode charge current.
5. Does the scanner's BLE setup barcode advertise service `0x1812` and type as a keyboard? If yes, the S3 BLE host becomes a real alternative. If the phone cannot see it as a keyboard, ignore BLE for this product.
6. Captive portal on a current iPhone and a current Android phone. Does the page appear, and does the provisioning QR path work when it does not?
7. Classic HID host on an original ESP32, only if questions 2–3 fail: pair, reconnect after scanner sleep, decode the report, and free heap with Wi-Fi plus TLS.
8. Range from the outlet where the box would sit to the shelves where people scan, including the door of the refrigerator.

## Untested sketch

This is a sketch of the data path. It has not been compiled or run. It is not a firmware project and it is not a patch to Stockbot.

```c
/* UNTESTED. Illustrates line assembly and the POST body.
   Not built, not flashed, not wired to esp_http_client. */

static char line[256];
static size_t len;

void on_key(char c) {
    if (c == '\r' || c == '\n') {
        if (len == 0) return;
        line[len] = 0;
        if (is_control(line)) {          /* STOCK_IN / STOCK_OUT from pair */
            set_mode_from_control(line); /* also arms the 5-minute idle */
        } else {
            append_buffer(new_id(), line, now_iso(), mode_now());
            flush_when_online();         /* POST /api/device/scans, drop on 2xx */
        }
        len = 0;
        return;
    }
    if (len + 1 < sizeof line) line[len++] = c;
}
```

The POST body the buffer would send:

```json
{
  "scans": [
    {
      "id": "01JABCDEF",
      "barcode": "012345678905",
      "scannedAt": "2026-10-16T03:00:00Z",
      "direction": "out"
    }
  ]
}
```

## Sources

Scanner:

- Amazon listing ASIN B01M264K5L, model MJ-2030-B, retrieved 16 October 2026. https://www.amazon.com/dp/B01M264K5L
- Symcode Bluetooth/2.4G manual (2877), HID / SPP / BLE setup barcodes and Bluetooth 4.1. https://symcode.cn/public/upload/2877%2074x118mm%20EN.pdf
- Symcode BT30 manual, same three protocols, HID mode required for phone and PC search. https://barcode.com.sg/wp-content/uploads/2020/07/UserManual-BT30.pdf
- Symcode R30, 1932DB, and R50 manuals, same radio description. https://www.symcode.cn/public/upload/R30%2074x110mm%20EN.pdf https://www.symcode.cn/public/upload/1932DB%20EN%2088x140mm.pdf https://manualzz.com/doc/70848436/symcode-r50-user-manual
- This repo: `deploy/scan-input.md`, `internal/scanlistener/listener.go`, `internal/server/handler_scan_create.go`, `internal/server/handler_scanner.go`, `internal/scan/scan.go` (`CreateScanEntry` merge).

Chips, host stacks, USB, OTA, provisioning:

- ESP-IDF v6.1 Bluetooth architecture overview, Classic vs LE by chip. https://docs.espressif.com/projects/esp-idf/en/stable/esp32/api-guides/bt-architecture/overview.html
- ESP-IDF v6.1 Bluetooth HID Host API and the `esp_hid_host` example. https://docs.espressif.com/projects/esp-idf/en/stable/esp32/api-reference/bluetooth/esp_hidh.html
- ESP-IDF Classic Bluetooth profile list, including HID host. https://docs.espressif.com/projects/esp-idf/en/latest/esp32/api-reference/bluetooth/classic_bt.html
- Espressif reply on esp-idf#12400: ESP32-S3 has no Classic Bluetooth. https://github.com/espressif/esp-idf/issues/12400
- Arduino-ESP32 `BluetoothSerial` (Classic SPP) and the note on including `esp32-hal-alloc-bt-classic-mem.h` before calling IDF Bluetooth APIs. https://github.com/espressif/arduino-esp32/blob/master/libraries/BluetoothSerial/README.md https://docs.espressif.com/projects/arduino-esp32/en/latest/api/bluetooth.html
- ESP-FAQ, USB host on S2/S3 and not on C3. https://docs.espressif.com/projects/esp-faq/en/latest/software-framework/peripherals/usb.html
- ESP-IoT-Solution USB peripheral table (OTG vs USB-Serial-JTAG). https://docs.espressif.com/projects/esp-iot-solution/en/release-v2.0/usb/usb_overview/usb_overview.html
- ESP-IDF USB HID host example README. https://github.com/espressif/esp-idf/blob/master/examples/peripherals/usb/host/hid/README.md
- ESP32-S3 USB host guide, VBUS must be supplied and current-limited in hardware. https://docs.espressif.com/projects/esp-usb/en/latest/esp32s3/usb_host.html
- ESP32-S3-USB-OTG schematic, USB-A VBUS at a 500 mA limit. https://dl.espressif.com/dl/schematics/SCH_ESP32-S3_USB_OTG.pdf
- ESP-IDF captive portal example. https://github.com/espressif/esp-idf/blob/master/examples/protocols/http_server/captive_portal/README.md
- ESP-IDF Wi-Fi provisioning and the softAP QR payload. https://docs.espressif.com/projects/esp-idf/en/stable/esp32/api-reference/provisioning/provisioning.html https://github.com/espressif/esp-idf/tree/master/examples/provisioning
- ESP-IDF OTA, app rollback, anti-rollback. https://docs.espressif.com/projects/esp-idf/en/stable/esp32/api-reference/system/ota.html
- ESP32-S3-WROOM-1 datasheet, Wi-Fi current table. https://documentation.espressif.com/esp32-s3-wroom-1_wroom-1u_datasheet_en.pdf
- Module and board prices cited inline above (DigiKey, LCSC, Adafruit, Findchips), 16 October 2026.
