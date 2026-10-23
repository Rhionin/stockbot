// Release shots for the offline sentence. Not part of the Playwright suite.
// From the repo root, with Node 24 and frontend dependencies installed:
//
//   TZ=America/Denver node frontend/e2e/capture-offline-notice.mjs
//
// The pages render in America/Denver. Shots land in /opt/cursor/artifacts
// (override with OFFLINE_SHOT_DIR). Requires ../bin/stockbot-server
// (go build -o bin/stockbot-server ./cmd/server). Phone shots use Expo web
// and install react-native-web with --no-save when it is missing.
import { spawn } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import playwright from '../node_modules/playwright/index.js';

process.env.TZ = 'America/Denver';

const { chromium } = playwright;
const sentence = "Can't reach Stockbot. Check your connection and try again.";
const repoRoot = join(import.meta.dirname, '..', '..');
const shotDir = process.env.OFFLINE_SHOT_DIR ?? '/opt/cursor/artifacts';
const nodeBin = process.env.NODE_BIN ?? process.execPath;
const apiPort = 18083;
const vitePort = 5174;
const proxyPort = 18084;
const expoPort = 8082;
const apiURL = `http://127.0.0.1:${apiPort}`;
const webURL = `http://127.0.0.1:${vitePort}`;
const children = [];

mkdirSync(shotDir, { recursive: true });

function start(command, args, options) {
  const child = spawn(command, args, {
    ...options,
    env: { ...process.env, TZ: 'America/Denver', ...(options.env ?? {}) },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.stdout?.on('data', () => {});
  child.stderr?.on('data', () => {});
  children.push(child);
  return child;
}

async function waitForOk(url) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {
      // The process is still binding the port.
    }
    await delay(500);
  }
  throw new Error(`Timed out waiting for ${url}`);
}

async function api(path, init) {
  const response = await fetch(`${apiURL}${path}`, {
    ...init,
    headers: { 'content-type': 'application/json', ...(init?.headers ?? {}) },
  });
  const text = await response.text();
  if (!response.ok) throw new Error(`${init?.method ?? 'GET'} ${path} ${response.status} ${text}`);
  return text === '' ? null : JSON.parse(text);
}

async function resetPending() {
  const pending = await api('/api/scans?userId=user-1&status=pending');
  for (const row of pending) {
    await api(`/api/scans/${row.id}`, { method: 'PATCH', body: JSON.stringify({ status: 'cancelled' }) });
  }
}

async function seedProduct(name, barcode) {
  await api('/api/products', {
    method: 'POST',
    body: JSON.stringify({ name, category: 'Grocery', unitOfMeasure: 'carton' }),
  });
  const products = await api('/api/products');
  const product = products.find((row) => row.name === name);
  if (!product) throw new Error(`missing product ${name}`);
  await api('/api/products/overrides', {
    method: 'POST',
    body: JSON.stringify({ barcode, productId: product.id }),
  });
}

async function seedScan(barcode, direction, commit) {
  const created = await api('/api/scans', {
    method: 'POST',
    body: JSON.stringify({ barcode, direction, userId: 'user-1' }),
  });
  if (commit) {
    await api(`/api/scans/${created.id}/commit`, { method: 'POST', body: JSON.stringify({}) });
  }
}

function toastSettled(info) {
  if (!info) return false;
  for (const item of info.chain) {
    if (Number(item.opacity) < 0.999) return false;
    if (!item.transform || item.transform === 'none') continue;
    const match = item.transform.match(/matrix\(([^)]+)\)/);
    if (!match) return false;
    const [a, b, c, d, tx, ty] = match[1].split(',').map((value) => Number(value.trim()));
    if (Math.abs(a - 1) > 0.01 || Math.abs(b) > 0.01 || Math.abs(c) > 0.01 || Math.abs(d - 1) > 0.01) return false;
    if (Math.abs(tx) > 0.5 || Math.abs(ty) > 0.5) return false;
  }
  const { box, viewport } = info;
  return box.width > 0 && box.height > 0
    && box.top >= -1 && box.left >= -1
    && box.bottom <= viewport.height + 1
    && box.right <= viewport.width + 1;
}

async function readToast(page, selector) {
  return page.evaluate(({ selector: rootSelector, expected }) => {
    const root = document.querySelector(rootSelector);
    const el = root ?? [...document.querySelectorAll('.restock-toast, [data-testid="confirm-toast"]')]
      .find((node) => node.textContent?.includes(expected));
    if (!el) return null;
    const chain = [];
    let node = el;
    while (node && node.nodeType === 1) {
      const style = getComputedStyle(node);
      chain.push({ opacity: style.opacity, transform: style.transform });
      node = node.parentElement;
    }
    const box = el.getBoundingClientRect();
    return {
      chain,
      box: { top: box.top, left: box.left, right: box.right, bottom: box.bottom, width: box.width, height: box.height },
      viewport: { width: window.innerWidth, height: window.innerHeight },
    };
  }, { selector, expected: sentence });
}

async function waitForSettledToast(page, selector) {
  const toast = page.locator(selector).filter({ hasText: sentence });
  await toast.waitFor({ state: 'visible', timeout: 15000 });
  await page.waitForFunction(({ selector: rootSelector, expected }) => {
    const root = document.querySelector(rootSelector);
    const el = root && root.textContent?.includes(expected)
      ? root
      : [...document.querySelectorAll('.restock-toast, [data-testid="confirm-toast"]')].find((node) => node.textContent?.includes(expected));
    if (!el) return false;
    let node = el;
    while (node && node.nodeType === 1) {
      const style = getComputedStyle(node);
      if (Number(style.opacity) < 0.999) return false;
      const transform = style.transform;
      if (transform && transform !== 'none') {
        const match = transform.match(/matrix\(([^)]+)\)/);
        if (!match) return false;
        const [a, b, c, d, tx, ty] = match[1].split(',').map((value) => Number(value.trim()));
        if (Math.abs(a - 1) > 0.01 || Math.abs(b) > 0.01 || Math.abs(c) > 0.01 || Math.abs(d - 1) > 0.01) return false;
        if (Math.abs(tx) > 0.5 || Math.abs(ty) > 0.5) return false;
      }
      node = node.parentElement;
    }
    const box = el.getBoundingClientRect();
    return box.top >= -1 && box.left >= -1 && box.bottom <= window.innerHeight + 1 && box.right <= window.innerWidth + 1;
  }, { selector, expected: sentence }, { timeout: 10000 });
  await page.waitForTimeout(600);
  const info = await readToast(page, selector);
  if (!toastSettled(info)) throw new Error(`toast was not settled: ${JSON.stringify(info)}`);
}

async function captureWeb(browser) {
  const shots = [
    { width: 390, height: 844, scheme: 'light', barcode: '37339081', name: 'Settled milk', file: 'web_offline_confirm_390_light.png', cleared: 'web_offline_cleared_inventory_390_light.png' },
    { width: 390, height: 844, scheme: 'dark', barcode: '37339091', name: 'Settled milk dark', file: 'web_offline_confirm_390_dark.png' },
    { width: 1280, height: 800, scheme: 'light', barcode: '37312881', name: 'Settled milk wide', file: 'web_offline_confirm_1280_light.png' },
    { width: 390, height: 844, scheme: 'light', barcode: '37339082', name: 'Settled oats', file: 'web_offline_remove_390_light.png', remove: true },
  ];

  for (const shot of shots) {
    await resetPending();
    await seedProduct(shot.name, shot.barcode);
    if (shot.remove) {
      await seedScan(shot.barcode, 'stock_in', true);
      await seedScan(shot.barcode, 'stock_out', true);
    }
    const context = await browser.newContext({
      viewport: { width: shot.width, height: shot.height },
      colorScheme: shot.scheme,
      locale: 'en-US',
      timezoneId: 'America/Denver',
      baseURL: webURL,
    });
    const page = await context.newPage();
    await page.goto(webURL);
    await page.waitForFunction(
      (scheme) => document.documentElement.getAttribute('data-mantine-color-scheme') === scheme,
      shot.scheme,
    );
    const mode = page.getByRole('alert').filter({ hasText: /^Mode: stock_(in|out)/ });
    await mode.waitFor({ state: 'visible' });
    const scanner = page.getByRole('textbox', { name: 'Barcode scanner input' });
    if (shot.remove || !(await page.getByText('Mode: stock_in').isVisible())) {
      const wanted = shot.remove ? 'STOCK_OUT' : 'STOCK_IN';
      const label = shot.remove ? 'stock_out' : 'stock_in';
      if (!(await page.getByText(`Mode: ${label}`).isVisible())) {
        await scanner.fill(wanted);
        await scanner.press('Enter');
        await page.getByText(`Mode: ${label}`).waitFor({ state: 'visible' });
      }
    }
    await scanner.fill(shot.barcode);
    await scanner.press('Enter');
    const card = page.getByRole('article', { name: new RegExp(`^Scan ${shot.barcode}(,|$)`) });
    await card.waitFor({ state: 'visible' });
    await context.setOffline(true);
    await card.getByRole('button', { name: shot.remove ? 'Remove' : 'Confirm', exact: true }).click();
    await page.getByText(sentence).waitFor({ state: 'visible' });
    await waitForSettledToast(page, '.restock-toast');
    if (await page.getByText('Failed to fetch').count()) throw new Error('raw fetch text is visible');
    await page.screenshot({ path: join(shotDir, shot.file) });
    console.log('saved', shot.file);

    await context.setOffline(false);
    if (shot.cleared) {
      await page.getByRole('link', { name: 'Inventory' }).click();
      await page.getByRole('heading', { name: 'Inventory' }).waitFor({ state: 'visible' });
      await page.getByText(sentence).waitFor({ state: 'hidden' });
      await page.locator('.restock-toast').waitFor({ state: 'detached' });
      await page.waitForTimeout(400);
      await page.screenshot({ path: join(shotDir, shot.cleared) });
      console.log('saved', shot.cleared);
    }
    await context.close();
  }
}

function patchPhoneCredentials() {
  const path = join(repoRoot, 'mobile', 'src', 'auth', 'credentials.ts');
  const original = readFileSync(path, 'utf8');
  if (!original.includes('SecureStore.getItemAsync(SERVER_URL_KEY)')) {
    throw new Error('credentials.ts no longer matches the screenshot patch');
  }
  const patched = original
    .replace(
      'export async function loadCredentials(): Promise<SavedCredentials | null> {',
      `function shotStore(): Storage | null {
  if (typeof localStorage === 'undefined') return null;
  return localStorage;
}

export async function loadCredentials(): Promise<SavedCredentials | null> {
  const memory = shotStore();
  if (memory !== null) {
    const serverUrl = memory.getItem(SERVER_URL_KEY);
    const password = memory.getItem(PASSWORD_KEY);
    if (serverUrl === null || password === null) return null;
    return { serverUrl, password };
  }`,
    )
    .replace(
      'export async function saveCredentials(credentials: SavedCredentials): Promise<void> {\n  await Promise.all([',
      `export async function saveCredentials(credentials: SavedCredentials): Promise<void> {
  const memory = shotStore();
  if (memory !== null) {
    memory.setItem(SERVER_URL_KEY, credentials.serverUrl);
    memory.setItem(PASSWORD_KEY, credentials.password);
    return;
  }
  await Promise.all([`,
    )
    .replace(
      'export async function clearCredentials(): Promise<void> {\n  await Promise.all([',
      `export async function clearCredentials(): Promise<void> {
  const memory = shotStore();
  if (memory !== null) {
    memory.removeItem(SERVER_URL_KEY);
    memory.removeItem(PASSWORD_KEY);
    return;
  }
  await Promise.all([`,
    );
  if (patched === original) throw new Error('could not patch phone credentials for Expo web');
  writeFileSync(path, patched);
  return () => writeFileSync(path, original);
}

async function capturePhone(browser) {
  const restore = patchPhoneCredentials();
  try {
    const rnw = join(repoRoot, 'mobile', 'node_modules', 'react-native-web', 'package.json');
    try {
      readFileSync(rnw);
    } catch {
      await new Promise((resolve, reject) => {
        const install = spawn('npm', ['install', 'react-native-web', '--no-save', '--no-package-lock'], {
          cwd: join(repoRoot, 'mobile'),
          stdio: 'inherit',
          env: { ...process.env, TZ: 'America/Denver' },
        });
        install.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`npm install exited ${code}`))));
      });
    }
    start(join(repoRoot, 'mobile', 'node_modules', '.bin', 'expo'), ['start', '--web', '--port', String(expoPort)], {
      cwd: join(repoRoot, 'mobile'),
      env: { CI: '1', BROWSER: 'none', EXPO_NO_TELEMETRY: '1' },
    });
    await waitForOk(`http://localhost:${expoPort}`);

    const flows = [
      { file: 'mobile_offline_confirm.png', cleared: 'mobile_offline_cleared_after_refresh.png', name: 'Phone milk', barcode: '37390011', remove: false },
      { file: 'mobile_offline_remove.png', name: 'Phone oats', barcode: '37390012', remove: true },
    ];
    for (const flow of flows) {
      await resetPending();
      await seedProduct(flow.name, flow.barcode);
      if (flow.remove) {
        await seedScan(flow.barcode, 'stock_in', true);
        await seedScan(flow.barcode, 'stock_out', true);
        await seedScan(flow.barcode, 'stock_out', false);
      } else {
        await seedScan(flow.barcode, 'stock_in', false);
      }
      const context = await browser.newContext({
        viewport: { width: 390, height: 844 },
        locale: 'en-US',
        timezoneId: 'America/Denver',
        deviceScaleFactor: 2,
      });
      const page = await context.newPage();
      await page.goto(`http://localhost:${expoPort}`, { waitUntil: 'networkidle', timeout: 120000 });
      await page.getByLabel('Connect').waitFor({ timeout: 120000 });
      await page.getByLabel('Server').fill(`http://127.0.0.1:${proxyPort}`);
      await page.getByLabel('Household password').fill('shots');
      await page.getByLabel('Connect').click();
      await page.getByRole('tab', { name: 'Queue' }).click();
      await page.getByText(flow.name, { exact: true }).waitFor({ timeout: 30000 });
      const button = flow.remove ? `Remove ${flow.name}` : `Confirm ${flow.name}`;
      await context.setOffline(true);
      await page.getByRole('button', { name: button }).click();
      await page.getByText(sentence).waitFor({ state: 'visible' });
      await waitForSettledToast(page, '[data-testid="confirm-toast"]');
      if (await page.getByText('Network request failed').count()) throw new Error('raw network text is visible');
      await page.screenshot({ path: join(shotDir, flow.file) });
      console.log('saved', flow.file);
      if (flow.cleared) {
        await context.setOffline(false);
        await page.getByRole('tab', { name: 'Scan' }).click();
        await page.getByRole('tab', { name: 'Queue' }).click();
        await page.getByText(sentence).waitFor({ state: 'hidden', timeout: 15000 });
        await page.getByText(flow.name, { exact: true }).waitFor({ state: 'visible' });
        await page.waitForTimeout(400);
        await page.screenshot({ path: join(shotDir, flow.cleared) });
        console.log('saved', flow.cleared);
      }
      await context.close();
    }
  } finally {
    restore();
  }
}

function startProxy() {
  const child = spawn(nodeBin, ['--input-type=module', '-e', `
    import http from 'node:http';
    const target = ${JSON.stringify(apiURL)};
    http.createServer((req, res) => {
      const cors = {
        'access-control-allow-origin': '*',
        'access-control-allow-methods': 'GET,POST,PATCH,PUT,DELETE,OPTIONS',
        'access-control-allow-headers': req.headers['access-control-request-headers'] || '*',
      };
      if (req.method === 'OPTIONS') { res.writeHead(204, cors); res.end(); return; }
      const headers = { ...req.headers, host: '127.0.0.1:${apiPort}' };
      delete headers.origin;
      const preq = http.request(target + req.url, { method: req.method, headers }, (pres) => {
        res.writeHead(pres.statusCode ?? 502, { ...pres.headers, ...cors });
        pres.pipe(res);
      });
      preq.on('error', (err) => { res.writeHead(502, cors); res.end(err.message); });
      req.pipe(preq);
    }).listen(${proxyPort}, '127.0.0.1');
  `], { env: { ...process.env, TZ: 'America/Denver' }, stdio: 'ignore' });
  children.push(child);
}

async function main() {
  const databasePath = join(tmpdir(), `stockbot-offline-shots-${process.pid}.db`);
  start(join(repoRoot, 'bin', 'stockbot-server'), [], {
    env: {
      DB_PATH: databasePath,
      ADDR: `127.0.0.1:${apiPort}`,
      DISABLE_EXTERNAL_PRODUCT_LOOKUP: 'true',
    },
  });
  start(nodeBin, [join(repoRoot, 'frontend', 'node_modules', 'vite', 'bin', 'vite.js'), '--host', '127.0.0.1', '--port', String(vitePort)], {
    cwd: join(repoRoot, 'frontend'),
    env: { VITE_API_PROXY_TARGET: apiURL },
  });
  startProxy();
  await waitForOk(`${apiURL}/health`);
  await waitForOk(webURL);
  await waitForOk(`http://127.0.0.1:${proxyPort}/api/session`);

  const browser = await chromium.launch({ headless: true });
  try {
    await captureWeb(browser);
    await capturePhone(browser);
  } finally {
    await browser.close();
  }
}

try {
  await main();
} finally {
  for (const child of children) child.kill('SIGTERM');
}
