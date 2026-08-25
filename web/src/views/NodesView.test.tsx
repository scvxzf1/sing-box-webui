import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Subscription } from '../api/types'
import { NodesView } from './NodesView'

const api = vi.hoisted(() => ({
  getSubscription: vi.fn(),
  getSubscriptionNodeLink: vi.fn(),
  importSubscriptionNodes: vi.fn(),
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
const reorderedSubscription: Subscription = {
  ...subscription,
  id: 'subscription-2',
  name: 'Backup',
  active: false,
  nodeCount: 0,
  nodes: [],
}

const pagedNodes = Array.from({ length: 120 }, (_, index) => ({
  id: `node-${index + 1}`,
  name: `Node ${String(index + 1).padStart(3, '0')}`,
  type: 'trojan',
  server: `node-${index + 1}.example.com`,
  port: 443,
  tls: true,
  selected: false,
}))
const pagedSubscription: Subscription = {
  ...subscription,
  nodeCount: pagedNodes.length,
  nodes: pagedNodes,
}

const manyResults = (ids: string[]) => ({
  items: ids.map((id) => ({ nodeId: id, name: id, status: 'ok' as const, latencyMs: 40 })),
})

describe('NodesView', () => {
	afterEach(cleanup)
  beforeEach(() => {
	api.testNodeLatency.mockReset()
    api.getSubscriptionNodeLink.mockReset()
    api.importSubscriptionNodes.mockReset()
    api.updateNodePool.mockReset()
    window.localStorage.clear()
    api.getSubscription.mockResolvedValue(subscription)
    api.getSubscriptionNodeLink.mockResolvedValue({
      link: 'trojan://node-secret@tokyo.example.com:443#Tokyo',
      source: 'original',
    })
    api.listSubscriptions.mockResolvedValue([subscription])
    api.listNodePools.mockResolvedValue([{
      id: 'pool-1', name: 'Daily', members: [], memberCount: 0, availableCount: 0,
      probeIntervalSeconds: 60, toleranceMs: 80,
      probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
      fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
      createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
    }])
    api.selectNode.mockResolvedValue(subscription)
    api.importSubscriptionNodes.mockResolvedValue({
      addedCount: 0,
      duplicateCount: 0,
      invalidCount: 0,
      items: [],
      subscription,
    })
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
    expect(screen.getByRole('dialog').parentElement?.parentElement).toBe(document.body)
    await user.click(screen.getByRole('button', { name: /Daily/ }))
    await waitFor(() => expect(api.updateNodePool).toHaveBeenCalledWith('pool-1', {
      members: [{ subscriptionId: 'subscription-1', nodeId: 'node-1' }],
    }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('opens the original node link on right click and keeps it out of the node list request', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    const nodeName = await screen.findByText('Tokyo')
    const card = nodeName.closest('article')
    expect(card).not.toBeNull()
    fireEvent.contextMenu(card!)

    expect(await screen.findByRole('dialog', { name: 'Tokyo' })).toBeInTheDocument()
    await waitFor(() => expect(api.getSubscriptionNodeLink).toHaveBeenCalledWith('subscription-1', 'node-1'))
    expect(screen.getByRole('textbox', { name: '原始节点链接' })).toHaveValue(
      'trojan://node-secret@tokyo.example.com:443#Tokyo',
    )
    expect(api.getSubscription).toHaveBeenCalledWith('subscription-1', expect.any(AbortSignal))

    await user.click(screen.getByRole('button', { name: '复制链接' }))
    expect(await navigator.clipboard.readText()).toBe('trojan://node-secret@tokyo.example.com:443#Tokyo')
    expect(screen.getByText('已复制')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '关闭节点链接' }))
    expect(screen.queryByRole('dialog', { name: 'Tokyo' })).not.toBeInTheDocument()
  })

  it('imports multiple node links, tests recognized nodes, and stays open', async () => {
    const importedNode: Subscription['nodes'][number] = {
      id: 'node-3',
      name: 'Singapore',
      type: 'vless',
      server: 'singapore.example.com',
      port: 443,
      tls: true,
      selected: false,
    }
    api.importSubscriptionNodes.mockResolvedValue({
      addedCount: 1,
      duplicateCount: 1,
      invalidCount: 1,
      items: [
        { line: 1, status: 'added', node: importedNode },
        { line: 2, status: 'duplicate', node: subscription.nodes[0] },
        { line: 3, status: 'invalid', error: '不支持的节点协议' },
      ],
      subscription: {
        ...subscription,
        nodeCount: 3,
        nodes: [...subscription.nodes, importedNode],
      },
    })
    api.testNodeLatency.mockResolvedValue({
      items: [
        { nodeId: 'node-3', name: 'Singapore', status: 'ok', latencyMs: 58 },
        { nodeId: 'node-1', name: 'Tokyo', status: 'timeout' },
      ],
    })
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '导入节点' }))
    const links = [
      'vless://secret-user@singapore.example.com:443#Singapore',
      'trojan://secret-password@tokyo.example.com:443#Tokyo',
      'unknown://do-not-render',
    ].join('\n')
    await user.type(screen.getByRole('textbox', { name: '节点链接' }), links)
    await user.click(screen.getByRole('button', { name: '添加并测试' }))

    await waitFor(() => expect(api.importSubscriptionNodes).toHaveBeenCalledWith('subscription-1', { links }))
    await waitFor(() => expect(api.testNodeLatency).toHaveBeenCalledWith('subscription-1', {
      nodeIds: ['node-3', 'node-1'],
    }))
    expect(await screen.findByText('58 ms')).toBeInTheDocument()
    expect(screen.getByText('超时')).toBeInTheDocument()
    expect(screen.getByText('不支持的节点协议')).toBeInTheDocument()
    expect(screen.getAllByText('已添加')).not.toHaveLength(0)
    expect(screen.getAllByText('已存在')).not.toHaveLength(0)
    expect(screen.getByRole('dialog', { name: '手动导入节点' })).toBeInTheDocument()
    expect(within(screen.getByLabelText('导入结果')).queryByText(/secret-user|secret-password|do-not-render/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '关闭导入节点' }))
    expect(screen.queryByRole('dialog', { name: '手动导入节点' })).not.toBeInTheDocument()
  })

  it('keeps the subscription selector in the persisted subscription order', async () => {
    api.listSubscriptions.mockResolvedValue([reorderedSubscription, subscription])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    await screen.findByText('Tokyo')
    const selector = screen.getByLabelText('订阅') as HTMLSelectElement
    expect(Array.from(selector.options).map((option) => option.textContent)).toEqual(['Backup', 'Main'])
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
    expect(screen.getByRole('button', { name: '测试 Tokyo 延迟' })).toHaveClass('node-card-region--testing')
    expect(screen.getByRole('button', { name: '测试 Tokyo 延迟' })).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByRole('button', { name: '测试 Tokyo 延迟' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '测试 London 延迟' })).toBeDisabled()

    resolvers.get('node-1')?.({ items: [{ nodeId: 'node-1', name: 'Tokyo', status: 'ok', latencyMs: 31 }] })
    resolvers.get('node-2')?.({ items: [{ nodeId: 'node-2', name: 'London', status: 'ok', latencyMs: 47 }] })
    expect(await screen.findByText('31 ms')).toBeInTheDocument()
    expect(await screen.findByText('47 ms')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '测试 Tokyo 延迟' })).not.toHaveClass('node-card-region--testing')
    expect(screen.getByRole('button', { name: '测试 Tokyo 延迟' })).toHaveAttribute('aria-busy', 'false')
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
    const tokyoBatch = screen.getByRole('button', { name: '批量选择 Tokyo' })
    const londonBatch = screen.getByRole('button', { name: '批量选择 London' })
    expect(tokyoBatch).toHaveAttribute('aria-pressed', 'false')
    await user.click(tokyoBatch)
    await user.click(londonBatch)
    expect(tokyoBatch).toHaveAttribute('aria-pressed', 'true')
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

  it('selects the current node from the left card region', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    expect(await screen.findByRole('button', { name: '设 Tokyo 为当前节点' })).toHaveAttribute('aria-pressed', 'true')
    const londonCurrent = screen.getByRole('button', { name: '设 London 为当前节点' })
    expect(londonCurrent).toHaveAttribute('aria-pressed', 'false')
    await user.click(londonCurrent)

    expect(api.selectNode).toHaveBeenCalledWith('subscription-1', 'node-2')
  })

  it('paginates nodes and restores the persisted page size', async () => {
    api.getSubscription.mockResolvedValue(pagedSubscription)
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    expect(await screen.findByText('Node 001')).toBeInTheDocument()
    expect(screen.getByText('Node 048')).toBeInTheDocument()
    expect(screen.queryByText('Node 049')).not.toBeInTheDocument()
    expect(screen.getByText('第 1 / 3 页')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(await screen.findByText('Node 049')).toBeInTheDocument()
    expect(screen.queryByText('Node 048')).not.toBeInTheDocument()
    expect(screen.getByText('第 2 / 3 页')).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('每页节点数'), '96')
    await waitFor(() => expect(window.localStorage.getItem('sing-box-webui:nodes-page-size')).toBe('96'))
    expect(await screen.findByText('Node 096')).toBeInTheDocument()
    expect(screen.getByText('第 1 / 2 页')).toBeInTheDocument()

    cleanup()
    const restored = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={restored}><NodesView /></QueryClientProvider>)
    expect(await screen.findByText('Node 001')).toBeInTheDocument()
    expect(screen.getByLabelText('每页节点数')).toHaveValue('96')
  })

  it('selects the current page and tests only the selected nodes', async () => {
    api.getSubscription.mockResolvedValue(pagedSubscription)
    api.testNodeLatency.mockImplementation((_id: string, input: { nodeIds?: string[] }) =>
      Promise.resolve(manyResults(input.nodeIds ?? [])))
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><NodesView /></QueryClientProvider>)

    expect(await screen.findByText('Node 001')).toBeInTheDocument()
    const selectAll = screen.getByRole('checkbox', { name: '全选当前页' })
    await user.click(selectAll)
    expect(await screen.findByText(/已选 48/)).toBeInTheDocument()
    expect(selectAll).toBeChecked()

    await user.click(screen.getByRole('button', { name: '下一页' }))
    await user.click(screen.getByRole('checkbox', { name: '全选当前页' }))
    expect(await screen.findByText(/已选 96/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '测试所选 (96)' }))
    await waitFor(() => expect(api.testNodeLatency).toHaveBeenCalledTimes(1))
    expect(api.testNodeLatency).toHaveBeenCalledWith('subscription-1', {
      nodeIds: pagedNodes.slice(0, 96).map((node) => node.id),
    })
    expect((await screen.findAllByText('40 ms')).length).toBeGreaterThan(0)
  })
})
