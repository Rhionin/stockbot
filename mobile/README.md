# Stockbot mobile

The phone app scans groceries into the household pantry, then confirms the waiting scans. It talks to the same API as the web app. A fresh install already points at `https://stockbot.rhionin.com`. The household password is the only thing a tester types. The address and password stay in the device secure store.

The release Android app allows HTTPS only. An `http://` address is rejected, and the Android manifest sets `usesCleartextTraffic` to false.

This project is Expo SDK 58 (React Native 0.88).

## Before the first build

Two decisions are still open. Do not guess them, and do not commit an Expo account into this repo.

1. Choose the Expo account that will own the project and the Android signing key. From `mobile/`, run `eas init`. That writes `owner` and `extra.eas.projectId` into `app.json`. Both are intentionally unset until then. See the TODO in `app.config.ts`. If `eas init` prints those fields instead of writing them, paste them into `app.json`.
2. Create an Expo access token for that account and add it to GitHub as the `EXPO_TOKEN` repository secret. The token is not stored in the repo.

`eas init` also creates the project on Expo. The Android keystore is created and stored by EAS the first time you build (`credentialsSource` is `remote` in `eas.json`). Do not download that keystore into the repo.

After the project id is in `app.json`, `app.config.ts` sets the EAS Update URL to `https://u.expo.dev/<projectId>` and turns updates on. Until that id exists, updates stay off so a build is not pointed at an account this repo does not know.

iOS and native push are out of scope.

## Install the preview APK

Family testers do not need Expo Go or a computer.

1. Build the preview APK (commands below). When it finishes, the CLI prints an Expo build page. That page has an install button and a QR code. The link is the internal-distribution install page.
2. Text that link to the phone.
3. On the phone, open the link and tap the install button. Android downloads an APK.
4. If Android blocks the install, it says the phone is not allowed to install unknown apps from this source. Tap Settings, turn on Allow from this source for the browser (or the Files app, if that is what opened the APK), go back, and tap Install.
5. Open Stockbot. Leave the server as `https://stockbot.rhionin.com`. Enter the household password and press Connect.
6. Allow the camera when the scan screen asks.

The public pantry signs in as the username `pantry`. The app sends the password as HTTP Basic auth.

## Build and update

From `mobile/`, with the Expo account from `eas init` logged in (`npx eas-cli@latest login`, or `EXPO_TOKEN` set):

```bash
npx eas-cli@latest build -p android --profile preview
npx eas-cli@latest update --channel preview --message "What changed"
```

`eas.json` has two profiles:

- `preview` builds an installable APK (`android.buildType` is `apk`, `distribution` is `internal`) and subscribes it to the `preview` update channel.
- `production` builds an Android App Bundle for a later Play Store upload. It is unused for family testing. Its channel is `production`, so a store build would not take preview updates.

`cli.appVersionSource` is `remote`, and both profiles set `autoIncrement`. EAS stores `versionCode` and bumps it on every build. The number is not in git. The first build starts at 1.

The build worker uses Node 24.22.0. `mobile/.nvmrc` is Node 24.

### Optional GitHub Action

`.github/workflows/mobile-eas-update.yml` runs `eas update --channel preview` only when someone starts it by hand (`workflow_dispatch`). It does not run on push or on a pull request. It reads `EXPO_TOKEN`. Run it from the branch whose native fingerprint matches the APK testers already installed. After this app is on `master`, that branch is `master`.

## How updates reach testers

`runtimeVersion` uses the `fingerprint` policy. Expo hashes the native project (SDK, native modules, config plugins, permissions). An over-the-air update is delivered only when that hash matches the installed APK.

`appVersion` was the other choice. It uses the `version` string in `app.json` (`1.0.0`). A new native module or a camera-permission change that shipped under the same version string would still be offered to the old APK, and the app could crash. Fingerprint moves the runtime version when the native project changes, so a JavaScript update cannot land on an incompatible binary. The cost is that some native config edits need a new APK even when the version string stays `1.0.0`.

JavaScript and asset changes keep the same fingerprint. Publish them with `eas update --channel preview`. The preview APK checks on launch and downloads in the background. The new bundle runs the next time the app is opened from a cold start: fully close Stockbot (swipe it away), open it once so it can download, fully close it again, and open it. The Household screen's update id changes. Testers do not reinstall.

A new APK is required when the fingerprint changes: a new native module, an Expo SDK bump, a permission change, or anything else that changes the native project. `eas update` cannot install that APK. Build again with the preview profile and text the new install link.

The app does not show "A new version is ready. Install it." EAS Update only answers a device with updates for the runtime version that device is already running. A newer APK has a different fingerprint, so the installed app never receives a manifest, an install link, or any other signal that a newer runtime exists. `checkForUpdateAsync` reports "no update" in that case. Showing the prompt would need a server endpoint that lists the latest runtime and the install URL. That endpoint was not added.

The sign-in screen and the Household tab show three lines a tester can read back:

- Version, including the Android `versionCode` once the app is a real build (`1.0.0 (4)`)
- Update id, or `embedded` when the copy inside the APK is what is running
- Runtime, the fingerprint, or `unavailable` in Expo Go

## Smoke test

On a stock Android phone, after the preview build:

- [ ] Open the EAS install link, allow install from this source if Android asks, and install the APK.
- [ ] Open Stockbot, leave the server as `https://stockbot.rhionin.com`, enter only the household password, and connect.
- [ ] Type an `http://` address and confirm the app rejects it without contacting the server.
- [ ] Allow the camera. Scan an item in (putting away).
- [ ] Scan an item out (using up).
- [ ] Open Queue and confirm the waiting scans.
- [ ] On Household, note the version, update id, and runtime.
- [ ] Publish a JavaScript-only update with `eas update --channel preview`. Fully close and reopen Stockbot twice. Household shows a new update id and the same runtime.
- [ ] Change a native setting (or add a native module), publish an update without a new build, and confirm the old APK does not take it. A new preview APK, texted as a new link, is what installs that change.

## Run it in Expo Go

Expo Go has to be the SDK 58 build. The App Store and Play Store copies of Expo Go can lag a new SDK by a few weeks. If Expo Go says the project is incompatible, install the SDK 58 build described below.

From the repository root:

```bash
cd mobile
npm ci
npx expo start
```

Node 24 matches `mobile/.nvmrc`.

1. Install Expo Go for SDK 58.
   - Android: install the Expo Go 58 APK from [expo/expo-go-releases](https://github.com/expo/expo-go-releases/releases), or follow the link `npx expo start` prints when the installed Expo Go is older.
   - iOS Simulator: press `i` in the Expo CLI. It installs a matching Expo Go.
   - iPhone: until the App Store build is SDK 58, install Expo Go 58 from [expo.dev/go](https://expo.dev/go).
2. Start the dev server with `npx expo start` and scan the QR code. The phone and the computer need to be on the same network.
3. Leave the server as `https://stockbot.rhionin.com`, or type another `https://` address. Cleartext `http://` addresses are rejected, including a pantry on the LAN.
4. Enter the household password and press Connect. The app checks `GET /api/session` and then stores the address and password.
5. Allow the camera. Point it at a package barcode. Confirm the scan on the Queue tab.

The dev server is only for the JavaScript bundle. The pantry API is the server address you saved.

Over-the-air updates do not run inside Expo Go. The version line there is the app config version, and the update id stays `embedded`.

## What the three tabs do

- Scan reads barcodes with the camera, in the formats the web scanner uses. A repeat of the same code within two seconds is ignored. `STOCK_IN` and `STOCK_OUT` switch the shared scan mode instead of creating a scan. Putting away and Using up call `POST /api/scanner/mode`.
- Queue lists pending scans and confirms one or all of them.
- Household changes the saved server and password, or disconnects. It also shows the version, update id, and runtime.

## Checks

```bash
npm run typecheck
npm run lint
npm test
```
