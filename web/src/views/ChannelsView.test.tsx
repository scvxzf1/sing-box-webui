import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ChannelsView } from './ChannelsView'

const api = vi.hoisted(() => ({
  createProxyChannel: vi.fn(),
  deleteProxyChannel: vi.fn(),
  getRuntime: vi.fn(),
  getSubscription: vi.fn(),
  listProxyChannels: vi.fn(),
  listSubscriptions: vi.fn(),
  updateProxyChannel: vi.fn(),
}))

vi.mock('../api/client', () => api)

describe('ChannelsView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    api.listProxyChannels.mockResolvedValue([])
    api.listSubscriptions.mockResolvedValue([
      { id: 'sub-1', name: 'Primary', url: 'https://example.com', autoUpdate: true, updateIntervalMinutes: 360, active: true, nodeCount: 1 },
    ])
    api.getSubscription.mockResolvedValue({
      id: 'sub-1', name: 'Primary', url: 'https://example.com', autoUpdate: true, updateIntervalMinutes: 360, active: true, nodeCount: 1,
      nodes: [{ id: 'node-1', name: 'Tokyo', type: 'shadowsocks', server: 'one.example.com', port: 443, tls: false, selected: true }],
    })
    api.getRuntime.mockResolvedValue({ state: 'stopped' })
  })

  it('requires authentication before creating a shared HTTP channel', async () => {
    api.createProxyChannel.mockResolvedValue({ id: 'channel-1', name: 'LAN HTTP' })
    const user = userEvent.setup()
    renderChannelsView()

    await user.click(await screen.findByRole('button', { name: '新建通道' }))
    await user.type(screen.getByPlaceholderText('例如：浏览器 SOCKS'), 'LAN HTTP')
    await user.click(screen.getByRole('button', { name: 'HTTP' }))
    await user.click(screen.getByRole('button', { name: '反向/共享' }))
    await waitFor(() => expect(screen.getByLabelText('选择通道节点')).toHaveValue('sub-1::node-1'))

    expect(screen.getByText('反向/共享入口必须启用认证')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()

    await user.type(screen.getByLabelText('用户名'), 'browser')
    await user.type(screen.getByLabelText('密码'), 'secret')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(api.createProxyChannel).toHaveBeenCalledWith({
      name: 'LAN HTTP', protocol: 'http', direction: 'reverse', port: 8080,
      username: 'browser', password: 'secret',
      node: { subscriptionId: 'sub-1', nodeId: 'node-1' }, enabled: true,
    })
  })

  it('renders a reachable LAN URI for a shared channel', async () => {
    api.listProxyChannels.mockResolvedValue([{
      id: 'channel-1', name: 'LAN HTTP', protocol: 'http', direction: 'reverse', port: 18080,
      username: 'browser', password: 'secret', node: { subscriptionId: 'sub-1', nodeId: 'node-1' }, enabled: true,
      nodeName: 'Tokyo', listenAddress: '0.0.0.0:18080', accessAddresses: ['192.168.5.173:18080'], available: true,
      createdAt: '2026-08-22T00:00:00Z', updatedAt: '2026-08-22T00:00:00Z',
    }])
    api.getRuntime.mockResolvedValue({ state: 'running' })
    renderChannelsView()

    expect(await screen.findByText('http://browser:secret@192.168.5.173:18080')).toBeInTheDocument()
    expect(screen.queryByText(/http:\/\/browser:secret@0\.0\.0\.0:18080/)).not.toBeInTheDocument()
  })
})

function renderChannelsView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><ChannelsView /></QueryClientProvider>)
}
