# Ticket draft: serve the Expo web build from the Go server (ADR-003 precondition for Q10)

- Status: Draft. To be filed as an issue for the Network Engineer, since builders don't edit deploy files.
- Date: 2026-11-24
- Unblocks: queue port Q10 (`docs/design/queue-port-parity.md`), meaning the web's `/` route serves the Expo queue and `ScanQueuePage` is deleted. Every later screen port depends on it too.

## Goal

The Go server serves `mobile/dist` (`npm run build:web`) for routes that are ported, and `frontend/dist` for the rest, until the last route moves. No user-visible change ships with this ticket alone: the route map starts empty.

## Changes

1. **Dockerfile:** add a `mobile-web` build stage on Node 24.
   - It copies `shared/`, `ui/` and `mobile/`, then runs `npm ci` and `npm run build:web` from `mobile/`.
   - `build:web` copies the self-hosted ZXing WASM to `public/zxing/` and fails if its hash changes.
   - `mobile/postinstall` links `ui/` node_modules (`scripts/link-ui-node-modules.cjs`), so `ui/` must be present before `npm ci`.
   - The final image gets `mobile/dist` next to the current `frontend/dist`. Example: `internal/webui/assets/` stays for the Vite app, and a new `internal/webui/expo/` holds the Expo export, both embedded.
2. **`internal/webui`:**
   - **Router:** a path-prefix map from route to bundle, starting empty. The Expo bundle owns `/_expo/` and `/zxing/`. SPA fallback goes to the owning bundle's `index.html`.
   - **Cache:** `/_expo/static/**` is hashed, so it gets `immutable`. Everything else gets `no-cache`, the same as the rule that exists for `assets/`. `/zxing/zxing_reader.wasm` gets `no-cache`, or `immutable` behind a versioned path.
   - **MIME:** `.wasm` is served as `application/wasm`. Register it in `init()` as is done for fonts, because distroless images have no `/etc/mime.types`. Without it, `WebAssembly.instantiateStreaming` fails.
   - **Service worker:** `sw.js`, `site.webmanifest` and the icons stay where they are (served from `frontend/public`) until web push moves. Their scope and headers are unchanged.
3. **CI:** `ci.yml` "Build and push container image" builds the new stage. `mobile.yml` already runs `e2e:web` against the real server, with a static proxy in `mobile/e2e/web-stack.mjs`. Once routing exists, add one e2e that runs against the Go server itself serving `mobile/dist`.
4. **Tests:** add `internal/webui` tests for each of these:
   - bundle selection by prefix
   - SPA fallback per bundle
   - cache headers for `/_expo/static/`
   - `application/wasm`
   - a missing Expo bundle when the map is empty (no 500s)

## Then, in Q10 (builder)
- Map `/` to the Expo bundle.
- Move the queue Playwright specs from `frontend/e2e` to `mobile/e2e`.
- Delete `ScanQueuePage`, `ScanEntryCard` and the queue components nothing else imports.

## Risks
- Two embedded bundles grow the image by about 2 MB until `frontend/` is deleted.
- Cookies and CSRF are unchanged, because both bundles are same-origin. The Expo web build already uses the cookie session (ADR-003).
