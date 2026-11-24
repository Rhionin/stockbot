import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { defineConfig } from '@playwright/test'

const webPort = 18173
const webURL = `http://127.0.0.1:${webPort}`
if (!process.env.STOCKBOT_E2E_RUN) process.env.STOCKBOT_E2E_RUN = String(process.pid)
const runId = process.env.STOCKBOT_E2E_RUN

// Same pre-built server binary as the Vite app's e2e run. Build it with:
//   go build -o ../bin/stockbot-server ../cmd/server
const serverBinary = process.env.SERVER_BIN ?? join('..', 'bin', 'stockbot-server')

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  webServer: {
    command: 'node e2e/web-stack.mjs',
    env: {
      ...process.env,
      SERVER_BIN: serverBinary,
      DB_PATH: join(tmpdir(), `stockbot-web-e2e-${runId}.db`),
      E2E_API_URL: 'http://127.0.0.1:18180',
      E2E_WEB_PORT: String(webPort),
    },
    url: `${webURL}/health`,
    reuseExistingServer: false,
    timeout: 120_000,
  },
  use: {
    baseURL: webURL,
    locale: 'en-US',
    trace: 'off',
  },
})
