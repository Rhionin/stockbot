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

const GROUP_RESTOCK_BODY = {
  ...RESTOCK_BODY,
  itemCount: 1,
  items: [{
    itemId: 'item-synth-cracker',
    productId: 'prod-cracker',
    name: 'Synth Cracker',
    unit: 'boxes',
    target: { kind: 'amount', value: 2 },
    level: 'low',
    sourceLabel: 'item',
    valueLabel: '2',
    rowSource: 'item',
    chain: [],
    categories: [],
    setLabel: '2 boxes',
  }],
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

const GROUP_LINE = {
  id: 'line-679-group',
  itemId: 'item-synth-cracker',
  quantity: 2,
  source: 'manual',
  purchasedAt: null,
  sent: 0,
  storeId: null,
  canSendToCart: true,
  lineKey: 'item:item-synth-cracker',
  group: {
    id: 'grp-synth-snacks',
    name: 'Synth Snacks',
    rule: 'favorite',
    ruleConfirmed: true,
    members: [{ itemId: 'item-synth-cracker', productId: 'prod-cracker', name: 'Synth Cracker', onHand: 0 }],
  },
};

const OFF_LIST_ROW = {
  id: 'item:item-synth-tea',
  itemId: 'item-synth-tea',
  name: 'Synth Tea',
  offListReason: 'zero_for_trip' as const,
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

const GROUPS_BODY = [{
  id: 'grp-synth-snacks',
  name: 'Synth Snacks',
  rule: 'favorite',
  ruleConfirmed: true,
  members: GROUP_LINE.group.members,
  windowMonths: 6,
}];

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

type MockOptions = {
  offListOnly?: boolean;
  dontRestockDelayMs?: number;
  dontRestockFails?: boolean;
  groupLine?: boolean;
};

function initialMockState(offListOnly: boolean, groupLine: boolean): ListMockState {
  const baseLine = groupLine ? GROUP_LINE : SYNTHETIC_LINE;
  if (offListOnly) {
    return {
      skipped: true,
      line: { ...baseLine, quantity: 0, adjustment: 0, provider: 'kroger' },
    };
  }
  return {
    skipped: false,
    line: { ...baseLine, quantity: 2, provider: 'kroger' },
  };
}

async function installShoppingMocks(
  page: import('@playwright/test').Page,
  options: MockOptions = {},
) {
  const mock = initialMockState(Boolean(options.offListOnly), Boolean(options.groupLine));
  await installSession(page);
  await page.route('**/api/groups**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(options.groupLine ? GROUPS_BODY : []),
  }));
  await page.route('**/api/restock**', (route) => {
    const url = route.request().url();
    if (url.includes('/dont-restock') || url.includes('/groups/')) return route.fallback();
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(options.groupLine ? GROUP_RESTOCK_BODY : RESTOCK_BODY),
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
  await page.route('**/api/groups/grp-synth-snacks/target', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback();
    if (options.dontRestockFails) {
      return route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'fail' }) });
    }
    if (options.dontRestockDelayMs) await new Promise((resolve) => setTimeout(resolve, options.dontRestockDelayMs));
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...GROUPS_BODY[0], windowMonths: 0 }),
    });
  });
  await page.route('**/api/inventory**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify([{
      item: {
        id: options.groupLine ? 'item-synth-cracker' : 'item-synth-tea',
        product: {
          id: options.groupLine ? 'prod-cracker' : 'prod-tea',
          name: options.groupLine ? 'Synth Cracker' : 'Synth Tea',
          category: 'Pantry',
          unitOfMeasure: 'boxes',
        },
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
  const offListRow = options.groupLine
    ? {
      id: 'group:grp-synth-snacks',
      groupId: 'grp-synth-snacks',
      name: 'Synth Snacks',
      offListReason: 'zero_for_trip' as const,
      sent: 0,
      storeId: null,
      canSendToCart: false,
      needsAttention: null,
      zeroForTrip: {
        entryId: 'line-679-group',
        providerId: 'kroger',
        actorUserId: null,
        actorDisplayName: null,
        setAt: null,
      },
    }
    : OFF_LIST_ROW;
  const statusEnvelope = () => ({
    cursor: 'c1',
    listStale: false,
    items: mock.skipped ? [] : [{ ...SYNTHETIC_LINE, quantity: mock.line.quantity, adjustment: mock.line.adjustment }],
    offList: mock.skipped ? [offListRow] : [],
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
  const entryId = options.groupLine ? 'line-679-group' : 'line-679-e2e';
  await page.route(`**/api/shopping-list/items/${entryId}/adjustment**`, (route) => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON() as { adjustment?: number };
      if (body.adjustment === 0) {
        mock.skipped = true;
        mock.line = { ...mock.line, quantity: 0, adjustment: 0 };
      }
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ adjustment: body.adjustment ?? 0 }) });
    }
    return route.fallback();
  });
}

async function prepareReducedMotion(page: import('@playwright/test').Page) {
  await page.emulateMedia({ reducedMotion: 'reduce' });
}

async function skipToZero(page: import('@playwright/test').Page, label: string) {
  await page.goto('/shopping');
  await expect(page.getByRole('heading', { name: 'Shopping plan' })).toBeVisible();
  const amount = page.getByLabel(label);
  await amount.fill('0');
  await amount.blur();
  await expect(amount).toHaveValue('0');
  await expect(page.getByText("Don't want")).toBeVisible();
}

async function openOffList(page: import('@playwright/test').Page) {
  await page.goto('/shopping');
  const toggle = page.getByRole('button', { name: /Not on the list/ });
  await toggle.click();
  const statusRow = page.locator('.shopping-not-on-list-row').first();
  await expect(statusRow.getByText("Don't want")).toBeVisible();
  await expect(statusRow.getByLabel(/Change amount/)).toBeVisible();
  return statusRow;
}

test.describe('zero for trip inline nudge', () => {
  test.beforeEach(async ({ page }) => {
    await installShoppingMocks(page);
    await prepareReducedMotion(page);
  });

  test('shows inline nudge after saving 0', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await skipToZero(page, 'Change amount for Synth Tea');
    await expect(page.getByText("Don't want it back next trip?")).toBeVisible();
    await expect(page.getByRole('button', { name: "Don't restock Synth Tea" })).toBeVisible();
  });

  test('keeps inline confirm and nudge after reload', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await skipToZero(page, 'Change amount for Synth Tea');
    await page.reload();
    await expect(page.getByRole('heading', { name: 'Shopping plan' })).toBeVisible();
    await expect(page.getByLabel('Change amount for Synth Tea')).toHaveValue('0');
    await expect(page.getByText('Skipped for this trip. It comes back next trip.')).toBeVisible();
    await expect(page.getByText("Don't want it back next trip?")).toBeVisible();
  });

  test('keeps zero adjustment after dont-restock error', async ({ page }) => {
    await page.route('**/api/restock/items/item-synth-tea/dont-restock', async (route) => {
      if (route.request().method() !== 'POST') return route.fallback();
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'synthetic failure' }),
      });
    });
    await page.setViewportSize({ width: 390, height: 844 });
    const adjustmentPuts: string[] = [];
    page.on('request', (req) => {
      if (req.method() === 'PUT' && req.url().includes('/adjustment')) adjustmentPuts.push(req.url());
    });
    await skipToZero(page, 'Change amount for Synth Tea');
    await page.getByRole('button', { name: "Don't restock Synth Tea" }).click();
    await expect(page.getByText("Couldn't change the restock target. Try again.")).toBeVisible();
    await expect(page.getByLabel('Change amount for Synth Tea')).toHaveValue('0');
    await expect(page.getByText('Skipped for this trip. It comes back next trip.')).toBeVisible();
    expect(adjustmentPuts).toHaveLength(1);
  });
});

test.describe('zero for trip screenshots (synthetic)', () => {
  for (const viewport of viewports) {
    for (const theme of ['light', 'dark'] as const) {
      const suffix = theme === 'dark' ? `-dark-${viewport.label}` : `-${viewport.label}`;

      test(`inline nudge ${viewport.label}px ${theme}`, async ({ page }) => {
        await installShoppingMocks(page);
        await prepareReducedMotion(page);
        if (theme === 'dark') await page.emulateMedia({ colorScheme: 'dark' });
        await page.setViewportSize({ width: viewport.width, height: viewport.height });
        await skipToZero(page, 'Change amount for Synth Tea');
        const row = page.getByRole('row').filter({ hasText: 'Synth Tea' });
        await row.screenshot({ path: join(shotDir, `679-inline-nudge${suffix}.png`) });
      });

      test(`status view nudge ${viewport.label}px ${theme}`, async ({ page }) => {
        await installShoppingMocks(page, { offListOnly: true });
        await prepareReducedMotion(page);
        if (theme === 'dark') await page.emulateMedia({ colorScheme: 'dark' });
        await page.setViewportSize({ width: viewport.width, height: viewport.height });
        const statusRow = await openOffList(page);
        await statusRow.screenshot({ path: join(shotDir, `679-status-nudge${suffix}.png`) });
      });

      test(`loading state ${viewport.label}px ${theme}`, async ({ page }) => {
        await installShoppingMocks(page, { dontRestockDelayMs: 3000 });
        await prepareReducedMotion(page);
        if (theme === 'dark') await page.emulateMedia({ colorScheme: 'dark' });
        await page.setViewportSize({ width: viewport.width, height: viewport.height });
        await skipToZero(page, 'Change amount for Synth Tea');
        const row = page.getByRole('row').filter({ hasText: 'Synth Tea' });
        const button = page.getByRole('button', { name: "Don't restock Synth Tea" });
        const beforeWidth = (await button.boundingBox())!.width;
        await button.click();
        await expect(page.locator('.zero-for-trip-nudge-spinner')).toBeVisible();
        const duringWidth = (await button.boundingBox())!.width;
        expect(Math.abs(beforeWidth - duringWidth)).toBeLessThanOrEqual(0.5);
        await row.screenshot({ path: join(shotDir, `679-nudge-loading${suffix}.png`) });
      });

      test(`error state ${viewport.label}px ${theme}`, async ({ page }) => {
        await installShoppingMocks(page, { dontRestockFails: true });
        await prepareReducedMotion(page);
        if (theme === 'dark') await page.emulateMedia({ colorScheme: 'dark' });
        await page.setViewportSize({ width: viewport.width, height: viewport.height });
        await skipToZero(page, 'Change amount for Synth Tea');
        const row = page.getByRole('row').filter({ hasText: 'Synth Tea' });
        await page.getByRole('button', { name: "Don't restock Synth Tea" }).click();
        await expect(page.getByText("Couldn't change the restock target. Try again.")).toBeVisible();
        await row.screenshot({ path: join(shotDir, `679-nudge-error${suffix}.png`) });
      });
    }
  }

  for (const viewport of viewports) {
    test(`group inline nudge ${viewport.label}px`, async ({ page }) => {
      await installShoppingMocks(page, { groupLine: true });
      await prepareReducedMotion(page);
      await page.setViewportSize({ width: viewport.width, height: viewport.height });
      await skipToZero(page, 'Change amount for Synth Cracker');
      await expect(page.getByText("Don't want the Synth Snacks group back next trip?")).toBeVisible();
      const row = page.getByRole('row').filter({ hasText: 'Synth Snacks' });
      await row.screenshot({ path: join(shotDir, `679-inline-nudge-group-${viewport.label}.png`) });
    });
  }
});