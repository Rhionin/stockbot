import { expect, test, type APIRequestContext, type Locator, type Page, type Route } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const shotDir = process.env.BATCH_BANNER_SHOTS
const fallback = 'Some scans could not be confirmed.'
const wasLine = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.'
const manyLine = '2 cards had fewer on hand than scanned, so only what was on hand was taken out.'
const failureNote = 'Nothing on hand to take out.'
const busySentence = 'Stockbot is busy right now. Try again in a moment.'
const noSpaceSentence = "Stockbot is out of storage space, so this wasn't saved."
// commit.go wraps a missing unit as this sentence.
const instanceGone = 'instance "inst-1" not found or already removed: item instance not found'

const viewports = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
] as const

interface KnownScan {
  id: string
  barcode: string
}

const cancelScan = async (request: APIRequestContext, id: string) => {
  const response = await request.patch(`/api/scans/${id}`, { data: { status: 'cancelled' } })
  expect(response.ok()).toBe(true)
}

const stockIn = async (request: APIRequestContext, barcode: string) => {
  const created = await request.post('/api/scans', {
    data: { barcode, direction: 'stock_in', userId: 'user-1' },
  })
  expect(created.ok(), await created.text()).toBe(true)
  const body = await created.json() as { id: string }
  const committed = await request.post(`/api/scans/${body.id}/commit`, { data: {} })
  expect(committed.ok(), await committed.text()).toBe(true)
}

const seedStockOuts = async (
  page: Page,
  request: APIRequestContext,
  items: { barcode: string; name: string }[],
): Promise<KnownScan[]> => {
  for (const item of items) {
    await createKnownProduct(request, {
      barcode: item.barcode,
      name: item.name,
      category: 'Grocery',
      unitOfMeasure: 'bag',
    })
    await stockIn(request, item.barcode)
  }
  await page.goto('/')
  await resetToStockIn(page)
  await setScannerMode(page, 'stock_out')
  for (const item of items) {
    await scanBarcode(page, item.barcode)
  }
  const pending = await request.get('/api/scans?userId=user-1&status=pending')
  expect(pending.ok()).toBe(true)
  const rows = await pending.json() as { id: string; barcode: string }[]
  return items.map((item) => {
    const row = rows.find((entry) => entry.barcode === item.barcode)
    if (row === undefined) throw new Error(`missing scan ${item.barcode}`)
    return { id: row.id, barcode: item.barcode }
  })
}

const batchCommitResponse = (page: Page) => page.waitForResponse((response) => {
  return response.url().includes('/api/scans/batch-commit') && response.request().method() === 'POST'
})

// A cancel publishes a scan event, and the queue clears the partial line when
// any scan event arrives. That event has to land before the batch body, or the
// line is gone by the time the banner is checked.
const clearCommittedScans = async (
  page: Page,
  route: Route,
  scans: KnownScan[],
  committedIds: readonly unknown[],
) => {
  const dropping = scans.filter((scan) => committedIds.includes(scan.id))
  for (const scan of dropping) {
    const cancelled = await route.fetch({
      url: new URL(`/api/scans/${scan.id}`, route.request().url()).toString(),
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      postData: JSON.stringify({ status: 'cancelled' }),
    })
    if (!cancelled.ok()) throw new Error(`could not clear scan ${scan.id}`)
  }
  for (const scan of dropping) {
    await page.waitForFunction((barcode) => {
      return [...document.querySelectorAll('article')].every((article) => article.getAttribute('aria-label') !== `Scan ${barcode}`)
    }, scan.barcode)
  }
}

const confirmBatch = async (
  page: Page,
  scans: KnownScan[],
  fulfill: (ids: Map<string, string>) => Record<string, unknown>,
) => {
  const ids = new Map(scans.map((scan) => [scan.barcode, scan.id]))
  const outcome = batchCommitResponse(page)
  await page.route('**/api/scans/batch-commit', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    const body = fulfill(ids)
    const committed = Array.isArray(body.committedIds) ? body.committedIds : []
    await clearCommittedScans(page, route, scans, committed)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
  })
  for (const scan of scans) {
    const card = page.getByRole('article', { name: `Scan ${scan.barcode}`, exact: true })
    await card.getByRole('checkbox', { name: 'Select scan for batch approval' }).check()
  }
  await page.getByRole('button', { name: `Confirm ${scans.length} selected scans` }).click()
  await outcome
}

const fitsInViewport = async (locator: Locator) => {
  await locator.scrollIntoViewIfNeeded()
  await expect(locator).toBeVisible()
  await expect.poll(() => locator.evaluate((root) => {
    const box = root.getBoundingClientRect()
    return box.width > 0
      && box.height > 0
      && box.left >= 0
      && box.right <= window.innerWidth + 0.5
      && box.top >= 0
      && box.bottom <= window.innerHeight + 0.5
      && root.scrollWidth <= root.clientWidth + 1
  })).toBe(true)
}

const failureBanner = (page: Page) => page.locator('.mantine-Alert-root.scan-batch-banner')

// A fade can still be running when the text is already correct. Wait out finite
// banner animations, and ignore anything that loops.
const settleBanner = async (page: Page) => {
  await page.evaluate(async () => {
    const nodes = [...document.querySelectorAll('.scan-batch-banner, .scan-batch-partial')]
    const pending = nodes.flatMap((node) => node.getAnimations({ subtree: true })).filter((animation) => {
      return animation.effect?.getComputedTiming().iterations !== Infinity
    })
    await Promise.all(pending.map((animation) => animation.finished.catch(() => undefined)))
  })
}

const saveShot = async (page: Page, path: string) => {
  await settleBanner(page)
  await page.screenshot({ path })
}

const expectSuccessBanner = async (page: Page, line: string) => {
  const banner = page.locator('.scan-batch-banner--success')
  await expect(banner).toHaveCount(1)
  await expect(banner).toHaveText(line)
  await expect(banner).toHaveAttribute('role', 'status')
  await expect(banner).not.toHaveAttribute('role', 'alert')
  await expect(failureBanner(page)).toHaveCount(0)
  await expect.poll(() => banner.evaluate((node) => {
    const style = getComputedStyle(node)
    const probe = document.createElement('div')
    const accent = getComputedStyle(document.documentElement).getPropertyValue('--accent-border').trim()
    probe.style.borderTop = `1px solid ${accent}`
    document.body.appendChild(probe)
    const expected = getComputedStyle(probe).borderTopColor
    probe.remove()
    return style.borderTopWidth === '1px' && style.borderTopStyle === 'solid' && style.borderTopColor === expected
  })).toBe(true)
  await expect(page.locator('.restock-toast').filter({ hasText: line })).toHaveCount(0)
  await fitsInViewport(banner)
}

const expectFailureWithPartial = async (page: Page, line: string) => {
  const failure = failureBanner(page)
  const partial = failure.locator('.scan-batch-partial')
  await expect(failure).toHaveCount(1)
  await expect(failure).toContainText(fallback)
  await expect(partial).toHaveCount(1)
  await expect(partial).toHaveText(line)
  await expect(partial).not.toHaveAttribute('role', 'alert')
  await expect(page.locator('.scan-batch-banner--success')).toHaveCount(0)
  await expect(page.locator('.restock-toast').filter({ hasText: line })).toHaveCount(0)
  await expect.poll(() => page.evaluate((sentence) => {
    const alert = document.querySelector('.mantine-Alert-root.scan-batch-banner')
    const note = alert?.querySelector('.scan-batch-partial')
    const head = [...(alert?.querySelectorAll('div') ?? [])].find((element) => element.textContent === 'Some scans could not be confirmed.')
    if (alert === null || alert === undefined || note === null || note === undefined || head === undefined) return false
    const follows = (head.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
    const oneAlert = document.querySelectorAll('.mantine-Alert-root.scan-batch-banner').length === 1
    return alert.contains(note)
      && note.textContent === sentence
      && follows
      && note.getAttribute('role') !== 'alert'
      && oneAlert
      && document.querySelectorAll('.scan-batch-banner--success').length === 0
  }, line)).toBe(true)
  await fitsInViewport(failure)
  await fitsInViewport(partial)
}

// Amber cards are not batch failures. A partial line still uses the teal banner.
// With nothing to say, there is no banner.
const expectAmberLeftoverBanner = async (page: Page, line: string) => {
  await expectSuccessBanner(page, line)
  await expect(page.getByText(fallback)).toHaveCount(0)
}

const expectNoBatchBanner = async (page: Page) => {
  await expect(page.locator('.scan-batch-banner--success')).toHaveCount(0)
  await expect(failureBanner(page)).toHaveCount(0)
  await expect(page.getByText(fallback)).toHaveCount(0)
}

const expectServerSentence = async (page: Page, sentence: string, failedBarcode: string) => {
  const failure = failureBanner(page)
  await expect(failure).toHaveCount(1)
  await expect(page.locator('.scan-batch-banner--success')).toHaveCount(0)
  await expect(page.getByText(fallback)).toHaveCount(0)
  await expect.poll(() => page.evaluate((expected) => {
    const alert = document.querySelector('.mantine-Alert-root.scan-batch-banner')
    const head = [...(alert?.querySelectorAll('div') ?? [])].find((element) => element.textContent === expected)
    return head !== undefined
      && document.querySelectorAll('.mantine-Alert-root.scan-batch-banner').length === 1
      && document.querySelectorAll('.scan-batch-banner--success').length === 0
  }, sentence)).toBe(true)
  const missed = page.getByRole('article', { name: `Scan ${failedBarcode}`, exact: true })
  await expect(missed.getByText(sentence, { exact: true })).toHaveCount(0)
  await fitsInViewport(failure)
}

for (const viewport of viewports) {
  test(`the batch banner fits the partial line at ${viewport.width}px`, async ({ page, request }) => {
    test.setTimeout(120_000)
    await page.setViewportSize(viewport)
    const chipsBarcode = `301${viewport.width}01`
    const missBarcode = `301${viewport.width}02`
    const scans = await seedStockOuts(page, request, [
      { barcode: chipsBarcode, name: 'Tortilla chips' },
      { barcode: missBarcode, name: `Short ${viewport.width}` },
    ])
    const chips = scans[0]
    const miss = scans[1]
    if (chips === undefined || miss === undefined) throw new Error('missing seeded scans')
    try {
      const outcome = batchCommitResponse(page)
      await page.route('**/api/scans/batch-commit', async (route) => {
        if (route.request().method() !== 'POST') {
          await route.continue()
          return
        }
        await clearCommittedScans(page, route, scans, [chips.id])
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            updatedCount: 2,
            committedIds: [chips.id],
            failedIds: [miss.id],
            failedReasons: [instanceGone],
            failedCodes: ['instance_gone'],
            stockOutCounts: {
              [chips.id]: { removedCount: 1, requestedCount: 3 },
            },
          }),
        })
      })
      for (const scan of scans) {
        const card = page.getByRole('article', { name: `Scan ${scan.barcode}`, exact: true })
        await card.getByRole('checkbox', { name: 'Select scan for batch approval' }).check()
      }
      await page.getByRole('button', { name: 'Confirm 2 selected scans' }).click()
      await outcome
      await expectFailureWithPartial(page, wasLine)
      const missed = page.getByRole('article', { name: `Scan ${missBarcode}`, exact: true })
      await expect(missed).toContainText(instanceGone)
      await expect(missed.getByText(failureNote)).toHaveCount(0)
      if (shotDir !== undefined) {
        await saveShot(page, `${shotDir}/banner_mixed_${viewport.width}_light.png`)
      }
    } finally {
      await page.unroute('**/api/scans/batch-commit')
      await cancelScan(request, miss.id).catch(() => undefined)
    }
  })
}

test('the batch banner shows the server sentence', async ({ page, request }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 1280, height: 800 })
  const cases = [
    {
      name: 'many-busy',
      items: [
        { barcode: '330128001', name: 'Kept oats' },
        { barcode: '330128002', name: 'Kept rice' },
        { barcode: '330128003', name: 'Stopped loaf' },
      ],
      failedBarcode: '330128003',
      sentence: `2 confirmed. ${busySentence}`,
      body: (scans: KnownScan[]) => ({
        updatedCount: 3,
        committedIds: [scans[0]?.id, scans[1]?.id],
        failedIds: [scans[2]?.id],
        failedReasons: [busySentence],
        failedCodes: ['busy'],
      }),
    },
    {
      name: 'none-busy',
      items: [
        { barcode: '330128011', name: 'First wait' },
        { barcode: '330128012', name: 'Second wait' },
      ],
      failedBarcode: '330128011',
      sentence: busySentence,
      body: (scans: KnownScan[]) => ({
        updatedCount: 0,
        committedIds: [],
        failedIds: scans.map((scan) => scan.id),
        failedReasons: scans.map(() => busySentence),
        failedCodes: scans.map(() => 'busy'),
      }),
    },
    {
      name: 'one-busy',
      items: [
        { barcode: '330128021', name: 'Kept beans' },
        { barcode: '330128022', name: 'Busy beans' },
      ],
      failedBarcode: '330128022',
      sentence: `1 confirmed. ${busySentence}`,
      body: (scans: KnownScan[]) => ({
        updatedCount: 1,
        committedIds: [scans[0]?.id],
        failedIds: [scans[1]?.id],
        failedReasons: [busySentence],
        failedCodes: ['busy'],
      }),
    },
    {
      name: 'none-space',
      items: [
        { barcode: '330128031', name: 'Full disk' },
        { barcode: '330128032', name: 'Also full' },
      ],
      failedBarcode: '330128031',
      sentence: noSpaceSentence,
      body: (scans: KnownScan[]) => ({
        updatedCount: 0,
        committedIds: [],
        failedIds: scans.map((scan) => scan.id),
        failedReasons: scans.map(() => noSpaceSentence),
        failedCodes: scans.map(() => 'no_space'),
      }),
    },
  ] as const
  for (const sample of cases) {
    const scans = await seedStockOuts(page, request, [...sample.items])
    try {
      await confirmBatch(page, scans, () => sample.body(scans))
      await expectServerSentence(page, sample.sentence, sample.failedBarcode)
    } finally {
      await page.unroute('**/api/scans/batch-commit')
      for (const scan of scans) {
        await cancelScan(request, scan.id).catch(() => undefined)
      }
    }
  }
})

test('confirming one card clears the busy banner and shows that card', async ({ page, request }) => {
  test.setTimeout(180_000)
  const schemes = shotDir === undefined ? ['light'] as const : ['light', 'dark'] as const
  for (const scheme of schemes) {
    await page.emulateMedia({ colorScheme: scheme })
    await page.setViewportSize({ width: 390, height: 844 })
    const prefix = scheme === 'dark' ? '33039011' : '33039001'
    const scans = await seedStockOuts(page, request, [
      { barcode: `${prefix}1`, name: 'Busy oats' },
      { barcode: `${prefix}2`, name: 'Busy rice' },
    ])
    const confirmed = scans[0]
    const waiting = scans[1]
    if (confirmed === undefined || waiting === undefined) throw new Error('missing busy scans')
    try {
      await confirmBatch(page, scans, () => ({
        updatedCount: 0,
        committedIds: [],
        failedIds: scans.map((scan) => scan.id),
        failedReasons: scans.map(() => busySentence),
        failedCodes: scans.map(() => 'busy'),
      }))
      await expectServerSentence(page, busySentence, confirmed.barcode)
      await page.route(`**/api/scans/${confirmed.id}/commit`, async (route) => {
        if (route.request().method() !== 'POST') {
          await route.continue()
          return
        }
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({ error: failureNote, code: 'none_on_hand' }),
        })
      })
      const card = page.getByRole('article', { name: `Scan ${confirmed.barcode}`, exact: true })
      await card.getByRole('button', { name: 'Confirm' }).click()
      const own = page.getByRole('article', { name: `Scan ${confirmed.barcode}, ${failureNote}` })
      await expect(own).toBeVisible()
      await expect(own.getByText(failureNote, { exact: true })).toBeVisible()
      await expectNoBatchBanner(page)
      await expect(page.getByText(busySentence)).toHaveCount(0)
      await expect(page.getByRole('article', { name: `Scan ${waiting.barcode}`, exact: true })).toBeVisible()
      if (shotDir !== undefined) {
        await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
        await saveShot(page, `${shotDir}/single_confirm_390_${scheme}.png`)
      }
    } finally {
      await page.unroute('**/api/scans/batch-commit')
      await page.unroute(`**/api/scans/${confirmed.id}/commit`)
      for (const scan of scans) {
        await cancelScan(request, scan.id).catch(() => undefined)
      }
    }
  }
})

test.describe('batch banner screenshots', () => {
  test.skip(shotDir === undefined, 'set BATCH_BANNER_SHOTS to save viewport shots')

  test('saves none, one partial, multiple partial, and mixed at both widths and schemes', async ({ page, request }) => {
    test.setTimeout(300_000)
    const schemes = ['light', 'dark'] as const
    let serial = 0
    for (const viewport of viewports) {
      for (const scheme of schemes) {
        serial += 1
        await page.emulateMedia({ colorScheme: scheme })
        await page.setViewportSize(viewport)
        // Digits only. A letter, or more than 14 digits, is stored as a QR card.
        const prefix = `${viewport.width}${scheme === 'dark' ? 1 : 0}${serial}`

        const one = await seedStockOuts(page, request, [
          { barcode: `${prefix}01`, name: 'Tortilla chips' },
        ])
        await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
        const oneScan = one[0]
        if (oneScan === undefined) throw new Error('missing one-partial scan')
        await confirmBatch(page, one, (ids) => ({
          updatedCount: 1,
          committedIds: [ids.get(oneScan.barcode)],
          stockOutCounts: {
            [oneScan.id]: { removedCount: 1, requestedCount: 3 },
          },
        }))
        await expectSuccessBanner(page, wasLine)
        await saveShot(page, `${shotDir}/banner_one_partial_${viewport.width}_${scheme}.png`)
        await page.unroute('**/api/scans/batch-commit')

        const manyA = `${prefix}11`
        const manyB = `${prefix}12`
        const many = await seedStockOuts(page, request, [
          { barcode: manyA, name: 'Tortilla chips' },
          { barcode: manyB, name: 'Rolled oats' },
        ])
        const left = many[0]
        const right = many[1]
        if (left === undefined || right === undefined) throw new Error('missing multi scans')
        await confirmBatch(page, many, () => ({
          updatedCount: 2,
          committedIds: [left.id, right.id],
          stockOutCounts: {
            [left.id]: { removedCount: 1, requestedCount: 3 },
            [right.id]: { removedCount: 2, requestedCount: 4 },
          },
        }))
        await expectSuccessBanner(page, manyLine)
        await expect(page.getByText(wasLine)).toHaveCount(0)
        await saveShot(page, `${shotDir}/banner_multiple_partial_${viewport.width}_${scheme}.png`)
        await page.unroute('**/api/scans/batch-commit')

        const fullBarcode = `${prefix}21`
        const noneBarcode = `${prefix}22`
        const none = await seedStockOuts(page, request, [
          { barcode: fullBarcode, name: 'Batch Full' },
          { barcode: noneBarcode, name: 'Batch Empty' },
        ])
        const full = none[0]
        const empty = none[1]
        if (full === undefined || empty === undefined) throw new Error('missing none scans')
        await confirmBatch(page, none, () => ({
          updatedCount: 2,
          committedIds: [full.id],
          failedIds: [empty.id],
          failedReasons: [failureNote],
          failedCodes: ['none_on_hand'],
          stockOutCounts: {
            [full.id]: { removedCount: 2, requestedCount: 2 },
          },
        }))
        await expect(page.getByText(failureNote)).toBeVisible()
        await expectNoBatchBanner(page)
        await expect(page.getByText(wasLine)).toHaveCount(0)
        await expect(page.getByText(manyLine)).toHaveCount(0)
        await saveShot(page, `${shotDir}/banner_none_${viewport.width}_${scheme}.png`)
        await page.unroute('**/api/scans/batch-commit')
        await cancelScan(request, empty.id)

        const mixedChips = `${prefix}31`
        const mixedMiss = `${prefix}32`
        const mixed = await seedStockOuts(page, request, [
          { barcode: mixedChips, name: 'Tortilla chips' },
          { barcode: mixedMiss, name: 'Short loaf' },
        ])
        const mixedFull = mixed[0]
        const mixedEmpty = mixed[1]
        if (mixedFull === undefined || mixedEmpty === undefined) throw new Error('missing mixed scans')
        await confirmBatch(page, mixed, () => ({
          updatedCount: 2,
          committedIds: [mixedFull.id],
          failedIds: [mixedEmpty.id],
          failedReasons: [failureNote],
          failedCodes: ['none_on_hand'],
          stockOutCounts: {
            [mixedFull.id]: { removedCount: 1, requestedCount: 3 },
          },
        }))
        await expectAmberLeftoverBanner(page, wasLine)
        await expect(page.getByText(failureNote)).toBeVisible()
        await saveShot(page, `${shotDir}/banner_mixed_${viewport.width}_${scheme}.png`)
        await page.unroute('**/api/scans/batch-commit')
        await cancelScan(request, mixedEmpty.id)
      }
    }
  })

  test('saves the server sentence at both widths and schemes', async ({ page, request }) => {
    test.setTimeout(300_000)
    const schemes = ['light', 'dark'] as const
    const shots = [
      {
        slug: 'm2_busy',
        sentence: `2 confirmed. ${busySentence}`,
        items: (prefix: string) => [
          { barcode: `${prefix}41`, name: 'Kept oats' },
          { barcode: `${prefix}42`, name: 'Kept rice' },
          { barcode: `${prefix}43`, name: 'Stopped loaf' },
        ],
        body: (scans: KnownScan[]) => ({
          updatedCount: 2,
          committedIds: [scans[0]?.id, scans[1]?.id],
          failedIds: [scans[2]?.id],
          failedReasons: [busySentence],
          failedCodes: ['busy'],
        }),
        failedIndex: 2,
      },
      {
        slug: 'm0_busy',
        sentence: busySentence,
        items: (prefix: string) => [
          { barcode: `${prefix}51`, name: 'First wait' },
          { barcode: `${prefix}52`, name: 'Second wait' },
        ],
        body: (scans: KnownScan[]) => ({
          updatedCount: 0,
          committedIds: [],
          failedIds: scans.map((scan) => scan.id),
          failedReasons: scans.map(() => busySentence),
          failedCodes: scans.map(() => 'busy'),
        }),
        failedIndex: 0,
      },
      {
        slug: 'busy',
        sentence: `1 confirmed. ${busySentence}`,
        items: (prefix: string) => [
          { barcode: `${prefix}61`, name: 'Kept beans' },
          { barcode: `${prefix}62`, name: 'Busy beans' },
        ],
        body: (scans: KnownScan[]) => ({
          updatedCount: 1,
          committedIds: [scans[0]?.id],
          failedIds: [scans[1]?.id],
          failedReasons: [busySentence],
          failedCodes: ['busy'],
        }),
        failedIndex: 1,
      },
      {
        slug: 'no_space',
        sentence: noSpaceSentence,
        items: (prefix: string) => [
          { barcode: `${prefix}71`, name: 'Full disk' },
          { barcode: `${prefix}72`, name: 'Also full' },
        ],
        body: (scans: KnownScan[]) => ({
          updatedCount: 0,
          committedIds: [],
          failedIds: scans.map((scan) => scan.id),
          failedReasons: scans.map(() => noSpaceSentence),
          failedCodes: scans.map(() => 'no_space'),
        }),
        failedIndex: 0,
      },
    ]
    let serial = 40
    for (const viewport of viewports) {
      for (const scheme of schemes) {
        serial += 1
        await page.emulateMedia({ colorScheme: scheme })
        await page.setViewportSize(viewport)
        const prefix = `${viewport.width}${scheme === 'dark' ? 1 : 0}${serial}`
        for (const shot of shots) {
          const scans = await seedStockOuts(page, request, shot.items(prefix))
          const failed = scans[shot.failedIndex]
          if (failed === undefined) throw new Error(`missing ${shot.slug} scan`)
          try {
            await confirmBatch(page, scans, () => shot.body(scans))
            await expectServerSentence(page, shot.sentence, failed.barcode)
            await saveShot(page, `${shotDir}/banner_${shot.slug}_${viewport.width}_${scheme}.png`)
          } finally {
            await page.unroute('**/api/scans/batch-commit')
            for (const scan of scans) {
              await cancelScan(request, scan.id).catch(() => undefined)
            }
          }
        }
      }
    }
  })
})
