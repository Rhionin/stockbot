# ADR-003: One Expo app for web and Android (phase 4a spike result)

- Status: Proposed. CJ decided the direction on 2026-11-24. This records the spike result he asked for before any screen is ported.
- Date: 2026-11-24
- Owner: Stockbot Architect
- Supersedes: the step 8 plan in [ADR-002](adr-002-tamagui-ui-library.md) (Tamagui components inside the Vite app). ADR-002's library choice and pins stand.
- Evidence: spike branch `ccr-c57118aa-frov1n`, draft PR [#753](https://github.com/Rhionin/stockbot/pull/753). Plan: [android-readiness-plan.md](android-readiness-plan.md), phase 4.

## Context

The web (React, Vite, Mantine, about 40 screens) and the phone (Expo, 3 screens) are two UIs. CJ decided to build every screen once: `mobile/` becomes the only UI and serves the website through Expo Router's web output (React Native Web). The Mantine `frontend/` is retired screen by screen. The risks the plan named were bundle size and runtime cost, Tamagui extraction (it failed under Vite 8 in ADR-002), cookie sessions on web, camera scanning in browsers, accessibility, and desktop layout. The fallback if the spike failed was ADR-002 step 8.

## What the spike built

- A web target for `mobile/`: `react-dom` 19.3.1 (matching React exactly), `react-native-web` 0.21.4 (the version Tamagui already resolved) and `@expo/metro-runtime` ~58.0.17. `web.output` is `"single"` (a SPA, because the Go server serves an index fallback). `npm run build:web` runs `expo export -p web` into `mobile/dist`, which is gitignored. Expo, React Native, React and Tamagui pins are unchanged.
- Cookie sessions on web, Bearer on native:
  - `auth/sessionMode.ts` and `sessionMode.web.ts` choose the mode and the page origin by platform file. `auth/secureStore.ts` has a no-op `secureStore.web.ts` beside it, so the web bundle never contains `expo-secure-store` (checked in the export).
  - The client sends `credentials: 'same-origin'` and no `Authorization` or `X-Stockbot-Client` header on web. The server then treats the client as non-Expo, sets the HttpOnly cookie and omits the token from the body, and the browser's own `Origin` header satisfies `csrfOK`. A test in `client-test.ts` covers the web headers on the native Jest run.
  - The server field is hidden on web. The session boots from `GET /api/auth/session`, skips the Expo version gate, and signs out with `POST /api/auth/logout`.
- A real end-to-end test, `mobile/e2e/web-smoke.spec.ts`. `e2e/web-stack.mjs` boots the real Go server, redeems its setup code, and serves `mobile/dist` from a small same-origin static server that proxies `/api` and `/health` (the way Vite does in dev). Nothing in `internal/webui`, the Dockerfile or `deploy/` changed. The test signs in with email and password, reaches the queue empty state, checks that the session cookie is HttpOnly and that no `sbs_` token is in `localStorage` or `sessionStorage`, checks that no API request carries `Authorization` or `X-Stockbot-Client`, seeds a product and a scan through the API, and sees the card after a reload. It runs axe on sign-in and on the queue before and after the card exists.
- Two real web bugs found and fixed by the test and screenshots:
  - Screens had no landmarks, and the tab bar had `tab` children without a `tablist` parent. Screens now carry `role="main"`, and the tab bar is a `navigation` with a `tablist`.
  - Tamagui turns a bare numeric `lineHeight` into a unitless CSS value on web, so an 18px label with `lineHeight={24}` was 432px tall and each queue card filled the screen. `ui/src/lineHeight.ts` gives web a px string and native points. This touches `ui/`; the `frontend/` Vitest suite (1,447 tests) and `tsc -b` still pass.

## Results

| Check | Result |
|---|---|
| `expo export -p web` | Pass, about 16 s. 35 files, 2.0 MB. Runs under Node 24 with no network. |
| Initial JS, Expo web (runtime + common + entry; 3 screens) | 1,485 KB raw, 394 KB gzip. First-route chunks add about 20 to 40 KB gzip. All route chunks together are 50 KB gzip. |
| Initial JS and CSS, Vite web today (`npm run build`; about 40 screens) | JS 1,027 KB raw, 306 KB gzip, plus CSS 345 KB raw, 53 KB gzip. Total 1,372 KB raw, 359 KB gzip. A ZXing chunk (452 KB raw, 119 KB gzip) loads only when the camera opens. |
| Comparison | The Expo web baseline is about 10% larger in gzip than the whole web app today, before any screen is ported. The Vite app's own source and glue is about 366 KB raw and the router splits routes, so ported screens should land mostly in lazy route chunks. This is an estimate; the port will measure it per PR. |
| Bundle breakdown (initial, raw, from the Metro source maps) | `expo-router` with its vendored React Navigation 462 KB (33%, plus 19 KB `react-native-screens` and 9 KB safe-area), `react-native-web` 310 KB (21%), Tamagui 287 KB (20%: 196 KB `@tamagui/web`, 52 KB `style-grammar`), `react-dom` 204 KB, app source 54 KB, Expo modules 53 KB. |
| Tamagui extraction under Metro web | Partial, no gain. `@tamagui/babel-plugin` has no 3.x release (the last is 2.7.7). The Metro path in 3.3.3 is `@tamagui/metro-plugin`'s `withTamagui`. It loaded and exported with no error. Its plans lowered 6 of 18 Tamagui elements (in `QueueCard` and `NoticeBanner`; the rest bailed on dynamic values), but the structural pass is named `web-noop-v1` and the output was byte-identical (same bundle hashes, within 0.1 KB). It was reverted: no plugin, config or lockfile change is in the PR. |
| Sign-in and cookie session on web | Pass. No token in JS storage, no `Authorization` or client header, HttpOnly cookie, same-origin. |
| End-to-end on the real Go server (`npm run e2e:web`) | Pass, about 6 s. |
| axe on sign-in, scan, queue (empty and with cards), household, at 1280 and 390 px | 0 violations after the landmark and `tablist` fixes. Before them: `region` on both pages and `aria-required-parent` on the tabs. |
| Camera on web | `expo-camera` 58 supports web barcode scanning (see below). Decoding was not exercised. A fake camera stream renders on the Scan tab. |
| Native regression | `typecheck`, `lint` and Jest pass (23 suites, 345 tests). `frontend`: Vitest 130 files, 1,447 tests, and `tsc -b` pass. |

### Camera

`expo-camera` 58 has a web implementation (`ExpoCameraManager.web`, `useWebBarcodeScanner`). `CameraView`, `useCameraPermissions`, `barcodeScannerSettings` and `onBarcodeScanned`, the calls `ScanScreen` already makes, work in the browser, so no `ScanScreen.web.tsx` is needed for camera scanning. It uses the browser's `BarcodeDetector` where it exists (Chrome on Android) and lazily loads the `barcode-detector` package (ZXing as WASM) elsewhere. The current web uses the native detector, then `@zxing/library`.

Two things it does not cover:

- The fallback fetches its WASM from `fastly.jsdelivr.net` at run time. That is an unpinned third-party fetch at scan time, it breaks offline, and it may break under a strict content security policy. It should be self-hosted (`setZXingModuleOverrides` with a `locateFile`) before the web swap.
- The current web also takes input from USB and Bluetooth keyboard-wedge scanners (`BarcodeInputField`). `ScanScreen` has no text path. The web needs a `ScanScreen.web.tsx` (a focused hidden input plus an opt-in camera) before the scan screen replaces the old one. This is not small, because it needs the same focus and paused-camera rules as the current `CameraScanner`, so it was not built here.

The Expo docs for SDK 58 were not reachable (the proxy blocks `docs.expo.dev`), so this is from the installed package source. A real decode needs a browser with `BarcodeDetector`, or the WASM served locally.

### Desktop layout

At 1280 px the phone layouts stretch or sit in the corner. At 390 px they match the phone.

- Sign-in and Household: a left-aligned column about 480 px wide, with 60% of the page empty.
- Queue: full-width rows with the Confirm button at the far right edge, 1,200 px from the name. It needs a centered column (about 720 px) or a master-detail layout.
- Scan: the camera fills the full width and height. It needs a bounded stage.
- Navigation: the bottom tab bar spans the page. Desktop wants a top bar or side navigation, with breakpoints in Tamagui (`$gtMd`).
- Web-only copy and rows: "Scan the pantry from the phone." on sign-in, the Server row on Household (it shows the page origin), and the build stamp ("Update embedded", "Runtime unavailable") are wrong or meaningless on web. Scan is the index route, so the web lands on the camera. The web's default landing should be the queue.

## Decision

**Go.** One Expo app can serve the website and Android. Cookie auth, the real server, axe and the bundle all work with the pinned versions and no package patches. The cost is about 394 KB gzip of framework before any ported screen, and the fixed costs are `expo-router`, `react-native-web` and the Tamagui runtime. The measurements do not show a blocker.

Fallback, if a later step fails its condition below: ADR-002 step 8 (Tamagui components inside the Vite app), still with no duplicated screens.

## Conditions on later steps

- **Serving change, as its own ticket (owner: Network Engineer).** Do this before the first ported screen replaces a live web route.
  - The Dockerfile builds `mobile/` (Node 24, `npm ci`, `npm run build:web`) with `shared/` and `ui/` copied, and `internal/webui` embeds `mobile/dist` in place of `frontend/dist`.
  - `internal/webui` caches `assets/` as immutable and everything else as `no-cache`. Expo's hashed files are under `_expo/static/`, so that prefix needs the immutable rule. Fonts and images under `assets/` already match.
  - `frontend/public` files (`sw.js`, `site.webmanifest`, icons) move to `mobile/public`. Expo copies `public/` into `dist`. The `/sw.js` scope and cache headers stay as they are.
  - Until the last route moves, ship both: `frontend/` serves the routes not yet ported, `mobile/dist` serves the ported ones, and the Go handler picks by path. Do not change `internal/webui` until that ticket.
- **Pins stay exact.** `react-dom` follows `react` exactly. `react-native-web` stays at Tamagui's resolved version. Any Expo, React Native, React or Tamagui bump needs architecture review.
- **Playwright pins.** `mobile/` pins `@playwright/test` 1.63.0 and overrides `playwright-core` to match the Chromium build installed in the Cloud Agent image (the Vite app's e2e uses 1.65.1). Align both when the image's browsers move. Wire `e2e:web` into CI (`npx playwright install chromium`, then `npm run e2e:web`) with the first port PR.
- **Each port PR** keeps `shared/` copy and contract, builds components in `ui/`, moves its Vitest tests to jest-expo real-render tests, and adds or keeps Playwright and axe coverage against the web export. A new web test asserts layout bounds the way the queue card test does, because jsdom and jest-expo do not catch the `lineHeight` class of bug.
- **Responsive work comes first, in its own PR** (before step 2 below): a breakpoint-aware shell (top or side navigation on desktop), a centered content column, and a bounded scan stage, plus the web-only copy and the queue as the web landing page. Without it, step 1 would make the desktop page worse than today's.
- **Scan on web (step 1)** needs the self-hosted ZXing WASM and the keyboard-wedge input described above.
- **Tamagui compiler.** Do not count on extraction under Metro web in 3.3.3. Recheck on each Tamagui upgrade. The runtime is 20% of the bundle either way.
- **No token in JS storage on web.** Native keeps Bearer in SecureStore. A reviewer should reject a web path that reads or writes `sbs_` values.

## Port order

Same as the plan's 4b, with the shell first:

1. Responsive shell and web-only copy (above), then the scan queue as the web's queue. Delete `ScanQueuePage` and `ScanEntryCard` when it ships.
2. Inventory list and item detail, with stock in and out.
3. Shopping list, including the Kroger cart send and provision results.
4. Restock targets, categories and suggestions.
5. Groups, inbox and suggestions.
6. History, ledger and the pulse chart (needs a cross-platform chart library, or a `.web.tsx` variant).
7. Household, households, members, invites, devices, pairing, settings and diagnostics.
8. Kroger app setup, print kit and setup sheet. Print and QR stay `.web.tsx`.
9. Web push stays web-only (service worker). Native push is still #241.

When the last route moves, delete `frontend/` and update CI and the Dockerfile.

## Consequences

- Positive: every screen is built once. Web gets the same sign-in, queue and tokens as the phone. The e2e harness runs the real server against the real export, and axe runs on the same code that ships.
- Negative: the web carries React Native Web and the Tamagui runtime, and starts about 35 KB gzip heavier than today's whole app, with a first-visit cost on slow connections. Layout work is real: every desktop screen needs a responsive pass. The camera fallback needs a self-hosted WASM, and keyboard-wedge scanning needs a web variant. Two bugs (landmarks and `lineHeight`) show that web-only rendering problems will not appear in the native tests.
- Opinion: the bundle number is the one to watch. The spike measures a 3-screen app, and the ported screens will be lazy route chunks, so the initial cost should stay near the baseline. If the first three ports push the initial gzip over about 500 KB or the first meaningful paint on a mid phone is poor, stop and take the fallback to CJ as a decision.
