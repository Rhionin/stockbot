import { spawn } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// Playwright used to talk to an open listener. The only listener now requires
// a session, so this boots the server, redeems the one-time setup code, and
// saves that cookie before advertising readiness.

const apiURL = process.env.E2E_API_URL ?? 'http://127.0.0.1:18080'
const appBaseURL = process.env.APP_BASE_URL ?? 'http://127.0.0.1:5173'
const readyPort = Number(process.env.E2E_READY_PORT ?? '18081')
const binary = process.env.SERVER_BIN
const databasePath = process.env.DB_PATH
const storagePath = process.env.AUTH_STATE
const email = 'e2e@stockbot.test'
const password = 'correct horse'

if (!binary || !databasePath || !storagePath) {
  console.error('authenticated-server needs SERVER_BIN, DB_PATH, and AUTH_STATE')
  process.exit(1)
}

const setupDir = mkdtempSync(join(tmpdir(), 'stockbot-e2e-setup-'))
for (const suffix of ['', '-wal', '-shm']) rmSync(databasePath + suffix, { force: true })
const api = new URL(apiURL)

const child = spawn(binary, [], {
  env: {
    ...process.env,
    DB_PATH: databasePath,
    ADDR: api.host,
    SETUP_DIR: setupDir,
    APP_BASE_URL: appBaseURL,
    DISABLE_EXTERNAL_PRODUCT_LOOKUP: 'true',
  },
  stdio: 'inherit',
})

const stop = (signal) => {
  if (!child.killed) child.kill(signal)
}
process.on('SIGTERM', () => stop('SIGTERM'))
process.on('SIGINT', () => stop('SIGINT'))
child.on('exit', (code, signal) => {
  if (signal) process.exit(1)
  process.exit(code ?? 0)
})

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function waitFor(label, probe) {
  const deadline = Date.now() + 90_000
  let last = 'not ready'
  while (Date.now() < deadline) {
    try {
      if (await probe()) return
    } catch (error) {
      last = error instanceof Error ? error.message : String(error)
    }
    await sleep(200)
  }
  throw new Error(`${label}: ${last}`)
}

function setupCode() {
  const text = readFileSync(join(setupDir, 'code'), 'utf8')
  const match = text.match(/Stockbot setup code: ([0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4})/)
  if (!match) throw new Error('setup code file has no code')
  return match[1]
}

function sessionCookie(response) {
  const cookies = typeof response.headers.getSetCookie === 'function'
    ? response.headers.getSetCookie()
    : [response.headers.get('set-cookie') ?? '']
  for (const header of cookies) {
    const match = header.match(/^([^=]+)=([^;]*)/)
    if (match && match[1].includes('stockbot_session')) {
      return { name: match[1], value: match[2] }
    }
  }
  throw new Error('login did not set a session cookie')
}

try {
  await waitFor('health', async () => {
    const response = await fetch(`${apiURL}/health`)
    return response.status === 200
  })
  await waitFor('setup code', async () => setupCode() !== '')
  const code = setupCode()
  const setup = await fetch(`${apiURL}/setup`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      setupCode: code,
      name: 'E2E Operator',
      email,
      password,
    }),
  })
  if (!setup.ok) throw new Error(`setup failed: ${setup.status} ${await setup.text()}`)
  const login = await fetch(`${apiURL}/api/auth/login`, {
    method: 'POST',
    headers: { 'content-type': 'application/json', origin: appBaseURL },
    body: JSON.stringify({ email, password }),
  })
  if (!login.ok) throw new Error(`login failed: ${login.status} ${await login.text()}`)
  const cookie = sessionCookie(login)
  writeFileSync(storagePath, JSON.stringify({
    cookies: [{
      name: cookie.name,
      value: cookie.value,
      domain: api.hostname,
      path: '/',
      expires: Math.floor(Date.now() / 1000) + 180 * 24 * 60 * 60,
      httpOnly: true,
      secure: true,
      sameSite: 'Lax',
    }],
    origins: [],
  }))
  await new Promise((resolve, reject) => {
    const ready = createServer((_req, response) => {
      response.writeHead(200, { 'content-type': 'text/plain' })
      response.end('ready')
    })
    ready.listen(readyPort, '127.0.0.1', () => resolve())
    ready.on('error', reject)
  })
} catch (error) {
  console.error(error instanceof Error ? error.message : error)
  stop('SIGTERM')
  process.exit(1)
}
