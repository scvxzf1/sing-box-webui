import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodePool, Subscription } from '../api/types'
import { ConnectionView } from './ConnectionView'

const api = vi.hoisted(() => ({
  applyRuntime: vi.fn(),
  getRuntime: vi.fn(),
  getSubscription: vi.fn(),
  listNodePools: vi.fn(),
  listSubscriptions: vi.fn(),
  stopRuntime: vi.fn(),
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

describe('ConnectionView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    api.listSubscriptions.mockResolvedValue(subscriptions)
    api.listNodePools.mockResolvedValue(pools)
    api.getSubscription.mockImplementation((id: string) => Promise.resolve(subscriptionDetails.get(id)))
    api.getRuntime.mockResolvedValue({
      state: 'stopped',
      capabilities: {
        singBox: { available: true, detail: 'ok' },
        systemProxy: { available: true, detail: 'ok' },
        tun: { available: true, detail: 'ok' },
      },
    })
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
})

function renderConnectionView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><ConnectionView /></QueryClientProvider>)
}
