import { spawn } from 'node:child_process'
import { createServer, request as httpRequest } from 'node:http'
import { existsSync, mkdtempSync, readFileSync, rmSync, statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { extname, join, normalize, resolve } from 'node:path'

// Boots the real Go server, redeems its one-time setup code to create the e2e
// account, then serves mobile/dist on one origin and proxies the API to the
// server, the way Vite does in dev. The browser sees a same-origin site.

const apiURL = process.env.E2E_API_URL ?? 'http://127.0.0.1:18180'
const webPort = Number(process.env.E2E_WEB_PORT ?? '18173')
const webURL = `http://127.0.0.1:${webPort}`
const binary = process.env.SERVER_BIN
const databasePath = process.env.DB_PATH
const distDir = resolve(process.env.DIST_DIR ?? join(import.meta.dirname, '..', 'dist'))
const email = 'e2e@stockbot.test'
const password = 'correct horse'

if (!binary || !databasePath) {
  console.error('web-stack needs SERVER_BIN and DB_PATH')
  process.exit(1)
}
if (!existsSync(join(distDir, 'index.html'))) {
  console.error(`no web export at ${distDir}; run npm run build:web first`)
  process.exit(1)
}

const setupDir = mkdtempSync(join(tmpdir(), 'stockbot-web-e2e-setup-'))
for (const suffix of ['', '-wal', '-shm']) rmSync(databasePath + suffix, { force: true })
const api = new URL(apiURL)

const child = spawn(binary, [], {
  env: {
    ...process.env,
    DB_PATH: databasePath,
    ADDR: api.host,
    SETUP_DIR: setupDir,
    APP_BASE_URL: webURL,
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

const sleep = (ms) => new Promise((resolveSleep) => setTimeout(resolveSleep, ms))

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

const types = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json',
  '.css': 'text/css',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.ttf': 'font/ttf',
  '.svg': 'image/svg+xml',
}

function proxy(req, res) {
  const upstream = httpRequest(
    { host: api.hostname, port: api.port, method: req.method, path: req.url, headers: req.headers },
    (reply) => {
      res.writeHead(reply.statusCode ?? 502, reply.headers)
      reply.pipe(res)
    },
  )
  upstream.on('error', () => {
    res.writeHead(502)
    res.end()
  })
  req.pipe(upstream)
}

function serveStatic(req, res) {
  const path = normalize(decodeURIComponent(new URL(req.url, webURL).pathname))
  let file = join(distDir, path)
  const inside = file.startsWith(distDir)
  const found = inside && existsSync(file) && statSync(file).isFile()
  if (!found) file = join(distDir, 'index.html')
  res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream' })
  res.end(readFileSync(file))
}

try {
  await waitFor('health', async () => (await fetch(`${apiURL}/health`)).status === 200)
  await waitFor('setup code', async () => setupCode() !== '')
  const setup = await fetch(`${apiURL}/setup`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ setupCode: setupCode(), name: 'E2E Operator', email, password }),
  })
  if (!setup.ok) throw new Error(`setup failed: ${setup.status} ${await setup.text()}`)
  await new Promise((resolveListen, reject) => {
    const web = createServer((req, res) => {
      const path = req.url ?? '/'
      if (path.startsWith('/api/') || path === '/health') proxy(req, res)
      else serveStatic(req, res)
    })
    web.listen(webPort, '127.0.0.1', () => resolveListen())
    web.on('error', reject)
  })
} catch (error) {
  console.error(error instanceof Error ? error.message : error)
  stop('SIGTERM')
  process.exit(1)
}
