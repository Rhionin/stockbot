# Stockbot for Expo Go

The phone app scans groceries into the household pantry, then confirms the waiting scans. It talks to the same API as the web app. Sign-in is a personal account (email and password). The server address and the session token stay in the device secure store. The password is never saved.

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
3. On the Stockbot screen, leave the server as `https://stockbot.rhionin.com` or type another address, including `https://` or `http://`. A pantry on your LAN can be `http://192.168.1.20:8080`; the server then points sign-in at its public address.
4. Enter your email and password and press Sign in. The app sends `POST /api/auth/login` with the header `X-Stockbot-Client: expo/<version>`, and the server answers with a session token. The app stores the address and token, and later requests send `Authorization: Bearer <token>`. A `401` from any other request signs the phone out. A `426` or a `minClientVersion.expo` above the app version shows "Update Stockbot to keep using it." instead.
5. Allow the camera when the scan screen asks. Point it at a package barcode. Confirm the scan on the Queue tab.

The dev server is only for the JavaScript bundle. The pantry API is the server address you saved, so the phone does not need the Go server running on your computer unless that is the address you entered.

## What the three tabs do

- Scan reads barcodes with the camera, in the formats the web scanner uses. A repeat of the same code within two seconds is ignored. `STOCK_IN` and `STOCK_OUT` switch the shared scan mode instead of creating a scan. Putting away and Using up call `POST /api/scanner/mode`.
- Queue lists pending scans and confirms one or all of them.
- Household shows the signed-in account and household, switches household when the account belongs to more than one, and signs out. Signing out or switching household clears the queue and scan screens so nothing from the previous household shows.

## Checks

```bash
npm run typecheck
npm run lint
npm test
```
