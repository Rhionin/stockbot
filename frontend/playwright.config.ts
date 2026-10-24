import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { defineConfig } from '@playwright/test'

const apiURL = 'http://127.0.0.1:18080'
const readyURL = 'http://127.0.0.1:18081/ready'
const webURL = 'http://127.0.0.1:5173'
// Workers re-import this file under a different pid. Pin the paths on the
// process that first loads the config so the server and the tests share them.
if (!process.env.STOCKBOT_E2E_RUN) process.env.STOCKBOT_E2E_RUN = String(process.pid)
const runId = process.env.STOCKBOT_E2E_RUN
const databasePath = join(tmpdir(), `pantry-playwright-${runId}.db`)
const storageState = join(tmpdir(), `pantry-playwright-${runId}.auth.json`)

// Path to the pre-built server binary, relative to this frontend directory.
// CI builds it here (see the "Build server binary" step in .github/workflows/ci.yml)
// so Playwright launches an already-compiled binary instead of cold-compiling
// `go run` under the default webServer timeout. It lives in the repo-root `bin/`
// directory, which is git-ignored. Build locally with:
//   go build -o ../bin/stockbot-server ../cmd/server
const serverBinary = join('..', 'bin', 'stockbot-server')

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  // Keep this at 1. The e2e run boots a single Go API + single SQLite DB, and
  // scanner mode is server-global state. Running specs concurrently would let one
  // spec's mode switch (e.g. flipping to stock-out) corrupt another spec's
  // in-flight scan, producing flaky failures. Raising the worker count safely
  // would first require per-worker server/DB isolation.
  workers: 1,
  webServer: [
    {
      command: 'node e2e/authenticated-server.mjs',
      env: {
        ...process.env,
        SERVER_BIN: serverBinary,
        DB_PATH: databasePath,
        AUTH_STATE: storageState,
        E2E_API_URL: apiURL,
        APP_BASE_URL: webURL,
        E2E_READY_PORT: '18081',
      },
      url: readyURL,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    {
      command: `VITE_API_PROXY_TARGET=${apiURL} npm run dev -- --host 127.0.0.1`,
      url: webURL,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
  use: {
    baseURL: webURL,
    locale: 'en-US',
    storageState,
    extraHTTPHeaders: { Origin: webURL },
    trace: 'retain-on-failure',
  },
})
