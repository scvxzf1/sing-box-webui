import { expect, test, type Page } from '@playwright/test'

const storageKey = 'sing-box-webui:ui-preferences-v1'
const views = [
  'connection',
  'subscriptions',
  'links',
  'nodes',
  'pools',
  'chains',
  'channels',
  'rules',
  'traffic',
  'dns',
  'core',
  'overview',
  'settings',
] as const

const gridSelectors: Partial<Record<(typeof views)[number], string[]>> = {
  connection: ['.connection-main', '.connection-diagnostics-row'],
  subscriptions: ['.subscriptions-layout'],
  links: ['.content--links .traffic-status-band'],
  nodes: ['.node-grid'],
  pools: ['.pools-layout'],
  chains: ['.chains-layout'],
  channels: ['.channels-layout'],
  traffic: ['.traffic-status-band', '.traffic-policy-fields'],
  overview: ['.status-grid'],
  settings: ['.global-settings-layout', '.view-scale-grid'],
}

async function setScale(page: Page, scale: number, textScale = 100) {
  await page.evaluate(({ key, navigationOrder, value, fontValue }) => {
    localStorage.setItem(key, JSON.stringify({
      sidebarWidth: 220,
      navigationOrder,
      hiddenNavigation: [],
      viewScales: Object.fromEntries([...navigationOrder, 'settings'].map((view) => [view, value])),
      fontScales: Object.fromEntries([...navigationOrder, 'settings'].map((view) => [view, fontValue])),
    }))
  }, { key: storageKey, navigationOrder: views.filter((view) => view !== 'settings'), value: scale, fontValue: textScale })
  await page.reload()
}

async function captureLayout(page: Page, view: (typeof views)[number]) {
  await page.goto(`/#${view}`)
  await expect(page.locator(`.content--${view}`)).toBeVisible()
  return page.evaluate(({ currentView, selectors }) => {
    const content = document.querySelector(`.content--${currentView}`)?.getBoundingClientRect()
    const scroll = document.querySelector('.content-scroll')?.getBoundingClientRect()
    const heading = document.querySelector('.page-heading h1')
    const columns = Object.fromEntries((selectors ?? []).flatMap((selector) => {
      const element = document.querySelector(selector)
      if (!element || getComputedStyle(element).display !== 'grid') return []
      return [[selector, getComputedStyle(element).gridTemplateColumns.split(' ').filter(Boolean).length]]
    }))
    return {
      coverage: content && scroll ? content.width / scroll.width : 0,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      headingFontSize: heading ? Number.parseFloat(getComputedStyle(heading).fontSize) : 0,
      columns,
    }
  }, { currentView: view, selectors: gridSelectors[view] })
}

test('page scaling preserves usable width and desktop information density', async ({ page }) => {
  await page.setViewportSize({ width: 2491, height: 1437 })
  await page.goto('/')

  await setScale(page, 100)
  const baseline: Partial<Record<(typeof views)[number], Awaited<ReturnType<typeof captureLayout>>>> = {}
  for (const view of views) baseline[view] = await captureLayout(page, view)

  for (const scale of [75, 85, 115, 130]) {
    await setScale(page, scale)
    for (const view of views) {
      const scaled = await captureLayout(page, view)
      expect(scaled.overflow, `${view} should not overflow horizontally at ${scale}%`).toBe(0)
      expect(scaled.coverage, `${view} should continue to use the available page width at ${scale}%`).toBeGreaterThanOrEqual(0.96)
      if (scale < 100) {
        for (const [selector, columns] of Object.entries(scaled.columns)) {
          expect(columns, `${view} ${selector} should not lose columns at ${scale}%`).toBeGreaterThanOrEqual(baseline[view]?.columns[selector] ?? 0)
        }
      }
    }
  }

  await setScale(page, 100, 130)
  for (const view of views) {
    const scaled = await captureLayout(page, view)
    expect(scaled.headingFontSize, `${view} heading should respond to font scaling`).toBeGreaterThanOrEqual((baseline[view]?.headingFontSize ?? 0) * 1.25)
    expect(scaled.overflow, `${view} should not overflow horizontally with 130% fonts`).toBe(0)
  }
})

test('connection items auto-arrange without overlap and preserve drag order', async ({ page }) => {
  await page.setViewportSize({ width: 2491, height: 1437 })
  await page.goto('/#connection')
  await expect(page.locator('.connection-layout-item')).toHaveCount(7)

  const source = page.getByRole('button', { name: '调整 节点质量检测 位置' })
  const target = page.getByRole('group', { name: '连接目标，可拖动排序' })
  await source.dragTo(target, { targetPosition: { x: 20, y: 20 } })
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem('sing-box-webui:connection-layout-order-v1') ?? '[]'))).toEqual([
    'quality', 'target', 'selection', 'mode', 'lan', 'exit', 'quick',
  ])
  await page.reload()
  await expect(source).toHaveCSS('order', '0')
  await expect(target).toHaveCSS('order', '1')
  await page.evaluate(() => {
    localStorage.setItem('sing-box-webui:connection-layout-order-v1', JSON.stringify([
      'target', 'selection', 'mode', 'lan', 'quality', 'exit', 'quick',
    ]))
  })
  await page.reload()

  for (const setup of [
    { width: 2491, height: 1437, scale: 100, fontScale: 100, minimumColumns: 4 },
    { width: 1366, height: 768, scale: 100, fontScale: 100, minimumColumns: 2 },
    { width: 1024, height: 768, scale: 100, fontScale: 100, minimumColumns: 1 },
    { width: 1024, height: 768, scale: 75, fontScale: 130, minimumColumns: 2 },
    { width: 390, height: 844, scale: 100, fontScale: 130, minimumColumns: 1 },
  ]) {
    await page.setViewportSize(setup)
    await setScale(page, setup.scale, setup.fontScale)
    await page.goto('/#connection')
    const layout = await page.evaluate(() => {
      const grid = document.querySelector('.connection-main')
      const items = [...document.querySelectorAll('.connection-layout-item')].map((element) => {
        const box = element.getBoundingClientRect()
        return { id: element.getAttribute('data-layout-id'), left: box.left, right: box.right, top: box.top, bottom: box.bottom }
      })
      const overlaps = items.flatMap((item, index) => items.slice(index + 1).filter((other) => (
        Math.min(item.right, other.right) - Math.max(item.left, other.left) > 1
        && Math.min(item.bottom, other.bottom) - Math.max(item.top, other.top) > 1
      )).map((other) => [item.id, other.id]))
      const quick = items.find((item) => item.id === 'quick')
      const quality = items.find((item) => item.id === 'quality')
      const exit = items.find((item) => item.id === 'exit')
      const gridBox = grid?.getBoundingClientRect()
      return {
        columns: grid ? getComputedStyle(grid).gridTemplateColumns.split(' ').filter(Boolean).length : 0,
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        overlaps,
        quickCoverage: quick && gridBox ? quick.right - quick.left >= gridBox.width - 2 : false,
        diagnosticsShareRow: quality && exit ? Math.abs(quality.top - exit.top) <= 1 : false,
      }
    })
    expect(layout.overflow, `connection should not overflow at ${setup.width}px and ${setup.scale}%`).toBe(0)
    expect(layout.overlaps, `connection items should not overlap at ${setup.width}px`).toEqual([])
    expect(layout.columns, JSON.stringify(setup)).toBeGreaterThanOrEqual(setup.minimumColumns)
    expect(layout.quickCoverage).toBe(true)
    expect(layout.diagnosticsShareRow).toBe(setup.minimumColumns > 1)
  }
})
