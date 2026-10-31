import { expect, test } from '@playwright/test'

test('CI proof failure for issue #488', () => {
  expect(1).toBe(2)
})
