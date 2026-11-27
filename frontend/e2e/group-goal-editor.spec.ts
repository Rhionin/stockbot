import { expect, test } from '@playwright/test';

const mockGroup = {
  id: 'g-juice-mock',
  name: 'Juice',
  rule: 'same_as_ran_out',
  ruleConfirmed: true,
  members: [
    {
      productId: 'apple',
      name: 'Apple juice 64 fl oz',
      onHand: 1,
      sizeKind: 'volume',
      size: { amount: 64, unit: 'fl oz' },
    },
    {
      productId: 'grape',
      name: 'Grape juice 64 fl oz',
      onHand: 0,
      sizeKind: 'volume',
      size: { amount: 64, unit: 'fl oz' },
    },
  ],
  runningLow: false,
  target: null,
  amountAvailable: true,
  amountUnit: 'fl oz',
  defaultKind: 'months',
  defaultMonths: 3,
  version: 'v-mock',
};

async function fulfillGroup(route: import('@playwright/test').Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  });
}

async function capture(page: import('@playwright/test').Page, name: string, width: number) {
  await page.setViewportSize({ width, height: 844 });
  await page.screenshot({ path: `/opt/cursor/artifacts/group-goal-editor-${name}-${width}.png`, fullPage: true });
}

test.describe('group goal editor (mocked API)', () => {
  test('saves items goal at 390 and 320', async ({ page }) => {
    let putBody: unknown;
    await page.route('**/api/groups/**', async (route) => {
      const url = route.request().url();
      if (route.request().method() === 'GET' && url.includes('g-juice-mock')) {
        await fulfillGroup(route, mockGroup);
        return;
      }
      if (route.request().method() === 'PUT' && url.includes('/target')) {
        putBody = route.request().postDataJSON();
        await fulfillGroup(route, {
          ...mockGroup,
          target: { kind: 'items', value: 2, unit: 'items', status: 'ok' },
        });
        return;
      }
      await route.continue();
    });

    await page.goto('/groups/g-juice-mock');
    await expect(page.locator('.keep-on-hand-block .seesaw-caption')).toBeVisible();
    await page.getByRole('button', { name: '3 mo default' }).click();
    await page.getByRole('radio', { name: /Number of items/ }).click();
    await page.getByRole('textbox', { name: 'Items' }).fill('2');
    await page.getByRole('button', { name: 'Save' }).click();
    await expect.poll(() => putBody).toEqual({ kind: 'items', value: 2, expectedVersion: 'v-mock' });
    await capture(page, 'save-items-light', 390);
    await page.evaluate(() => {
      window.localStorage.setItem('mantine-color-scheme-value', 'dark');
      document.documentElement.setAttribute('data-mantine-color-scheme', 'dark');
    });
    await capture(page, 'save-items-dark', 390);
    await capture(page, 'save-items-light', 320);
    await capture(page, 'save-items-dark', 320);
  });

  test('shows amount_unavailable blocker path', async ({ page }) => {
    await page.route('**/api/groups/**', async (route) => {
      const url = route.request().url();
      if (route.request().method() === 'GET' && url.includes('g-juice-mock')) {
        await fulfillGroup(route, {
          ...mockGroup,
          amountAvailable: false,
          amountBlockers: [{ productId: 'lemon', name: 'Frozen lemonade', reason: 'size_missing' }],
        });
        return;
      }
      await route.continue();
    });

    await page.goto('/groups/g-juice-mock');
    await page.getByRole('button', { name: '3 mo default' }).click();
    await expect(page.getByText("Frozen lemonade doesn't have one")).toBeVisible();
    await expect(page.getByText('Frozen lemonade: no size')).toBeVisible();
  });
});