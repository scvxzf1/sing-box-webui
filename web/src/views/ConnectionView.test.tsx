import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodePool, ProxyChain, Subscription } from '../api/types'
import { ConnectionView } from './ConnectionView'

const api = vi.hoisted(() => ({
  applyRuntime: vi.fn(),
  getRuntime: vi.fn(),
  getSubscription: vi.fn(),
  listNodePools: vi.fn(),
  listProxyChains: vi.fn(),
  listSubscriptions: vi.fn(),
  stopRuntime: vi.fn(),
  updateRuntimePreferences: vi.fn(),
}))

vi.mock('../api/client', () => api)
vi.mock('./NodeDiagnostic', () => ({ NodeDiagnostic: () => null }))
vi.mock('./QuickTest', () => ({ QuickTest: () => null }))

const subscriptions: Subscription[] = [
  {
    id: 'subscription-1', name: 'Primary', url: 'https://example.com/primary', autoUpdate: true,
    updateIntervalMinutes: 360, active: true, nodeCount: 1,
  },
  {
    id: 'subscription-2', name: 'Backup', url: 'https://example.com/backup', autoUpdate: true,
    updateIntervalMinutes: 360, active: false, nodeCount: 1,
  },
]

const subscriptionDetails = new Map(subscriptions.map((subscription, index) => [subscription.id, {
  ...subscription,
  selectedNodeId: `node-${index + 1}`,
  nodes: [{
    id: `node-${index + 1}`, name: index === 0 ? 'Tokyo' : 'London', type: 'trojan',
    server: `${subscription.id}.example.com`, port: 443, tls: true, selected: true,
  }],
}]))

const poolDefaults = {
  members: [], memberCount: 2, availableCount: 2, probeIntervalSeconds: 60, toleranceMs: 80,
  probeUrl: 'https://cp.cloudflare.com/generate_204', fallbackProbeUrls: [], idleTimeoutSeconds: 1800,
  highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
  interruptExistingConnections: false, createdAt: '2026-08-12T00:00:00Z', updatedAt: '2026-08-12T00:00:00Z',
}
const pools: NodePool[] = [
  { ...poolDefaults, id: 'pool-1', name: 'Daily' },
  { ...poolDefaults, id: 'pool-2', name: 'Backup pool' },
]
const chains: ProxyChain[] = [{
  id: 'chain-1', name: 'Tokyo to London', entryType: 'node',
  entryNode: { subscriptionId: 'subscription-1', nodeId: 'node-1' },
  exitNode: { subscriptionId: 'subscription-2', nodeId: 'node-2' },
  entryName: 'Tokyo', exitName: 'London', available: true,
  createdAt: '2026-08-12T00:00:00Z', updatedAt: '2026-08-12T00:00:00Z',
}]

describe('ConnectionView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    api.listSubscriptions.mockResolvedValue(subscriptions)
    api.listNodePools.mockResolvedValue(pools)
    api.listProxyChains.mockResolvedValue(chains)
    api.getSubscription.mockImplementation((id: string) => Promise.resolve(subscriptionDetails.get(id)))
    api.getRuntime.mockResolvedValue({
      state: 'stopped',
      capabilities: {
        singBox: { available: true, detail: 'ok' },
        systemProxy: { available: true, detail: 'ok' },
        tun: { available: true, detail: 'ok' },
      },
    })
    api.updateRuntimePreferences.mockImplementation(({ allowLan }: { allowLan: boolean }) => Promise.resolve({
      state: 'stopped', allowLan,
      capabilities: {
        singBox: { available: true, detail: 'ok' },
        systemProxy: { available: true, detail: 'ok' },
        tun: { available: true, detail: 'ok' },
      },
    }))
  })

  it('restores the target type and each dropdown option', async () => {
    window.localStorage.setItem('sing-box-webui:connection-target-type', 'pool')
    window.localStorage.setItem('sing-box-webui:connection-subscription-id', 'subscription-2')
    window.localStorage.setItem('sing-box-webui:connection-pool-id', 'pool-2')
    const user = userEvent.setup()

    renderConnectionView()

    await waitFor(() => expect(screen.getByLabelText('选择节点池')).toHaveValue('pool-2'))
    expect(screen.getByRole('button', { name: '节点池' })).toHaveClass('segmented-control--active')
    await user.click(screen.getByRole('button', { name: '单节点' }))
    expect(await screen.findByLabelText('选择订阅')).toHaveValue('subscription-2')
  })

  it('persists option changes and restores them after remounting', async () => {
    const user = userEvent.setup()
    const firstRender = renderConnectionView()

    await waitFor(() => expect(screen.getByLabelText('选择订阅')).toHaveValue('subscription-1'))
    await user.selectOptions(screen.getByLabelText('选择订阅'), 'subscription-2')
    await user.click(screen.getByRole('button', { name: '节点池' }))
    await user.selectOptions(await screen.findByLabelText('选择节点池'), 'pool-2')
    await waitFor(() => {
      expect(window.localStorage.getItem('sing-box-webui:connection-target-type')).toBe('pool')
      expect(window.localStorage.getItem('sing-box-webui:connection-subscription-id')).toBe('subscription-2')
      expect(window.localStorage.getItem('sing-box-webui:connection-pool-id')).toBe('pool-2')
    })

    firstRender.unmount()
    renderConnectionView()
    await waitFor(() => expect(screen.getByLabelText('选择节点池')).toHaveValue('pool-2'))
    await user.click(screen.getByRole('button', { name: '单节点' }))
    expect(await screen.findByLabelText('选择订阅')).toHaveValue('subscription-2')
  })

  it('restores the LAN preference while the proxy is stopped', async () => {
    api.getRuntime.mockResolvedValue({
      state: 'stopped', allowLan: true,
      capabilities: {
        singBox: { available: true, detail: 'ok' },
        systemProxy: { available: true, detail: 'ok' },
        tun: { available: true, detail: 'ok' },
      },
    })
    renderConnectionView()

    await waitFor(() => expect(screen.getByRole('checkbox')).toBeChecked())
    expect(screen.getByText('已开放')).toBeInTheDocument()
  })

  it('persists the LAN preference as soon as the switch changes', async () => {
    const user = userEvent.setup()
    renderConnectionView()

    const toggle = await screen.findByRole('checkbox')
    await user.click(toggle)
    expect(api.updateRuntimePreferences.mock.calls[0]?.[0]).toEqual({ allowLan: true })
    await waitFor(() => expect(toggle).toBeChecked())
  })

  it('falls back when remembered dropdown options no longer exist', async () => {
    window.localStorage.setItem('sing-box-webui:connection-subscription-id', 'deleted-subscription')
    window.localStorage.setItem('sing-box-webui:connection-pool-id', 'deleted-pool')
    const user = userEvent.setup()

    renderConnectionView()

    await waitFor(() => expect(screen.getByLabelText('选择订阅')).toHaveValue('subscription-1'))
    await user.click(screen.getByRole('button', { name: '节点池' }))
    await waitFor(() => expect(screen.getByLabelText('选择节点池')).toHaveValue('pool-1'))
    await waitFor(() => {
      expect(window.localStorage.getItem('sing-box-webui:connection-subscription-id')).toBe('subscription-1')
      expect(window.localStorage.getItem('sing-box-webui:connection-pool-id')).toBe('pool-1')
    })
  })

  it('offers a hot switch while running and keeps stop available', async () => {
    api.getRuntime.mockResolvedValue({
      state: 'running', mode: 'tun', targetType: 'pool', poolId: 'pool-1', poolName: 'Daily', allowLan: false,
      capabilities: {
        singBox: { available: true, detail: 'ok' },
        systemProxy: { available: true, detail: 'ok' },
        tun: { available: true, detail: 'ok' },
      },
    })
    api.applyRuntime.mockResolvedValue({})
    const user = userEvent.setup()
    renderConnectionView()

    expect((await screen.findAllByText('Tokyo')).length).toBeGreaterThan(0)
    expect(screen.getByText('即将热切换')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '停止' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '热切换' }))

    expect(api.applyRuntime.mock.calls[0]?.[0]).toEqual({
      subscriptionId: 'subscription-1', nodeId: 'node-1', mode: 'tun', allowLan: false,
    })
  })

  it('allows a pending pool switch to be canceled and stopped', async () => {
	window.localStorage.setItem('sing-box-webui:connection-target-type', 'pool')
    api.getRuntime.mockResolvedValue({
      state: 'running', mode: 'tun', targetType: 'node', subscriptionId: 'subscription-1', nodeId: 'node-1', allowLan: false,
      capabilities: {
        singBox: { available: true, detail: 'ok' },
        systemProxy: { available: true, detail: 'ok' },
        tun: { available: true, detail: 'ok' },
      },
    })
    api.applyRuntime.mockReturnValue(new Promise(() => {}))
    api.stopRuntime.mockResolvedValue({ state: 'stopped' })
    const user = userEvent.setup()
    renderConnectionView()

    await user.click(await screen.findByRole('button', { name: '热切换' }))
    expect(await screen.findByRole('button', { name: '正在检测 2 个节点…' })).toBeDisabled()
    const stopButton = screen.getByRole('button', { name: '取消并停止' })
    expect(stopButton).toBeEnabled()
    await user.click(stopButton)
    expect(api.stopRuntime).toHaveBeenCalledOnce()
  })

  it('applies a proxy chain as a runtime target', async () => {
    api.applyRuntime.mockResolvedValue({})
    const user = userEvent.setup()
    renderConnectionView()

    await user.click(await screen.findByRole('button', { name: '链式代理' }))
    expect(screen.getByLabelText('选择链式代理')).toHaveValue('chain-1')
    expect(screen.getAllByText('Tokyo to London').length).toBeGreaterThan(0)
    await user.click(screen.getByRole('button', { name: '开启' }))

    expect(api.applyRuntime.mock.calls[0]?.[0]).toEqual({ chainId: 'chain-1', mode: 'tun', allowLan: false })
  })

  it('reorders layout items by dragging and persists the repaired order', async () => {
    window.localStorage.setItem('sing-box-webui:connection-layout-order-v1', JSON.stringify(['target', 'unknown', 'target']))
    const firstRender = renderConnectionView()
    const source = screen.getByRole('button', { name: '调整 节点质量检测 位置' })
    const target = screen.getByRole('group', { name: '连接目标，可拖动排序' })
    const transferred = new Map<string, string>()
    const dataTransfer = {
      effectAllowed: 'none',
      dropEffect: 'none',
      setData: (type: string, value: string) => transferred.set(type, value),
      getData: (type: string) => transferred.get(type) ?? '',
    }

    fireEvent.dragStart(source, { dataTransfer })
    fireEvent.dragOver(target, { dataTransfer, clientX: 100, clientY: 100 })
    fireEvent.drop(target, { dataTransfer })

    await waitFor(() => {
      const order = JSON.parse(window.localStorage.getItem('sing-box-webui:connection-layout-order-v1') ?? '[]') as string[]
      expect(order.slice(0, 3)).toEqual(['target', 'quality', 'selection'])
      expect(new Set(order).size).toBe(7)
    })

    firstRender.unmount()
    renderConnectionView()
    expect(screen.getByRole('group', { name: '节点质量检测，可拖动排序' })).toHaveStyle({ order: 1 })
    expect(screen.getByRole('group', { name: '使用节点，可拖动排序' })).toHaveStyle({ order: 2 })
    await userEvent.click(screen.getByRole('button', { name: '恢复连接配置默认排列' }))
    await waitFor(() => {
      const order = JSON.parse(window.localStorage.getItem('sing-box-webui:connection-layout-order-v1') ?? '[]') as string[]
      expect(order).toEqual(['target', 'selection', 'mode', 'lan', 'quality', 'exit', 'quick'])
    })
  })

  it('supports keyboard layout reordering from the drag handle', async () => {
    const user = userEvent.setup()
    renderConnectionView()

    const handle = screen.getByRole('button', { name: '调整 使用节点 位置' })
    await user.click(handle)
    await user.keyboard('{ArrowUp}')

    await waitFor(() => {
      const order = JSON.parse(window.localStorage.getItem('sing-box-webui:connection-layout-order-v1') ?? '[]') as string[]
      expect(order.slice(0, 2)).toEqual(['selection', 'target'])
    })
  })
})

function renderConnectionView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><ConnectionView /></QueryClientProvider>)
}
