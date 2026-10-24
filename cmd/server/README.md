# Stockbot Server

A barcode inventory management server for home pantries, using Open Food Facts
as the product database.

## Quick Start

```bash
# Build and run
go run ./cmd/server

# Server listens on :8080 by default, database is pantry.db
# Visit http://localhost:8080 to access the web UI
```

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `ADDR` | `:8080` | LAN listen address when `LAN_LISTEN_ADDR` is unset |
| `APP_BASE_URL` | empty | Public origin (`https://host`, no path). Empty stays LAN-only and does not bind the public listener. The Pi deployment value is `https://stockbot.rhionin.com`. Invite, reset, and pair links, and the Origin check, use this value |
| `APP_NAME` | `Stockbot` | Product name. A rename is this setting |
| `SESSION_COOKIE_NAME` | `__Host-stockbot_session` | Personal-session cookie. `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`. The `__Host-` prefix is removed when `COOKIE_DOMAIN` is set |
| `COOKIE_DOMAIN` | empty | Host-only when empty. A parent domain lets a later HTTPS hostname share the cookie |
| `EMAIL_FROM` | empty | Sender address. Empty until a mailer exists |
| `LAN_PEOPLE` | `redirect` | `redirect` sends LAN browsers to `APP_BASE_URL` after the household password is retired. `signin` is reserved for a later LAN session |
| `EXPO_MIN_VERSION` | `1.0.0` | Oldest Expo version. `expo/<version>` below it is `426 upgrade_required` |
| `PUBLIC_LISTEN_ADDR` | `:8081` | Public entry. Bound only when `APP_BASE_URL` is set. `off` disables it, and startup then fails if `APP_BASE_URL` is set |
| `LAN_LISTEN_ADDR` | `:8080` | LAN entry. Overrides `ADDR` when set |
| `DB_PATH` | `pantry.db` | SQLite database path |
| `PRODUCT_CACHE_TTL` | `720h` | Product cache TTL before refresh |
| `PRODUCT_MISS_TTL` | `168h` | Miss cache TTL before retry |
| `DISABLE_EXTERNAL_PRODUCT_LOOKUP` | `false` | Disable external API calls. Also keeps product contribution local |
| `PANTRY_RETAILER_API_KEY` | empty | Store price API key. Empty keeps live prices off. A key alone does not fetch prices until a retailer adapter is connected. Sales you note in the app still apply. |
| `PANTRY_RETAILER_API_URL` | empty | Reserved base URL for that adapter |
| `PRODUCT_OPENER_USER_ID` | empty | Optional Product Opener account. Both this and the password must be set before an opted-in product can be sent |
| `PRODUCT_OPENER_PASSWORD` | empty | Password for that account. Never invent one |
| `STOCK_IN_CONTROL_BARCODE` | `STOCK_IN` | Barcode to switch to stock-in mode |
| `STOCK_OUT_CONTROL_BARCODE` | `STOCK_OUT` | Barcode to switch to stock-out mode |
| `HEADLESS_USER_ID` | `user-1` | User ID for headless scan operations |
| `SENTRY_DSN` | empty | Error reporting. Empty disables it and makes no network calls |
| `SENTRY_ENVIRONMENT` | empty | Sent with errors when `SENTRY_DSN` is set. Empty means `production` |
| `SENTRY_TUNNEL_PROJECT_IDS` | empty | Comma-separated project ids the browser tunnel may forward. Empty hides `POST /api/sentry-tunnel` |
| `SENTRY_TUNNEL_PUBLIC_KEYS` | empty | Optional comma-separated DSN public keys for that tunnel. Empty does not check the key |

## Headless Scanner Input

### Local Development (with keyboard)

To test control barcodes and mode switching without a physical scanner:

```bash
SCAN_INPUT=stdin go run ./cmd/server
```

Type a barcode followed by Enter in the terminal. The scanner status at
`GET /health` will show `connected: false` since there's no device.

### Appliance Deployment (with USB scanner)

For Raspberry Pi deployment with a USB barcode scanner:

1. Configure the udev rule at `/etc/udev/rules.d/99-pantry-scanner.rules`
   (see `deploy/udev/99-pantry-scanner.rules` in the repository)

2. In your `.env` file, keep `SCAN_INPUT=device` (default) and set
   `SCANNER_DEVICE=/dev/pantry-scanner`

3. The container will automatically reconnect if the scanner is unplugged
   and replugged

Public HTTPS, for a hostname you own, is an optional step on top of that LAN install. See `deploy/README.md` (Public Internet access). The public site asks for one shared password. A few exact paths stay open: the timing snapshot at `https://<your-host>/api/telemetry`, plus `https://<your-host>/brand/logo.png`, `https://<your-host>/terms`, and `https://<your-host>/privacy`.

On the same LAN as the Pi, that public hostname hangs when the router does not hairpin traffic aimed at its own WAN address. Cellular data is outside that path, so the same URL loads there. From home Wi-Fi, open `http://<pi-ip>:8080` (or `http://pantry.local:8080` after setup publishes it). That LAN listener has no password. Cloudflare Tunnel is the supported way to make the public hostname work on home Wi-Fi without a port forward. Details and the Gryphon steps are in `deploy/README.md`.

### Health Endpoint

The `/health` endpoint includes scanner status when the listener is configured:

```json
{
  "status": "ok",
  "scanner": {
    "source": "device",
    "devicePath": "/dev/pantry-scanner",
    "connected": true,
    "grabbed": true,
    "mode": "stock_in",
    "unmappedKeys": 0,
    "lastScanAt": "2026-01-15T10:30:00Z",
    "lastError": ""
  }
}
```

The `source` field shows whether the listener is reading from `device` (evdev)
or `stdin`. The `connected` field indicates whether the device is currently
accessible (always true for stdin). The `mode` field shows the current
scan direction (`stock_in` or `stock_out`).

## Telemetry

`/health` answers "is the process up, and is the scanner connected?" The
diagnostic snapshot is `GET /api/telemetry`. It stays in memory on this
machine: nothing is exported, and the document does not include barcodes,
product names, or user ids. Query strings are stripped from browser reports.

```bash
curl -s http://localhost:8080/health
curl -s http://localhost:8080/api/telemetry
```

On the public hostname the same snapshot is intentionally unauthenticated. It
has no barcodes, product names, or user ids, so an agent can read it without
the household password:

```bash
curl -s https://stockbot.rhionin.com/api/telemetry
```

`POST /api/telemetry/client` is public on that hostname as well, so the page
can report timings. `GET /api/build` is public too, so an agent can see which
build is running without the household password. `GET /brand/logo.png`,
`GET /terms`, and `GET /privacy` are public because a grocery developer app
stores those URLs and fetches them with no password. `POST /api/sentry-tunnel`
is public so a signed-out page can report a failure; see below. Every other
path still asks for the shared password. The LAN listener on `:8080` is
unchanged: it has no password. The menu's Build section shows the same
version and commit.
Diagnostics renders `pageLoad` for someone at the screen. On a Pi that is
already public, `sudo ./setup.sh` is what loads the Caddyfile exception.

## Running build

`GET /api/build` names the binary. Compare `commit` with `master` to confirm
a deploy. CI stamps the values with `-ldflags` (`COMMIT_HASH`, `COMMIT_TIME`,
`BUILD_TIME`, `VERSION`, and a base64 commit subject). A `go build` with no
flags returns `commit` `unknown` and `version` `dev`.

| Field | Meaning |
| --- | --- |
| `commit` | Full git SHA, or `unknown` |
| `committedAt` | Commit time, ISO 8601. Omitted when unset |
| `builtAt` | Image build time, ISO 8601 UTC. Omitted on a local build |
| `version` | `dev` locally. CI stamps `v<UTC yyyymmdd>-<7 character sha>`, the same formula as the annotated release tag |
| `subject` | Commit subject. Omitted when unset |

```bash
curl -s https://stockbot.rhionin.com/api/build
```

The response is that identity only. It has no environment values, hostnames,
or paths, and `Cache-Control` is `no-store`. Error reports use this same
`commit` string as their release. There is not a second version source.

## Browser error tunnel

`POST /api/sentry-tunnel` forwards a Sentry envelope to the ingest host in
`SENTRY_DSN`. A browser or mobile SDK points its tunnel option at
`/api/sentry-tunnel`, so the app keeps `connect-src 'self'` and does not
call the ingest host itself.

| | |
| --- | --- |
| Method | `POST` |
| Path | `/api/sentry-tunnel` |
| Content-Type | `application/x-sentry-envelope` |
| Body limit | 256 KB. Larger bodies return `413` |
| Rate limit | Per client IP and a global cap. Excess requests return `429` |
| `400` | Missing or wrong content type, an envelope or item header that is not valid (duplicate or differently-cased keys, a blank line between items, or an item length past the body), or an item type other than `event`, `transaction`, `session`, `sessions`, `client_report`, or `attachment` |
| `403` | The envelope DSN host is not the ingest host of `SENTRY_DSN`, its project id is not listed in `SENTRY_TUNNEL_PROJECT_IDS`, or `SENTRY_TUNNEL_PUBLIC_KEYS` is set and does not contain the envelope's public key |
| `404` | `SENTRY_DSN` or `SENTRY_TUNNEL_PROJECT_IDS` is empty |
| `405` | Any method other than `POST` while the tunnel is configured |
| `429` from ingest | That status is passed through, with `Retry-After` and `X-Sentry-Rate-Limits` when ingest sent them |
| `502` | The forward to ingest failed |
| Success | The ingest status, with an empty body |

The route is open before household login, including on the login page, and
it does not accept the sync token as a credential. No other route is opened
with it. The client IP is the address Caddy already records
(`X-Pantry-Client-IP` when `X-Pantry-Entry` is `public`, otherwise the
connection address). `X-Forwarded-For` is not used. The envelope is not
written to the log.

SDK configuration uses the tunnel option `'/api/sentry-tunnel'`.

In development the Vite server proxies `/api`, so the same path works against
the dev UI's origin. The browser posts its own timings and errors to
`POST /api/telemetry/client`; you read them back from the snapshot.

Lines written to the server log start with `telemetry` when a request is slow
or failing, when a scan is published with no browser connected (after one has
connected before), when a slow browser is dropped, or when the page reports
an error. Grep for that prefix.

How to read a bad morning:

| What you saw | Where to look |
| --- | --- |
| The page never loads | If `curl /health` fails the same way, the process is not accepting connections. If `/health` is ok, check `http.recentProblems`, `http.serverErrors`, and `http.latency` for the route that hung (`GET /`, `GET /api/products`, `GET /api/scans`, `GET /api/inventory`). `streams.openLatency` is how long `GET /api/events` took to send headers. |
| A scan is missing until refresh | `scanSync.publishedWithNoSubscribers` (and `scanSync.byEvent.scan`) counts events saved while `subscribers` was 0. Refresh works because `GET /api/scans` reads the database; the live stream does not replay. `streams.active` and `scanSync.subscribers` are the browsers listening right now. |
| A scan disappears, or the page misses one while it stays open | `scanSync.droppedSlowClients` and `writeErrors` mean a browser fell behind and was disconnected. `client.sseGaps` and `client.sseErrors` are the page reporting a hole in event ids or a dropped stream. `client.recent` lists the latest page loads, route changes, API failures, and those stream errors. `at` on a client report is when the server received it. |
| The page loads, but it feels slow | Read `pageLoad` (the Diagnostics page shows the same numbers). `pageLoad.latest` is the last visit: compare `ttfbMs` (first byte), `documentMs` (DOM ready minus first byte: scripts and styles), `domContentLoadedMs`, `afterDomMs` (full load minus DOM ready), and `durationMs` (full load). `pageLoad.dominant` names the largest of those and the slowest `firstPaintApi` call. `pageLoad.js` and `pageLoad.css` are file counts and transfer size, with no URLs. `pageLoad.slowestClientApi` is how long the browser waited on API routes. `pageLoad.slowestHttp` is server time; `GET /` is the document and the UI files, and `GET /api/inventory` is the inventory payload. `pageLoad.duration`, `ttfb`, and `domContentLoaded` are p50/p95 over recent page loads. A `durationMs` of 0 with a real `domContentLoadedMs` means full load was not recorded (`loadEventEnd` was still 0); use `ttfbMs` and `domContentLoadedMs`. |

Latency fields are milliseconds. `p50Ms` / `p95Ms` / `p99Ms` are upper bounds of fixed buckets (1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000), not exact percentiles. `delivery` is publish-to-write. `ingestToDelivery` is the scan's `scannedAt` to that write. The `GET /api/telemetry` request itself is recorded after the snapshot is sent, so it shows up on the next fetch. UI asset requests share the `GET /` route because one handler serves the page.
