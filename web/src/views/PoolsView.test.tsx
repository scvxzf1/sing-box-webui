import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodePool } from '../api/types'
import { PoolsView } from './PoolsView'

const api = vi.hoisted(() => ({
  createNodePool: vi.fn(),
  deleteNodePool: vi.fn(),
  listNodePools: vi.fn(),
  reorderNodePools: vi.fn(),
  testNodeLatency: vi.fn(),
  updateNodePool: vi.fn(),
}))

vi.mock('../api/client', () => api)

const pool: NodePool = {
  id: 'pool-1', name: 'Daily', memberCount: 2, availableCount: 2,
  probeIntervalSeconds: 60, toleranceMs: 80,
  probeUrl: 'https://cp.cloudflare.com/generate_204', idleTimeoutSeconds: 1800, interruptExistingConnections: false,
  fallbackProbeUrls: [], highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2, maxBackoffSeconds: 300,
  createdAt: '2026-08-05T00:00:00Z', updatedAt: '2026-08-05T00:00:00Z',
  members: [
    { subscriptionId: 'sub-a', subscriptionName: 'Alpha', nodeId: 'node-a', nodeName: 'Tokyo', type: 'trojan', server: 'tokyo.example.com', port: 443, available: true },
    { subscriptionId: 'sub-b', subscriptionName: 'Beta', nodeId: 'node-b', nodeName: 'London', type: 'vless', server: 'london.example.com', port: 443, available: true },
  ],
}

describe('PoolsView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    api.listNodePools.mockResolvedValue([pool])
    api.testNodeLatency.mockResolvedValue({ items: [{ nodeId: 'node-a', name: 'Tokyo', status: 'ok', latencyMs: 36 }] })
    api.updateNodePool.mockResolvedValue(pool)
    api.reorderNodePools.mockResolvedValue([pool])
  })

  it('restores and persists the pool member grid preference', async () => {
    window.localStorage.setItem('sing-box-webui:pools-grid-columns', '3')
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { container } = render(<QueryClientProvider client={client}><PoolsView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    expect(screen.getByLabelText('池内节点每行列数')).toHaveValue('3')
    expect(container.querySelector('.pool-members')).toHaveClass('pool-members--3')

    await user.selectOptions(screen.getByLabelText('池内节点每行列数'), '2')
    await waitFor(() => expect(window.localStorage.getItem('sing-box-webui:pools-grid-columns')).toBe('2'))
    expect(container.querySelector('.pool-members')).toHaveClass('pool-members--2')
  })

  it('renders only members and tests a selected node', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><PoolsView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    expect(screen.getByText('London')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '测试 Tokyo 延迟' }))
    expect(await screen.findByText('36 ms')).toBeInTheDocument()
    expect(api.testNodeLatency).toHaveBeenCalledWith('sub-a', { nodeIds: ['node-a'] })

    await user.click(screen.getByRole('button', { name: '移除 London' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateNodePool).toHaveBeenCalledWith('pool-1', expect.objectContaining({
      members: [{ subscriptionId: 'sub-a', nodeId: 'node-a' }],
    })))
  })

  it('applies health check and automatic routing settings when the pool is saved', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><PoolsView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '节点池设置' }))

    const probeUrl = screen.getByLabelText('探测地址')
    await user.clear(probeUrl)
    await user.type(probeUrl, 'https://www.gstatic.com/generate_204')
    await user.type(screen.getByLabelText('备用探测地址'), 'https://example.com/generate_204')
    await user.selectOptions(screen.getByLabelText('探测间隔'), '180')
    await user.selectOptions(screen.getByLabelText('高延迟阈值'), '2000')
    await user.selectOptions(screen.getByLabelText('切换容差'), '150')
    await user.selectOptions(screen.getByLabelText('空闲超时'), '3600')
    await user.selectOptions(screen.getByLabelText('连续失败隔离'), '3')
    await user.selectOptions(screen.getByLabelText('恢复成功次数'), '3')
    await user.selectOptions(screen.getByLabelText('最大退避时间'), '600')
    await user.click(screen.getByRole('checkbox'))
    await user.click(screen.getByRole('button', { name: '应用设置' }))
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => expect(api.updateNodePool).toHaveBeenCalledWith('pool-1', expect.objectContaining({
      probeUrl: 'https://www.gstatic.com/generate_204',
      fallbackProbeUrls: ['https://example.com/generate_204'],
      probeIntervalSeconds: 180,
      highLatencyThresholdMs: 2000,
      toleranceMs: 150,
      idleTimeoutSeconds: 3600,
      consecutiveFailures: 3,
      recoverySuccesses: 3,
      maxBackoffSeconds: 600,
      interruptExistingConnections: true,
    })))
  })

  it('filters pool members by the search box', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><PoolsView /></QueryClientProvider>)

    expect(await screen.findByText('Tokyo')).toBeInTheDocument()
    const search = screen.getByLabelText('搜索池内节点')
    await user.type(search, 'london')
    expect(screen.queryByText('Tokyo')).not.toBeInTheDocument()
    expect(screen.getByText('London')).toBeInTheDocument()

    await user.clear(search)
    await user.type(search, 'beta')
    expect(screen.getByText('London')).toBeInTheDocument()

    await user.clear(search)
    await user.type(search, '不存在')
    expect(screen.getByText('没有匹配“不存在”的节点')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '清除搜索' }))
    expect(screen.getByText('Tokyo')).toBeInTheDocument()
    expect(screen.getByText('London')).toBeInTheDocument()
  })

  it('previews and persists a dragged pool position', async () => {
    const secondPool = { ...pool, id: 'pool-2', name: 'Backup' }
    api.listNodePools.mockResolvedValue([pool, secondPool])
    api.reorderNodePools.mockResolvedValue([secondPool, pool])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><PoolsView /></QueryClientProvider>)

    const firstRow = await screen.findByRole('button', { name: /Daily/ })
    const secondRow = screen.getByRole('button', { name: /Backup/ })
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn(), getData: vi.fn(() => 'pool-1') }
    fireEvent.dragStart(firstRow, { dataTransfer })
    fireEvent.dragOver(secondRow, { dataTransfer, clientY: -1 })
    expect(secondRow).toHaveClass('pool-row--drop-after')
    fireEvent.drop(secondRow, { dataTransfer })

    await waitFor(() => expect(api.reorderNodePools.mock.calls[0]?.[0]).toEqual(['pool-2', 'pool-1']))
  })

  it('ignores a drop whose target does not match the current indicator', async () => {
    const secondPool = { ...pool, id: 'pool-2', name: 'Backup' }
    const thirdPool = { ...pool, id: 'pool-3', name: 'Emergency' }
    api.listNodePools.mockResolvedValue([pool, secondPool, thirdPool])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><PoolsView /></QueryClientProvider>)

    const firstRow = await screen.findByRole('button', { name: /Daily/ })
    const secondRow = screen.getByRole('button', { name: /Backup/ })
    const thirdRow = screen.getByRole('button', { name: /Emergency/ })
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn(), getData: vi.fn(() => 'pool-1') }
    fireEvent.dragStart(firstRow, { dataTransfer })
    fireEvent.dragOver(secondRow, { dataTransfer, clientY: -1 })
    fireEvent.drop(thirdRow, { dataTransfer })

    expect(api.reorderNodePools).not.toHaveBeenCalled()
  })
})
