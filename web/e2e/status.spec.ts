import { expect, test } from '@playwright/test'

test('shows the local control-plane status', async ({ page }) => {
  await page.goto('/')

  await expect(page).toHaveTitle('sing-box WebUI · 本机代理控制面')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  await expect(page.getByText('Web API')).toBeVisible()
  await expect(page.getByText('127.0.0.1:31334').first()).toBeVisible()
})

test('switches and persists the dark theme without narrow-screen overflow', async ({ page }) => {
  await page.goto('/')

  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await page.getByRole('button', { name: '切换到深色主题' }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')

  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.getByRole('button', { name: '切换到浅色主题' })).toBeVisible()

  await page.setViewportSize({ width: 360, height: 800 })
  const layout = await page.evaluate(() => {
    const header = document.querySelector('.topbar')?.getBoundingClientRect()
    const actions = document.querySelector('.topbar-actions')?.getBoundingClientRect()
    return {
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      alignedRight: Boolean(header && actions && header.right - actions.right <= 16),
    }
  })
  expect(layout.overflow).toBe(0)
  expect(layout.alignedRight).toBe(true)
})

test('navigates subscription, node and connection workflows', async ({ page }) => {
  await page.route('**/api/v1/subscriptions', async (route) => {
    await route.fulfill({ json: { items: [] } })
  })
  await page.route('**/api/v1/runtime', async (route) => {
    await route.fulfill({
      json: {
        state: 'stopped',
        capabilities: {
          singBox: { available: false, detail: 'sing-box 核心不可用' },
          systemProxy: { available: true, detail: '系统代理可用' },
          tun: { available: false, detail: 'TUN 未启用' },
        },
      },
    })
  })
  await page.route('**/api/v1/pools', async (route) => route.fulfill({ json: { items: [] } }))
  await page.goto('/')

  await page.getByRole('button', { name: '订阅' }).click()
  await expect(page.getByRole('heading', { name: '订阅管理' })).toBeVisible()
  await expect(page.getByText('暂无订阅')).toBeVisible()
  await page.getByRole('button', { name: '添加订阅' }).click()
  await expect(page.getByRole('heading', { name: '新订阅' })).toBeVisible()

  await page.getByRole('button', { name: '节点', exact: true }).click()
  await expect(page.getByRole('heading', { name: '节点选择' })).toBeVisible()
  await expect(page.getByText('当前订阅没有可用节点')).toBeVisible()

  await page.getByRole('button', { name: '连接' }).click()
  await expect(page.getByRole('heading', { name: '连接与应用' })).toBeVisible()
  await expect(page.getByRole('button', { name: '开启' })).toBeDisabled()
  await expect(page.getByText('sing-box 核心不可用')).toBeVisible()
})

test('renders a reported URL beside the resolved host', async ({ page }) => {
  await page.route('**/api/v1/links*', async (route) => route.fulfill({ json: {
    running: true,
    updatedAt: '2026-08-05T00:00:00Z',
    stats: { active: 1, total: 1, uploadTotal: 0, downloadTotal: 0, uploadRate: 0, downloadRate: 0, trackedCapacity: 1000 },
    links: [{
      id: 'link-1', host: '203.0.113.10:443', url: 'example.com', network: 'tcp', type: 'mixed',
      upload: 0, download: 0, uploadRate: 0, downloadRate: 0, node: 'Tokyo', active: true,
      firstSeenAt: '2026-08-05T00:00:00Z', lastSeenAt: '2026-08-05T00:00:00Z',
    }],
  } }))
  await page.goto('/#links')

  await expect(page.getByRole('heading', { name: '链接状态' })).toBeVisible()
  await expect(page.getByRole('columnheader', { name: '网址 / 域名' })).toBeVisible()
  await expect(page.getByText('203.0.113.10:443')).toBeVisible()
  await expect(page.getByText('example.com', { exact: true })).toBeVisible()
})

test('manages local and imported routing rules without narrow-screen overlap', async ({ page }) => {
  const rules = [
    {
      id: 'manual-1', name: 'Private direct', enabled: true, origin: 'manual', action: 'direct', supported: true, position: 0,
      conditions: [{ type: 'ip_is_private', values: [] }],
    },
    {
      id: 'sub-rule-1', name: 'rule_set: geoip-cn', enabled: false, origin: 'subscription', action: 'direct', supported: false, position: 0,
      subscriptionId: 'sub-1', subscriptionName: 'Main', unsupportedReason: 'unsupported condition "rule_set"',
      source: '{"rule_set":["geoip-cn"],"outbound":"direct"}',
    },
    { id: 'builtin-global-proxy', name: '全局代理', enabled: true, origin: 'builtin', action: 'proxy', supported: true, position: 1073741824, locked: true },
  ]
  await page.route('**/api/v1/rules', async (route) => route.fulfill({ json: { items: rules } }))
  await page.route('**/api/v1/rule-pools', async (route) => route.fulfill({ json: { items: [{
    id: 'rule-pool-1', name: '常用规则', enabled: true, position: 0,
    rules: [
      { id: 'pool-rule-1', name: '内网直连', enabled: true, action: 'direct', position: 0, conditions: [{ type: 'ip_is_private', values: [] }] },
      { id: 'pool-rule-2', name: '广告阻断', enabled: false, action: 'block', position: 1, conditions: [{ type: 'domain_suffix', values: ['ads.example'] }] },
    ],
  }] } }))
  await page.goto('/#rules')

  await expect(page.getByRole('heading', { name: '规则管理' })).toBeVisible()
  const manualPool = page.getByText('未归类本地规则').locator('..').locator('..')
  await manualPool.getByRole('button', { name: '进入' }).click()
  await expect(page.getByText('Private direct')).toBeVisible()
  await expect(page.getByText('全局代理')).toBeVisible()
  await page.getByRole('button', { name: '新增规则' }).click()
  await expect(page.getByRole('dialog', { name: '新增规则' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '新增规则' })).toBeHidden()

  await page.getByRole('button', { name: '用纯文本编辑 Private direct' }).click()
  await expect(page.getByRole('dialog', { name: '纯文本编辑' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '规则文本' })).toContainText('ip_is_private=true')
  await page.keyboard.press('Escape')
  const localDownloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '下载 Private direct 规则文本' }).click()
  const localDownload = await localDownloadPromise
  expect(localDownload.suggestedFilename()).toMatch(/^Private direct_\d{4}_\d{1,2}_\d{1,2}_\d{1,2}_\d{1,2}\.txt$/)

  await page.getByRole('button', { name: '返回规则池' }).click()
  await expect(page.getByText('常用规则', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '用纯文本编辑规则池 常用规则' }).click()
  await expect(page.getByRole('dialog', { name: '纯文本编辑规则池' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: '规则池文本' })).toContainText('---')
  await page.keyboard.press('Escape')
  const poolDownloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '下载规则池 常用规则' }).click()
  const poolDownload = await poolDownloadPromise
  expect(poolDownload.suggestedFilename()).toMatch(/^常用规则_\d{4}_\d{1,2}_\d{1,2}_\d{1,2}_\d{1,2}\.txt$/)
  const namedPool = page.getByText('常用规则', { exact: true }).locator('..').locator('..')
  await namedPool.getByRole('button', { name: '进入' }).click()
  await expect(page.getByText('内网直连', { exact: true })).toBeVisible()
  await expect(page.getByText('广告阻断', { exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  const poolLayout = await page.evaluate(() => {
    const toolbar = document.querySelector('.rule-group-toolbar')
    const member = document.querySelector('.rule-row')
    return {
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      headerOverflow: toolbar ? toolbar.scrollWidth - toolbar.clientWidth : null,
      memberOverflow: member ? member.scrollWidth - member.clientWidth : null,
    }
  })
  expect(poolLayout).toEqual({ overflow: 0, headerOverflow: 0, memberOverflow: 0 })

  await page.getByRole('tab', { name: '订阅规则 1' }).click()
  await expect(page.getByText('unsupported condition "rule_set"')).toBeVisible()
  await expect(page.getByRole('checkbox', { name: '关' })).toBeDisabled()
  await page.getByRole('button', { name: '用纯文本编辑 rule_set: geoip-cn' }).click()
  await expect(page.getByText(/保存后将创建一条可独立编辑的本地规则/)).toBeVisible()
  await expect(page.getByRole('textbox', { name: '规则文本' })).toContainText('上游原始规则')
  await page.keyboard.press('Escape')
  const subscriptionDownloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '下载 rule_set: geoip-cn 规则文本' }).click()
  const subscriptionDownload = await subscriptionDownloadPromise
  expect(subscriptionDownload.suggestedFilename()).toMatch(/^rule_set_ geoip-cn_\d{4}_\d{1,2}_\d{1,2}_\d{1,2}_\d{1,2}\.txt$/)
  for (const viewport of [{ width: 1024, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    const layout = await page.evaluate(() => {
      const row = document.querySelector('.rule-row--subscription')
      const condition = row?.querySelector('.rule-condition')?.getBoundingClientRect()
      const support = row?.querySelector('.rule-support')?.getBoundingClientRect()
      return {
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        separated: Boolean(condition && support && support.top >= condition.bottom),
      }
    })
    expect(layout.overflow).toBe(0)
    expect(layout.separated).toBe(true)
  }
})

test('builds a cross-subscription pool and selects it as connection target', async ({ page }) => {
  const pool = {
    id: 'pool-1', name: 'Daily failover', memberCount: 2, availableCount: 2,
    probeIntervalSeconds: 60, toleranceMs: 80,
    probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
    fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
    createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
    members: [
      { subscriptionId: 'sub-a', subscriptionName: 'Alpha', nodeId: 'node-a', nodeName: 'Tokyo', type: 'trojan', server: 'tokyo.example.com', port: 443, available: true },
      { subscriptionId: 'sub-b', subscriptionName: 'Beta', nodeId: 'node-b', nodeName: 'London', type: 'vless', server: 'london.example.com', port: 443, available: true },
    ],
  }
  await page.route('**/api/v1/pools', async (route) => route.fulfill({ json: { items: [pool] } }))
  await page.route('**/api/v1/subscriptions', async (route) => route.fulfill({ json: { items: [] } }))
  await page.route('**/api/v1/runtime', async (route) => route.fulfill({ json: {
    state: 'stopped', capabilities: {
      singBox: { available: true, detail: 'sing-box 可执行文件可用' },
      systemProxy: { available: true, detail: '系统代理可用' },
      tun: { available: false, detail: 'TUN 未启用' },
    },
  } }))
  await page.route('**/api/v1/session', async (route) => route.fulfill({ json: { csrfToken: 'test-token' } }))
  await page.route('**/api/v1/subscriptions/sub-a/latency', async (route) => route.fulfill({ json: {
    items: [{ nodeId: 'node-a', name: 'Tokyo', status: 'ok', latencyMs: 32 }],
  } }))
  await page.route('**/api/v1/subscriptions/sub-b/latency', async (route) => route.fulfill({ json: {
    items: [{ nodeId: 'node-b', name: 'London', status: 'ok', latencyMs: 58 }],
  } }))

  await page.goto('/#pools')
  await expect(page.getByText('Daily failover', { exact: true })).toBeVisible()
  await expect(page.getByText('Tokyo', { exact: true })).toBeVisible()
  await expect(page.getByText('London', { exact: true })).toBeVisible()
  await page.getByLabel('池内节点每行列数').selectOption('3')
  await expect(page.getByLabel('池内节点每行列数')).toHaveValue('3')
  await page.reload()
  await expect(page.getByLabel('池内节点每行列数')).toHaveValue('3')

  for (const viewport of [{ width: 1366, height: 768, columns: 3 }, { width: 390, height: 844, columns: 1 }]) {
    await page.setViewportSize(viewport)
    const layout = await page.evaluate(() => {
      const grid = document.querySelector('.pool-members')
      return {
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        columns: grid ? getComputedStyle(grid).gridTemplateColumns.split(' ').length : 0,
      }
    })
    expect(layout.overflow).toBe(0)
    expect(layout.columns).toBe(viewport.columns)
  }

  await page.setViewportSize({ width: 1366, height: 768 })
  const toolbarCenters = await page.evaluate(() => {
    const toolbar = document.querySelector('.pool-member-toolbar')
    return [...(toolbar?.children ?? [])].map((item) => {
      const box = item.getBoundingClientRect()
      return (box.top + box.bottom) / 2
    })
  })
  expect(Math.max(...toolbarCenters) - Math.min(...toolbarCenters)).toBeLessThanOrEqual(1)
  await page.getByRole('button', { name: '测试全部' }).click()
  await expect(page.getByText('32 ms')).toBeVisible()
  await expect(page.getByText('58 ms')).toBeVisible()

  await page.getByRole('button', { name: '连接' }).click()
  await page.locator('.connection-main').getByRole('button', { name: '节点池' }).click()
  await expect(page.getByLabel('选择节点池')).toHaveValue('pool-1')
  await expect(page.getByText('2/2 个成员可用 · 每 60 秒探测')).toBeVisible()
  await page.getByRole('button', { name: '系统代理' }).click()
  await expect(page.getByRole('button', { name: '开启' })).toBeEnabled()

  for (const viewport of [{ width: 1366, height: 768 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBe(0)
  }
})

test('persists the node grid and tests node latency', async ({ page }) => {
  let poolUpdate: { members?: Array<{ subscriptionId: string; nodeId: string }> } | undefined
  let releaseManualTests = () => {}
  const manualTestGate = new Promise<void>((resolve) => { releaseManualTests = resolve })
  let manualTestRequests = 0
  const nodes = [
    { id: 'node-1', name: 'Tokyo', type: 'trojan', server: 'tokyo.example.com', port: 443, tls: true, selected: true },
    { id: 'node-2', name: 'London', type: 'shadowsocks', server: 'london.example.com', port: 8388, tls: false, selected: false },
    ...Array.from({ length: 10 }, (_, index) => ({
      id: `node-${index + 3}`,
      name: `Node ${index + 3}`,
      type: 'vless',
      server: `node-${index + 3}.example.com`,
      port: 443,
      tls: true,
      selected: false,
    })),
  ]
  const subscription = {
    id: 'sub-1',
    name: 'Main',
    url: 'https://example.com/sub',
    autoUpdate: true,
    updateIntervalMinutes: 360,
    active: true,
    selectedNodeId: 'node-1',
    nodeCount: nodes.length,
    nodes,
  }
  await page.route('**/api/v1/subscriptions', async (route) => {
    await route.fulfill({ json: { items: [{ ...subscription, nodes: undefined }] } })
  })
  await page.route('**/api/v1/subscriptions/sub-1', async (route) => {
    await route.fulfill({ json: subscription })
  })
  await page.route('**/api/v1/session', async (route) => {
    await route.fulfill({ json: { csrfToken: 'test-token' } })
  })
  await page.route('**/api/v1/subscriptions/sub-1/latency', async (route) => {
    const input = route.request().postDataJSON() as { nodeIds?: string[] }
    if (input.nodeIds?.length === 1) {
      manualTestRequests++
      await manualTestGate
    }
    const items = [
      { nodeId: 'node-1', name: 'Tokyo', status: 'ok', latencyMs: 38 },
      { nodeId: 'node-2', name: 'London', status: 'timeout', detail: '连接超时' },
    ].filter((item) => !input.nodeIds?.length || input.nodeIds.includes(item.nodeId))
    await route.fulfill({
      json: { items },
    })
  })
  await page.route('**/api/v1/pools', async (route) => {
    await route.fulfill({ json: { items: [{
      id: 'pool-1', name: 'Daily', members: [], memberCount: 0, availableCount: 0,
      probeIntervalSeconds: 60, toleranceMs: 80,
      probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
      fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
      createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
    }] } })
  })
  await page.route('**/api/v1/pools/pool-1', async (route) => {
    poolUpdate = route.request().postDataJSON()
    await route.fulfill({ json: {
      id: 'pool-1', name: 'Daily', memberCount: 1, availableCount: 1,
      probeIntervalSeconds: 60, toleranceMs: 80,
      probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
      fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
      createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
      members: [{ subscriptionId: 'sub-1', subscriptionName: 'Main', nodeId: 'node-1', nodeName: 'Tokyo', type: 'trojan', server: 'tokyo.example.com', port: 443, available: true }],
    } })
  })

  await page.goto('/#nodes')
  await expect(page.getByText('Tokyo', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '测试 Tokyo 延迟' }).click()
  await page.getByRole('button', { name: '测试 London 延迟' }).click()
  await expect.poll(() => manualTestRequests).toBe(2)
  releaseManualTests()
  await expect(page.getByText('38 ms')).toBeVisible()
  await expect(page.getByText('超时')).toBeVisible()
  await page.getByRole('button', { name: '批量选择 Tokyo' }).click()
  await page.getByRole('button', { name: '批量选择 London' }).click()
  await page.getByRole('button', { name: '加入节点池 (2)' }).click()
  await page.locator('.pool-picker-row').click()
  expect(poolUpdate?.members).toEqual([
    { subscriptionId: 'sub-1', nodeId: 'node-1' },
    { subscriptionId: 'sub-1', nodeId: 'node-2' },
  ])
  await page.getByLabel('每行列数').selectOption('4')
  await expect(page.getByLabel('每行列数')).toHaveValue('4')

  await page.getByRole('button', { name: '批量选择 Tokyo' }).click()
  await page.getByRole('button', { name: '批量选择 London' }).click()
  await page.getByRole('button', { name: /^测试所选/ }).click()
  await expect(page.getByText('38 ms')).toBeVisible()
  await expect(page.getByText('超时')).toBeVisible()

  await page.reload()
  await expect(page.getByLabel('每行列数')).toHaveValue('4')

  await page.setViewportSize({ width: 1366, height: 768 })
  const scrollContainer = page.locator('.content-scroll')
  const fixedBefore = await page.evaluate(() => ({
    topbarTop: document.querySelector('.topbar')?.getBoundingClientRect().top,
    sidebarTop: document.querySelector('.sidebar')?.getBoundingClientRect().top,
  }))
  await scrollContainer.evaluate((element) => {
    element.scrollTop = 600
  })
  const fixedAfter = await page.evaluate(() => ({
    topbarTop: document.querySelector('.topbar')?.getBoundingClientRect().top,
    sidebarTop: document.querySelector('.sidebar')?.getBoundingClientRect().top,
    contentScrollTop: document.querySelector('.content-scroll')?.scrollTop ?? 0,
    windowScrollTop: window.scrollY,
  }))
  expect(fixedAfter.contentScrollTop).toBeGreaterThan(0)
  expect(fixedAfter.windowScrollTop).toBe(0)
  expect(fixedAfter.topbarTop).toBe(fixedBefore.topbarTop)
  expect(fixedAfter.sidebarTop).toBe(fixedBefore.sidebarTop)

  await page.getByRole('button', { name: '核心' }).click()
  await expect(page.getByRole('heading', { name: 'sing-box 核心' })).toBeVisible()
  await expect(page.locator('.content-scroll')).toHaveJSProperty('scrollTop', 0)
  await page.getByRole('button', { name: '节点', exact: true }).click()
  await expect(page.getByText('Tokyo', { exact: true })).toBeVisible()

  const viewports = [
    { width: 2048, height: 1152, columns: 4, minimumContentWidth: 1600 },
    { width: 1366, height: 768, columns: 4, minimumContentWidth: 1000 },
    { width: 1024, height: 768, columns: 2, minimumContentWidth: 700 },
    { width: 390, height: 844, columns: 1, minimumContentWidth: 360 },
    { width: 320, height: 568, columns: 1, minimumContentWidth: 300 },
  ]
  for (const viewport of viewports) {
    await page.setViewportSize(viewport)
    const layout = await page.evaluate(() => {
      const content = document.querySelector('.content')?.getBoundingClientRect()
      const grid = document.querySelector('.node-grid')
      return {
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        contentWidth: content?.width ?? 0,
        columns: grid ? getComputedStyle(grid).gridTemplateColumns.split(' ').length : 0,
      }
    })
    expect(layout.overflow).toBe(0)
    expect(layout.contentWidth).toBeGreaterThanOrEqual(viewport.minimumContentWidth)
    expect(layout.columns).toBe(viewport.columns)
  }
})
