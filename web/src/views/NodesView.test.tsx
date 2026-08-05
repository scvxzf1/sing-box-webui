import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Subscription } from '../api/types'
import { NodesView } from './NodesView'

const api = vi.hoisted(() => ({
  getSubscription: vi.fn(),
  listNodePools: vi.fn(),
  listSubscriptions: vi.fn(),
  selectNode: vi.fn(),
  testNodeLatency: vi.fn(),
  updateNodePool: vi.fn(),
}))

vi.mock('../api/client', () => api)

const subscription: Subscription = {
  id: 'subscription-1',
  name: 'Main',
  url: 'https://example.com/sub',
  autoUpdate: true,
  updateIntervalMinutes: 360,
  active: true,
  nodeCount: 2,
  selectedNodeId: 'node-1',
  nodes: [
    { id: 'node-1', name: 'Tokyo', type: 'trojan', server: 'tokyo.example.com', port: 443, tls: true, selected: true },
    { id: 'node-2', name: 'London', type: 'shadowsocks', server: 'london.example.com', port: 8388, tls: false, selected: false },
  ],
}

describe('NodesView', () => {
	afterEach(cleanup)
  beforeEach(() => {
	api.testNodeLatency.mockReset()
    api.updateNodePool.mockReset()
    window.localStorage.clear()
    api.getSubscription.mockResolvedValue(subscription)
    api.listSubscriptions.mockResolvedValue([subscription])
    api.listNodePools.mockResolvedValue([{
      id: 'pool-1', name: 'Daily', members: [], memberCount: 0, availableCount: 0,
      probeIntervalSeconds: 60, toleranceMs: 80,
      probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
      fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
      createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
    }])
    api.selectNode.mockResolvedValue(subscription)
    api.testNodeLatency.mockResolvedValue({
      items: [{ nodeId: 'node-1', name: 'Tokyo', status: 'ok', latencyMs: 42 }],
    })
    api.updateNodePool.mockResolvedValue({})
  })

  it('restores the grid preference and tests one node', async () => {
    window.localStorage.setItem('sing-box-webui:nodes-grid-columns', '4')
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <NodesView />
      </QueryClientProvider>,
    )

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    expect(screen.getByLabelText('每行列数')).toHaveValue('4')

    await user.selectOptions(screen.getByLabelText('每行列数'), '3')
    await waitFor(() => expect(window.localStorage.getItem('sing-box-webui:nodes-grid-columns')).toBe('3'))

    await user.click(screen.getByRole('button', { name: '测试 Tokyo 延迟' }))
    expect(await screen.findByText('42 ms')).toBeInTheDocument()
    expect(api.testNodeLatency).toHaveBeenCalledWith('subscription-1', { nodeIds: ['node-1'] })

    await user.click(screen.getByRole('button', { name: '将 Tokyo 加入节点池' }))
    await user.click(screen.getByRole('button', { name: /Daily/ }))
    await waitFor(() => expect(api.updateNodePool).toHaveBeenCalledWith('pool-1', {
      members: [{ subscriptionId: 'subscription-1', nodeId: 'node-1' }],
    }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('starts manual tests for different nodes concurrently', async () => {
    const resolvers = new Map<string, (value: unknown) => void>()
    api.testNodeLatency.mockImplementation((_subscriptionId: string, input: { nodeIds?: string[] }) => new Promise((resolve) => {
      resolvers.set(input.nodeIds?.[0] ?? '', resolve)
    }))
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '测试 Tokyo 延迟' }))
    await user.click(screen.getByRole('button', { name: '测试 London 延迟' }))

    expect(api.testNodeLatency).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: '测试 Tokyo 延迟' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '测试 London 延迟' })).toBeDisabled()

    resolvers.get('node-1')?.({ items: [{ nodeId: 'node-1', name: 'Tokyo', status: 'ok', latencyMs: 31 }] })
    resolvers.get('node-2')?.({ items: [{ nodeId: 'node-2', name: 'London', status: 'ok', latencyMs: 47 }] })
    expect(await screen.findByText('31 ms')).toBeInTheDocument()
    expect(await screen.findByText('47 ms')).toBeInTheDocument()
  })

  it('adds multiple selected nodes to a pool in one deduplicated update', async () => {
    api.listNodePools.mockResolvedValue([{
      id: 'pool-1', name: 'Daily', memberCount: 1, availableCount: 1,
      probeIntervalSeconds: 60, toleranceMs: 80,
      probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
      fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
      createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
      members: [{ subscriptionId: 'subscription-1', subscriptionName: 'Main', nodeId: 'node-1', nodeName: 'Tokyo', type: 'trojan', server: 'tokyo.example.com', port: 443, available: true }],
    }])
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: '批量选择 Tokyo' }))
    await user.click(screen.getByRole('checkbox', { name: '批量选择 London' }))
    await user.click(screen.getByRole('button', { name: '加入节点池 (2)' }))
    await user.click(screen.getByRole('button', { name: /Daily/ }))

    await waitFor(() => expect(api.updateNodePool).toHaveBeenCalledTimes(1))
    expect(api.updateNodePool).toHaveBeenLastCalledWith('pool-1', {
      members: [
        { subscriptionId: 'subscription-1', nodeId: 'node-1' },
        { subscriptionId: 'subscription-1', nodeId: 'node-2' },
      ],
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})
