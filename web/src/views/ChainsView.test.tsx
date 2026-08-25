import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ChainsView } from './ChainsView'

const api = vi.hoisted(() => ({
  createProxyChain: vi.fn(), deleteProxyChain: vi.fn(), getSubscription: vi.fn(),
  listNodePools: vi.fn(), listProxyChains: vi.fn(), listSubscriptions: vi.fn(), updateProxyChain: vi.fn(),
  testProxyChainLatency: vi.fn(),
}))

vi.mock('../api/client', () => api)

describe('ChainsView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    api.listProxyChains.mockResolvedValue([])
    api.listSubscriptions.mockResolvedValue([
      { id: 'sub-1', name: 'Primary', url: 'https://example.com', autoUpdate: true, updateIntervalMinutes: 360, active: true, nodeCount: 2 },
    ])
    api.getSubscription.mockResolvedValue({
      id: 'sub-1', name: 'Primary', url: 'https://example.com', autoUpdate: true, updateIntervalMinutes: 360, active: true, nodeCount: 2,
      nodes: [
        { id: 'node-1', name: 'Tokyo', type: 'trojan', server: 'one.example.com', port: 443, tls: true, selected: true },
        { id: 'node-2', name: 'London', type: 'shadowsocks', server: 'two.example.com', port: 443, tls: false, selected: false },
      ],
    })
    api.listNodePools.mockResolvedValue([{ id: 'pool-1', name: 'Daily', members: [], memberCount: 2, availableCount: 2 }])
  })

  it('creates a node-to-node chain', async () => {
    api.createProxyChain.mockResolvedValue({ id: 'chain-1', name: 'Work route' })
    const user = userEvent.setup()
    renderChainsView()

    await user.click(await screen.findByRole('button', { name: '新建链路' }))
    await user.type(screen.getByPlaceholderText('例如：香港中转到日本'), 'Work route')
    await waitFor(() => expect(screen.getByLabelText('选择入口节点')).toHaveValue('sub-1\u0000node-1'))
    expect(screen.getByLabelText('选择出口节点')).toHaveValue('sub-1\u0000node-2')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(api.createProxyChain.mock.calls[0]?.[0]).toEqual({
      name: 'Work route', entryType: 'node',
      entryNode: { subscriptionId: 'sub-1', nodeId: 'node-1' },
      exitNode: { subscriptionId: 'sub-1', nodeId: 'node-2' },
    })
  })

  it('filters entry and exit node selectors independently', async () => {
    const user = userEvent.setup()
    renderChainsView()

    await user.click(await screen.findByRole('button', { name: '新建链路' }))
    const entrySelect = await screen.findByLabelText('选择入口节点')
    const exitSelect = await screen.findByLabelText('选择出口节点')
    await waitFor(() => expect(entrySelect).toHaveValue('sub-1\u0000node-1'))

    await user.type(screen.getByLabelText('选择入口节点搜索'), 'London')
    expect(Array.from((entrySelect as HTMLSelectElement).options).filter((option) => !option.hidden).map((option) => option.textContent)).toEqual(['London · shadowsocks'])
    expect(entrySelect).toHaveValue('sub-1\u0000node-1')
    expect(Array.from((exitSelect as HTMLSelectElement).options).map((option) => option.textContent)).toEqual(['Tokyo · trojan', 'London · shadowsocks'])

    await user.selectOptions(entrySelect, 'sub-1\u0000node-2')
    expect(entrySelect).toHaveValue('sub-1\u0000node-2')
    expect(screen.getByLabelText('选择入口节点搜索')).toHaveValue('')

    await user.type(screen.getByLabelText('选择出口节点搜索'), 'Tokyo')
    expect(Array.from((exitSelect as HTMLSelectElement).options).map((option) => option.textContent)).toEqual(['Tokyo · trojan'])
  })

  it('renders the complete entry-to-exit path after testing', async () => {
    api.listProxyChains.mockResolvedValue([{
      id: 'chain-1', name: 'Tokyo to London', entryType: 'node',
      entryNode: { subscriptionId: 'sub-1', nodeId: 'node-1' },
      exitNode: { subscriptionId: 'sub-1', nodeId: 'node-2' },
      entryName: 'Tokyo', exitName: 'London', available: true,
      createdAt: '2026-08-22T00:00:00Z', updatedAt: '2026-08-22T00:00:00Z',
    }])
    api.testProxyChainLatency.mockResolvedValue({ items: [{ nodeId: 'node-1', name: 'Tokyo', path: ['Tokyo', 'London'], status: 'ok', latencyMs: 123 }] })
    const user = userEvent.setup()
    renderChainsView()

    await user.click(await screen.findByRole('button', { name: '测试链路' }))
    expect(await screen.findByText('Tokyo → London · 123 ms')).toBeInTheDocument()
  })
})

function renderChainsView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><ChainsView /></QueryClientProvider>)
}
