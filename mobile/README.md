# Stockbot for Expo Go

The phone app scans groceries into the household pantry, then confirms the waiting scans. It talks to the same API as the web app. The server address and household password stay in the device secure store.

This project is Expo SDK 58 (React Native 0.88). Expo Go has to be the SDK 58 build. The App Store and Play Store copies of Expo Go can lag a new SDK by a few weeks. If Expo Go says the project is incompatible, install the SDK 58 build described below instead of upgrading the app code.

## Run it

From the repository root:

```bash
cd mobile
npm ci
npx expo start
```

Node 24 matches `mobile/.nvmrc`.

Then open the project in Expo Go:

1. Install Expo Go for SDK 58.
   - Android: install the Expo Go 58 APK from [expo/expo-go-releases](https://github.com/expo/expo-go-releases/releases), or follow the link `npx expo start` prints when the installed Expo Go is older.
   - iOS Simulator: press `i` in the Expo CLI. It installs a matching Expo Go.
   - iPhone: until the App Store build is SDK 58, install Expo Go 58 from [expo.dev/go](https://expo.dev/go).
2. Start the dev server with `npx expo start` and scan the QR code (Android Expo Go, or the iPhone camera, which hands off to Expo Go). The phone and the computer need to be on the same network.
3. On the Stockbot screen, leave the server as `https://stockbot.rhionin.com` or type another address, including `https://` or `http://`. A pantry on your LAN can be `http://192.168.1.20:8080`.
4. Enter the household password and press Connect. The app checks `GET /api/session` and then stores the address and password. Later requests send them as HTTP Basic auth with the username `pantry`.
5. Allow the camera when the scan screen asks. Point it at a package barcode. Confirm the scan on the Queue tab.

The dev server is only for the JavaScript bundle. The pantry API is the server address you saved, so the phone does not need the Go server running on your computer unless that is the address you entered.

## What the three tabs do

- Scan reads barcodes with the camera, in the formats the web scanner uses. A repeat of the same code within two seconds is ignored. `STOCK_IN` and `STOCK_OUT` switch the shared scan mode instead of creating a scan. Putting away and Using up call `POST /api/scanner/mode`.
- Queue lists pending scans and confirms one or all of them.
- Household changes the saved server and password, or disconnects.

## Checks

```bash
npm run typecheck
npm run lint
npm test
```
