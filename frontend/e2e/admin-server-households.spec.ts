import { expect, test } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

for (const { width, height } of [{ width: 390, height: 844 }, { width: 1280, height: 800 }] as const) {
  test(`admin server households show counts only at ${width}x${height}`, async ({ page }) => {
    resetStage()
    stage.kind = 'account'
    stage.operator = true
    stage.householdName = 'Test Household'
    await page.setViewportSize({ width, height })
    await install(page)
    await page.goto('/households')
    await expect(page.getByRole('heading', { name: 'All households (Admin)' })).toBeVisible()
    const section = page.getByTestId('operator-households-section')
    await expect(section.getByText('Test Household', { exact: true }).first()).toBeVisible()
    await expect(section.getByText(/2 scanners online/)).toBeVisible()
    await expect(section.getByText(/1 scanner offline/)).toBeVisible()
    await expect(section.getByText(/5 scans this week/)).toBeVisible()
    await expect(section.getByText('secret@example.com')).toHaveCount(0)
    await expect(section.getByText('Ada')).toHaveCount(0)
    await expect(page.getByText(/\boperator\b/i)).toHaveCount(0)
  })
}
