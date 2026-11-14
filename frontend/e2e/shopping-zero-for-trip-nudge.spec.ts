import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';

const shotDir = process.env.ZERO_FOR_TRIP_SHOTS ?? '/opt/cursor/artifacts';
mkdirSync(shotDir, { recursive: true });

const viewports = [
  { width: 390, height: 844, label: '390' },
  { width: 320, height: 640, label: '320' },
  { width: 1280, height: 800, label: '1280' },
] as const;

const RESTOCK_BODY = {
  household: { kind: 'months', value: 2 },
  householdLabel: '2 months',
  appliesTo: 1,
  itemCount: 1,
  items: [{
    itemId: 'item-synth-tea',
    productId: 'prod-tea',
    name: 'Synth Tea',
    unit: 'boxes',
    target: { kind: 'amount', value: 3 },
    level: 'low',
    sourceLabel: 'item',
    valueLabel: '3',
    rowSource: 'item',
    chain: [],
    categories: [],
    setLabel: '3 boxes',
  }],
  categories: [],
  notRestocked: 0,
  suggestionCount: 0,
};

const SYNTHETIC_LINE = {
  id: 'line-679-e2e',
  itemId: 'item-synth-tea',
  quantity: 2,
  source: 'manual',
  purchasedAt: null,
  sent: 0,
  storeId: null,
  canSendToCart: true,
  lineKey: 'item:item-synth-tea',
};

const OFF_LIST_ROW = {
  id: 'item:item-synth-tea',
  itemId: 'item-synth-tea',
  name: 'Synth Tea',
  offListReason: 'zero_for_trip',
  sent: 0,
  storeId: null,
  canSendToCart: false,
  needsAttention: null,
  zeroForTrip: {
    entryId: 'line-679-e2e',
    providerId: 'kroger',
    actorUserId: null,
    actorDisplayName: null,
    setAt: null,
  },
};

async function installSession(page: import('@playwright/test').Page) {
  await page.route('**/api/auth/session', async (route) => {
    if (route.request().method() !== 'GET') return route.fallback();
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'account',
        user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
        memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
        households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
        activeHouseholdId: 'hh-1',
        operator: false,
      }),
    });
  });
  await page.route('**/api/config', async (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ live: 'sse', minClientVersion: { expo: '1.0.0' }, entry: 'public' }),
  }));
  await page.route('**/api/events', async (route) => route.fulfill({
    status: 200,
    contentType: 'text/event-stream',
    body: 'event: hint\ndata: {"type":"devices"}\n\n',
  }));
}

type ListMockState = {
  skipped: boolean;
  line: Record<string, unknown>;
};

function initialMockState(offListOnly: boolean): ListMockState {
  if (offListOnly) {
    return {
      skipped: true,
      line: { ...SYNTHETIC_LINE, quantity: 0, adjustment: 0, provider: 'kroger' },
    };
  }
  return {
    skipped: false,
    line: { ...SYNTHETIC_LINE, quantity: 2, provider: 'kroger' },
  };
}

async function installShoppingMocks(
  page: import('@playwright/test').Page,
  options: { offListOnly?: boolean; dontRestockDelayMs?: number; dontRestockFails?: boolean } = {},
) {
  const mock = initialMockState(Boolean(options.offListOnly));
  await installSession(page);
  await page.route('**/api/restock**', (route) => {
    const url = route.request().url();
    if (url.includes('/dont-restock')) return route.fallback();
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(RESTOCK_BODY),
    });
  });
  await page.route('**/api/restock/items/item-synth-tea/dont-restock', async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    if (options.dontRestockFails) {
      return route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'synthetic failure' }),
      });
    }
    if (options.dontRestockDelayMs) await new Promise((resolve) => setTimeout(resolve, options.dontRestockDelayMs));
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        title: 'Synth Tea',
        detail: 'Its restock target is now 0.',
        previous: { kind: 'amount', value: 3 },
      }),
    });
  });
  await page.route('**/api/inventory**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify([{
      item: {
        id: 'item-synth-tea',
        product: { id: 'prod-tea', name: 'Synth Tea', category: 'Tea', unitOfMeasure: 'boxes' },
      },
      instanceCount: 0,
      nearExpiryCount: 0,
      expiredCount: 0,
    }]),
  }));
  await page.route('**/api/providers**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify([{
      id: 'kroger',
      displayName: 'Kroger',
      capabilities: {
        auth: 'oauth2_authorization_code',
        delivery: 'server_push',
        confirmation: 'per_request',
        mutation: 'add_only',
        identity: 'derived',
      },
      connectionState: 'connected',
      credentialsConfigured: true,
    }]),
  }));
  const statusEnvelope = () => ({
    cursor: 'c1',
    listStale: false,
    items: mock.skipped ? [] : [{ ...SYNTHETIC_LINE, quantity: mock.line.quantity, adjustment: mock.line.adjustment }],
    offList: mock.skipped ? [OFF_LIST_ROW] : [],
    stores: [],
    attentionGroups: [],
    storeDisabled: null,
    lastTripEndedAt: null,
    lastTripEndedBy: null,
  });
  await page.route('**/api/shopping-list**', (route) => {
    const url = route.request().url();
    const method = route.request().method();
    if (method === 'GET' && url.includes('status=1')) {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(statusEnvelope()),
      });
    }
    if (method === 'GET' && !url.includes('/items/') && !url.includes('/export')) {
      const legacy = options.offListOnly && mock.skipped
        ? []
        : [mock.line];
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(legacy),
      });
    }
    return route.fallback();
  });
  await page.route('**/api/shopping-list/items/line-679-e2e/adjustment**', (route) => {
    if (route.request().method() === 'PUT') {
      mock.skipped = true;
      mock.line = { ...mock.line, quantity: 0, adjustment: 0 };
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ adjustment: 0 }) });
    }
    return route.fallback();
  });
}

test.describe('zero for trip inline nudge', () => {
  test.beforeEach(async ({ page }) => {
    await installShoppingMocks(page);
  });

  test('shows inline nudge after saving 0', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/shopping');
    await expect(page.getByRole('heading', { name: 'Shopping plan' })).toBeVisible();
    const amount = page.getByLabel('Change amount for Synth Tea');
    await amount.fill('0');
    const refreshDone = page.waitForResponse(
      (res) => res.url().includes('/api/shopping-list') && res.request().method() === 'GET' && !res.url().includes('status=1'),
    );
    await amount.blur();
    await refreshDone;
    await expect(amount).toHaveValue('0');
    await expect(page.getByText('Skipped for this trip. It comes back next trip.')).toBeVisible();
    await expect(page.getByText("Don't want it back next trip?")).toBeVisible();
    await expect(page.getByRole('button', { name: "Don't restock Synth Tea" })).toBeVisible();
  });
});

test.describe('zero for trip screenshots (synthetic)', () => {

  for (const viewport of viewports) {
    test(`inline nudge ${viewport.label}px`, async ({ page }) => {
      await installShoppingMocks(page);
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto('/shopping');
      const amount = page.getByLabel('Change amount for Synth Tea');
      await amount.fill('0');
      await amount.blur();
      await expect(amount).toHaveValue('0');
      await expect(page.getByText("Don't want it back next trip?")).toBeVisible();
      const row = page.getByRole('row').filter({ hasText: 'Synth Tea' });
      await row.screenshot({
        path: join(shotDir, `679-inline-nudge-${viewport.label}.png`),
      });
    });

    test(`status view nudge ${viewport.label}px`, async ({ page }) => {
      await installShoppingMocks(page, { offListOnly: true });
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto('/shopping');
      await page.getByRole('button', { name: /Not on the list/ }).click();
      await expect(page.getByText("Don't want it back next trip?")).toBeVisible();
      const statusRow = page.locator('.shopping-not-on-list-row').filter({ hasText: 'Synth Tea' });
      await statusRow.screenshot({
        path: join(shotDir, `679-status-nudge-${viewport.label}.png`),
      });
    });

    test(`loading state ${viewport.label}px`, async ({ page }) => {
      await installShoppingMocks(page, { dontRestockDelayMs: 3000 });
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto('/shopping');
      const amount = page.getByLabel('Change amount for Synth Tea');
      await amount.fill('0');
      await amount.blur();
      await expect(amount).toHaveValue('0');
      const row = page.getByRole('row').filter({ hasText: 'Synth Tea' });
      await page.getByRole('button', { name: "Don't restock Synth Tea" }).click();
      await expect(page.locator('.zero-for-trip-nudge-spinner')).toBeVisible();
      await row.screenshot({
        path: join(shotDir, `679-nudge-loading-${viewport.label}.png`),
      });
    });

    test(`error state ${viewport.label}px`, async ({ page }) => {
      await installShoppingMocks(page, { dontRestockFails: true });
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await page.goto('/shopping');
      const amount = page.getByLabel('Change amount for Synth Tea');
      await amount.fill('0');
      await amount.blur();
      await expect(amount).toHaveValue('0');
      const row = page.getByRole('row').filter({ hasText: 'Synth Tea' });
      await page.getByRole('button', { name: "Don't restock Synth Tea" }).click();
      await expect(page.getByText("Couldn't change the restock target. Try again.")).toBeVisible();
      await row.screenshot({
        path: join(shotDir, `679-nudge-error-${viewport.label}.png`),
      });
    });
  }
});