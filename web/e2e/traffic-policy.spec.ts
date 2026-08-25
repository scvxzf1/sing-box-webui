import { expect, test } from '@playwright/test'

const pool = {
  id: 'download-pool', name: '高速下载池', members: [], memberCount: 3, availableCount: 3,
  probeIntervalSeconds: 60, toleranceMs: 80, probeUrl: 'https://example.com', fallbackProbeUrls: [],
  idleTimeoutSeconds: 1800, highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2,
  maxBackoffSeconds: 300, interruptExistingConnections: true,
  createdAt: '2026-08-24T12:00:00Z', updatedAt: '2026-08-24T12:00:00Z',
}

const policy = {
  enabled: true, downloadPoolId: pool.id, triggerRateBytesPerSecond: 5 << 20, triggerDurationSeconds: 5,
  releaseRateBytesPerSecond: 1 << 20, releaseDurationSeconds: 60, cooldownSeconds: 600,
  state: 'monitoring', currentDownloadBps: 3 << 20, activeConnections: 4, triggerProgressSeconds: 2,
  releaseProgressSeconds: 0, events: [],
}

test('renders the live traffic policy without viewport overflow', async ({ page }) => {
  for (const viewport of [{ width: 1366, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    await page.goto('/#traffic')
    await expect(page.getByRole('heading', { name: '流量策略' })).toBeVisible()
    await expect(page.getByRole('region', { name: '流量策略状态' })).toBeVisible()
    await expect(page.getByRole('heading', { name: '下载代理策略' })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
  }
})

test('separates live status from the editable policy draft', async ({ page }) => {
  let savedPolicy = { ...policy }
  await page.route('**/api/v1/traffic-policy', async (route) => {
    if (route.request().method() === 'PUT') {
      savedPolicy = { ...savedPolicy, ...route.request().postDataJSON() }
    }
    await route.fulfill({ json: savedPolicy })
  })
  await page.route('**/api/v1/pools', async (route) => route.fulfill({ json: { items: [pool] } }))

  await page.goto('/#traffic')
  const status = page.getByRole('region', { name: '流量策略状态' })
  await expect(status).toContainText('2 / 5 秒')
  await expect(page.getByRole('button', { name: '保存策略' })).toBeDisabled()

  await page.getByLabel('持续时间').fill('30')
  await expect(status).toContainText('2 / 5 秒')
  await expect(page.getByRole('button', { name: '放弃更改' })).toBeEnabled()
  await page.getByRole('button', { name: '放弃更改' }).click()
  await expect(page.getByLabel('持续时间')).toHaveValue('5')

  await page.getByLabel('回落速率').fill('6')
  await expect(page.getByRole('alert')).toContainText('回落速率必须低于触发速率')
  await expect(page.getByRole('button', { name: '保存策略' })).toBeDisabled()

  await page.getByLabel('回落速率').fill('1')
  await page.getByLabel('持续时间').fill('30')
  await page.getByRole('button', { name: '保存策略' }).click()
  await expect.poll(() => savedPolicy.triggerDurationSeconds).toBe(30)
  await expect(page.getByRole('button', { name: '保存策略' })).toBeDisabled()
})
