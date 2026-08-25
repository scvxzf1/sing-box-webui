import { expect, test, type Page } from '@playwright/test'

const storageKey = 'sing-box-webui:ui-preferences-v1'
const views = [
  'overview',
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
  'settings',
] as const

type View = (typeof views)[number]

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => window.localStorage.setItem('sing-box-webui:theme', 'dark'))
})

test('dark theme audit round 1: desktop views keep readable semantic layers', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 })
  await page.goto('/#overview')

  const colors = await page.evaluate(() => {
    const style = getComputedStyle(document.documentElement)
    const token = (name: string) => style.getPropertyValue(name).trim()
    return {
      theme: document.documentElement.dataset.theme,
      page: token('--page'),
      surface: token('--surface'),
      raised: token('--surface-raised'),
      inset: token('--surface-inset'),
      ink: token('--ink'),
      muted: token('--muted'),
      border: token('--border'),
      borderStrong: token('--border-strong'),
      green: token('--green'),
      accent: token('--accent'),
      red: token('--red'),
    }
  })

  expect(colors.theme).toBe('dark')
  expect(new Set([colors.page, colors.surface, colors.raised, colors.inset]).size).toBe(4)
  expect(new Set([colors.green, colors.accent, colors.red]).size).toBe(3)
  expect(contrast(colors.ink, colors.page)).toBeGreaterThanOrEqual(12)
  expect(contrast(colors.muted, colors.page)).toBeGreaterThanOrEqual(6)
  expect(contrast(colors.border, colors.surface)).toBeGreaterThanOrEqual(1.3)
  expect(contrast(colors.borderStrong, colors.surface)).toBeGreaterThanOrEqual(1.8)

  for (const view of views) {
    await openView(page, view)
    const state = await page.evaluate((currentView) => {
      const content = document.querySelector(`.content--${currentView}`)
      const bodyStyle = getComputedStyle(document.body)
      const contentStyle = content ? getComputedStyle(content) : null
      return {
        bodyBackgroundImage: bodyStyle.backgroundImage,
        contentColor: contentStyle?.color,
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      }
    }, view)
    expect(state.bodyBackgroundImage, `${view} should use a quiet solid background`).toBe('none')
    expect(state.overflow, `${view} should not overflow at desktop width`).toBe(0)
  }
})

test('dark theme audit round 2: scale combinations preserve width and depth', async ({ page }) => {
  for (const setup of [
    { width: 2491, height: 1437, pageScale: 75, fontScale: 100 },
    { width: 1366, height: 768, pageScale: 100, fontScale: 100 },
    { width: 1366, height: 768, pageScale: 115, fontScale: 130 },
  ]) {
    await page.setViewportSize(setup)
    await setScale(page, setup.pageScale, setup.fontScale)

    for (const view of views) {
      await openView(page, view)
      const layout = await page.evaluate((currentView) => {
        const content = document.querySelector(`.content--${currentView}`)?.getBoundingClientRect()
        const scroll = document.querySelector('.content-scroll')?.getBoundingClientRect()
        const panel = document.querySelector(`.content--${currentView} .panel, .content--${currentView} .connection-step, .content--${currentView} .traffic-policy-section`)
        const root = getComputedStyle(document.documentElement)
        return {
          coverage: content && scroll ? content.width / scroll.width : 0,
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
          panelBackground: panel ? getComputedStyle(panel).backgroundColor : null,
          surface: root.getPropertyValue('--surface').trim(),
        }
      }, view)
      expect(layout.overflow, `${view} should not overflow for ${JSON.stringify(setup)}`).toBe(0)
      expect(layout.coverage, `${view} should keep using available width`).toBeGreaterThanOrEqual(0.96)
      if (layout.panelBackground) expect(layout.panelBackground).not.toBe('rgba(0, 0, 0, 0)')
    }
  }
})

test('dark theme audit round 3: mobile and interaction states remain distinct', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await setScale(page, 100, 100)

  for (const view of views) {
    await openView(page, view)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow, `${view} should not overflow on mobile`).toBe(0)
  }

  await openView(page, 'nodes')
  const activeNavigation = page.locator('.nav-item--active')
  await expect(activeNavigation).toBeVisible()
  const navColors = await activeNavigation.evaluate((element) => {
    const style = getComputedStyle(element)
    const sidebar = getComputedStyle(document.querySelector('.sidebar') as Element)
    return { active: style.backgroundColor, sidebar: sidebar.backgroundColor }
  })
  expect(navColors.active).not.toBe(navColors.sidebar)

  const search = page.getByPlaceholder('搜索名称、协议或服务器')
  await search.focus()
  expect(await search.evaluate((element) => getComputedStyle(element).boxShadow)).not.toBe('none')

  const disabledButton = page.locator('button:disabled').first()
  await expect(disabledButton).toBeVisible()
  expect(Number(await disabledButton.evaluate((element) => getComputedStyle(element).opacity))).toBeGreaterThanOrEqual(0.6)

  const nodeRegions = await page.locator('.node-card').first().locator('.node-card-region').evaluateAll((elements) => (
    elements.map((element) => getComputedStyle(element).backgroundColor)
  ))
  expect(new Set(nodeRegions).size).toBeGreaterThanOrEqual(2)

  await openView(page, 'connection')
  const dragHandle = page.locator('.connection-layout-handle').first()
  await dragHandle.hover()
  expect(Number(await dragHandle.evaluate((element) => getComputedStyle(element).opacity))).toBeGreaterThanOrEqual(0.75)

  await openView(page, 'nodes')
  await page.getByRole('button', { name: '导入节点' }).click()
  const dialog = page.locator('.node-import-dialog')
  await expect(dialog).toBeVisible()
  const dialogState = await dialog.evaluate((element) => {
    const style = getComputedStyle(element)
    const backdrop = getComputedStyle(element.parentElement as Element)
    return { background: style.backgroundColor, border: style.borderColor, backdrop: backdrop.backgroundColor }
  })
  expect(dialogState.background).not.toBe(dialogState.backdrop)
  expect(dialogState.border).not.toBe('rgba(0, 0, 0, 0)')
})

async function openView(page: Page, view: View) {
  await page.goto(`/#${view}`)
  await expect(page.locator(`.content--${view}`)).toBeVisible()
}

async function setScale(page: Page, pageScale: number, fontScale: number) {
  await page.goto('/#overview')
  await page.evaluate(({ key, order, pageValue, fontValue }) => {
    window.localStorage.setItem(key, JSON.stringify({
      sidebarWidth: 220,
      navigationOrder: order.filter((view) => view !== 'settings'),
      hiddenNavigation: [],
      viewScales: Object.fromEntries(order.map((view) => [view, pageValue])),
      fontScales: Object.fromEntries(order.map((view) => [view, fontValue])),
    }))
  }, { key: storageKey, order: views, pageValue: pageScale, fontValue: fontScale })
}

function contrast(foreground: string, background: string) {
  const foregroundLuminance = luminance(foreground)
  const backgroundLuminance = luminance(background)
  return (Math.max(foregroundLuminance, backgroundLuminance) + 0.05)
    / (Math.min(foregroundLuminance, backgroundLuminance) + 0.05)
}

function luminance(color: string) {
  const value = color.replace('#', '')
  const channels = [0, 2, 4].map((index) => Number.parseInt(value.slice(index, index + 2), 16) / 255)
  const [red, green, blue] = channels.map((channel) => (
    channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
  ))
  return red * 0.2126 + green * 0.7152 + blue * 0.0722
}
