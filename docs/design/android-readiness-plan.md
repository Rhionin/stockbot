# Android app readiness: effort and plan

- Status: Draft for CJ
- Date: 2026-11-24
- Scope: get the Expo phone app (`mobile/`) into family hands on Android, then onto Google Play. iOS comes later.

## Where things stand

| Area | State |
|---|---|
| Phone app | `mobile/` is Expo SDK 58 / React Native 0.88 with Expo Router. It has three tabs: Scan (camera barcodes, scan mode), Queue (confirm one or all, backfill, link suggestions) and Household (server and password). About 1.7k lines of screens. Mobile CI runs typecheck, lint and Jest. |
| Shared code | `shared/` (contract, types, brand tokens, copy) is used by web and phone. `ui/` (`@stockbot/ui`, Tamagui 3.3.x) has `NoticeBanner` and `QueueCard`, and only the phone uses it. That is unification step 7, done in #595/#597. |
| Web app | React + Vite + Mantine, about 15k lines of screens: inventory, groups, history, ledger, shopping list, restock, household admin, Kroger, diagnostics and the product editor. Step 8 (web onto Tamagui) is still conditional (ADR-002). |
| **Server auth** | Stage 1 personal accounts are live. `internal/server` has no Basic auth left (phase C cleanup). Expo clients get a Bearer `sbs_` token from `POST /api/auth/login` when they send `X-Stockbot-Client: expo/<semver>`. Old clients get `426 upgrade_required` below `minClientVersion.expo`. |
| **Phone auth** | Still sends `Authorization: Basic pantry:<household password>` (`mobile/src/api/client.ts`) with no `X-Stockbot-Client` header. **The phone cannot sign in to a current server.** #384 (S11) covers the fix and is open. |
| Standalone build | #243 is open. PR #246 (`cursor/android-eas-preview-4a2c`) has `eas.json`, `app.config.ts`, a fingerprint runtime, a build stamp and an EAS Update workflow. It targets the old #230 branch, is unmerged and stale. No Expo project or EAS account exists yet. |

## Work, in order

Sizes are for one builder agent plus review. "PR" means one reviewed PR.

### Phase 1: phone sign-in on Stage 1 (#384 S11). Blocker. About 1–2 PRs, 2–4 days.
- Remove the Basic `pantry` path and `HOUSEHOLD_USERNAME`.
- Sign in with email and password via `POST /api/auth/login`. Keep the Bearer `sbs_` token in SecureStore. Sign out with `POST /api/auth/logout`.
- Send `X-Stockbot-Client: expo/<app version>` and `Authorization: Bearer sbs_…` on every request.
- Any `401` (except the sign-in route) clears the token and returns to sign-in.
- `426 upgrade_required` and a too-old `minClientVersion.expo` from `GET /api/config` show "Update Stockbot to keep using it." and never loop back to sign-in.
- A LAN `entry` from `GET /api/config` sends sign-in to `appBaseUrl`.
- Household switcher when the account has more than one membership (`POST /api/auth/session/household`). Clear queue and scan state on any account or household change (fixes #645).
- Deferred to a follow-up: device pairing from the phone and invite acceptance (both can stay on the web for now).

### Phase 2: standalone Android preview build (#243, port of PR #246). About 1 PR, 1–2 days of code, then CJ's account steps.
- Rebase PR #246's content onto master after phase 1: `eas.json` (preview APK internal, production AAB), `app.config.ts`, fingerprint `runtimeVersion`, remote `versionCode` autoincrement, cleartext off in release, `expo-updates`, the build stamp on Household, and the manual `mobile-eas-update.yml`.
- Drop PR #246's household-password parts. Phase 1 replaces them.
- **CJ only:** create the Expo account, run `eas init` (writes `owner` and `projectId`), add the `EXPO_TOKEN` secret, and run the first `eas build -p android --profile preview`. Nobody else can do this, and it can't be done from CI without the token.
- On-device smoke test: install, sign in, scan in, scan out, confirm, get an OTA update. That also covers ADR-002's open "device check" for the Tamagui banner and queue card.

### Phase 3: phone correctness gaps already filed. About 3–4 PRs, 1–2 weeks.
- #659 live updates: queue and scan mode follow web changes. RN `fetch` can't stream SSE. Use an XHR-based SSE reader (pure JS, works in Expo Go) with a polling fallback.
- #648: don't say "Used" or "Added" before confirming, and let people confirm from the Scan tab.
- #650 record a use or put-away without a scanner. #244 name unknown and flagged products from the phone.
- #373/#408 offline sentences already exist. Check them on a device.

### Phase 4: one app for web and Android. Large: about 15–25 PRs, 8–14 weeks.
CJ decided (2026-11-24) to get the web onto React Native before adding phone features, so that no screen exists twice. The recommended form is **one Expo app**: `mobile/` grows into the only UI and serves the website through Expo Router's web output (React Native Web). The Mantine `frontend/` is retired screen by screen. This goes further than ADR-002's step 8, which kept two apps (Vite web and Expo phone) that only share `ui/` components. An ADR-003 records the change.

**4a. Spike (1 PR, about 3–5 days). Go/no-go before porting anything.**
- `npx expo export -p web` of today's phone app, served by the Go server in place of `frontend/dist`. The Dockerfile copies `mobile/`, `ui/` and `shared/`.
- Measure the initial web bundle against today's Vite build (#634). Check whether Tamagui extraction works under Metro web, where it failed under Vite 8.
- Web sign-in uses the cookie session plus CSRF, not Bearer. `client.ts` picks the mode by platform.
- Camera scanning on web: check `expo-camera` barcode support in mobile browsers against the current web `CameraScanner`. Keep a `.web.tsx` variant if needed.
- Run one existing Playwright e2e test against the Expo web build.
- Fallback if it fails: ADR-002 step 8 (Tamagui components in the Vite app), still with no duplicated screens.
- **Outcome (2026-11-24): go.** See [ADR-003](adr-003-one-expo-app-for-web-and-android.md). `expo export -p web` works with the pinned versions. Cookie sessions work on web with no token in JS storage. A Playwright test (`npm run e2e:web` in `mobile/`) signs in against the real Go server and passes axe. The initial bundle is 394 KB gzip for 3 screens, against 359 KB for the whole Vite app. Tamagui extraction under Metro web does nothing in 3.3.3. `expo-camera` scans on web, but its ZXing fallback loads WASM from a CDN, and keyboard-wedge scanners need a web screen. Desktop layouts need a responsive shell before step 1 of 4b.

**4b. Port the web screens into the one app (about 12–20 PRs).** Order by daily use, and keep each web route live until its port replaces it:
1. Scan queue (already on the phone). Make it the web's queue too, and delete `ScanQueuePage`/`ScanEntryCard`.
2. Inventory list and item detail (#699, #700), with stock in and out.
3. Shopping list, including the Kroger cart send and provision results.
4. Restock targets, categories and suggestions.
5. Groups, inbox and suggestions.
6. History, ledger and pulse chart. The chart needs a cross-platform library, such as `react-native-svg` plus `victory-native`, or keep a `.web.tsx` variant.
7. Household, households, members, invites, devices, pairing, settings and diagnostics.
8. Kroger app setup, print kit and setup sheet. Print and QR stay web-only `.web.tsx` files.
9. Web push stays web-only (service worker). Native push is still #241.

Each port reuses `shared/` contract and copy, builds components in `ui/` (Tamagui), moves its Vitest tests to jest-expo real-render tests, and keeps or rewrites the Playwright and axe coverage against the web export. When the last route moves, delete `frontend/` and update CI and the Dockerfile.

**Risks:** the web bundle size and runtime cost (ADR-002 measured about 53 KB gzip for Tamagui alone). Desktop layouts need responsive work, because the phone screens are phone-first. Accessibility on React Native Web must keep the current axe results.

### Phase 5: Google Play release. Deferred: CJ decided sideloading the APK is fine at first. About 1 PR plus 2–3 weeks of calendar time when it starts.
- Google Play developer account ($25, identity verification). It is in CJ's name.
- **New personal developer accounts must run a closed test with at least 12 testers for 14 continuous days before production access.** Start it as soon as the phase 2 build works, because it is the longest wait on the critical path.
- Store listing, privacy policy URL, Data safety form (camera; account email; no ads), content rating, and target API level as Expo SDK 58 sets it.
- `eas submit -p android --profile production` with a Play service-account key stored as an EAS secret. Raise `minClientVersion.expo` only after the family is on the store build.
- Native push (#241) stays deferred. It needs FCM credentials and an `expo-notifications` dev build.

### Not Android-specific, but on the path
- Node 24 for `mobile/` (`.nvmrc`). The Cloud Agent image and some containers ship Node 22.
- Keep pins exact (ADR-002). Any Expo, RN, React or Tamagui bump needs architecture review.
- Open Dependabot bumps in `/mobile` (#705, #334) should wait until phase 2 is on a device.

## Totals

| Milestone | Effort | Calendar |
|---|---|---|
| Family can install and use the scan loop on Android (phases 1–2) | 2–3 PRs, about 1 week | plus CJ's Expo setup |
| Phone is a good scan-and-confirm app (plus phase 3) | 5–7 PRs, about 2–3 weeks | |
| One app serves web and Android, and `frontend/` is deleted (plus phase 4) | 20–32 PRs, about 10–17 weeks | |
| On Google Play production (phase 5, deferred) | 1 PR plus forms | at least 14 days of closed testing |

Phase 3 work lands once in the shared app, so it can run alongside 4b. Phase 3 items that touch web screens wait for their port.

## Decisions
1. Expo account: probably `rhionin` (CJ, 2026-11-24, not final). CJ confirms when running `eas init`.
2. **Web first, one implementation** (CJ, 2026-11-24): move the web onto React Native before adding phone features, so that no screen is built twice. Phase 4 above. It needs ADR-003, superseding ADR-002's step 8 plan.
3. **Sideloaded APK plus OTA updates is enough for now** (CJ, 2026-11-24). Google Play (phase 5) is deferred.
