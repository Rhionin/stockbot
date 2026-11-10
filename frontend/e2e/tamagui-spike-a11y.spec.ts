import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';

const harnessPath = '/tamagui-harness.html';

test.describe('tamagui spike harness', () => {
  test('passes axe and activates the action via keyboard', async ({ page }) => {
    await page.goto(harnessPath);
    await expect(page.getByTestId('tamagui-harness-root')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText('Tamagui spike harness')).toBeVisible();
    await expect(page.getByText(OFFLINE_SENTENCE)).toBeVisible();

    const accessibilityScanResults = await new AxeBuilder({ page })
      .include('[data-testid="tamagui-harness-root"]')
      .analyze();
    expect(accessibilityScanResults.violations).toEqual([]);

    const tryAgain = page.getByRole('button', { name: 'Try again' });
    await tryAgain.focus();
    await expect(tryAgain).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(page.getByTestId('tamagui-harness-press-count')).toHaveText('Action presses: 1');
    await page.keyboard.press('Space');
    await expect(page.getByTestId('tamagui-harness-press-count')).toHaveText('Action presses: 2');
  });
});
