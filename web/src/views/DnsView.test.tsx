import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DnsProfile, Runtime } from '../api/types'
import { DnsView } from './DnsView'

const api = vi.hoisted(() => ({
  getDnsProfile: vi.fn(), getRuntime: vi.fn(), updateDnsProfile: vi.fn(),
}))

vi.mock('../api/client', () => api)

const profile: DnsProfile = {
  servers: [{ tag: 'dns-google', type: 'udp', server: '8.8.8.8' }],
  final: 'dns-google',
  strategy: 'prefer_ipv4',
  fakeIP: { enabled: false },
}

const runtime = {
  state: 'running',
  mode: 'tun',
  capabilities: {
    singBox: { available: true, detail: '' },
    systemProxy: { available: true, detail: '' },
    tun: { available: true, detail: '' },
  },
} as Runtime

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><DnsView /></QueryClientProvider>)
}

describe('DnsView', () => {
  afterEach(cleanup)
  beforeEach(() => {
    for (const mock of Object.values(api)) mock.mockReset()
    api.getDnsProfile.mockResolvedValue(profile)
    api.getRuntime.mockResolvedValue(runtime)
    api.updateDnsProfile.mockImplementation(async (input) => input as DnsProfile)
  })

  it('edits the server address and persists the profile', async () => {
    const user = userEvent.setup()
    renderView()

    expect(await screen.findByRole('heading', { name: 'DNS' })).toBeInTheDocument()
    const address = await screen.findByLabelText('服务器 1 地址')
    await user.clear(address)
    await user.type(address, '223.5.5.5')
    await user.click(screen.getByRole('button', { name: '保存配置' }))

    await waitFor(() => expect(api.updateDnsProfile).toHaveBeenCalledWith({
      servers: [{ tag: 'dns-google', type: 'udp', server: '223.5.5.5' }],
      final: 'dns-google',
      strategy: 'prefer_ipv4',
      fakeIP: { enabled: false },
    }))
  })

  it('blocks saving when a server loses its address', async () => {
    const user = userEvent.setup()
    renderView()

    const address = await screen.findByLabelText('服务器 1 地址')
    await user.clear(address)

    expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()
    expect(screen.getByText(/缺少地址/)).toBeInTheDocument()
  })

  it('requires a new default server when the current default is removed', async () => {
    const user = userEvent.setup()
    renderView()

    await screen.findByRole('heading', { name: 'DNS' })
    await user.click(await screen.findByRole('button', { name: '添加服务器' }))

    let tags = screen.getAllByLabelText(/服务器 \d+ 标签/)
    expect(tags).toHaveLength(2)
    expect(screen.getByText(/缺少地址/)).toBeInTheDocument()

    await user.clear(tags[1])
    await user.type(tags[1], 'dns-remote')
    await user.type(screen.getByLabelText('服务器 2 地址'), '1.1.1.1')

    await user.click(screen.getByRole('button', { name: '添加服务器' }))
    tags = screen.getAllByLabelText(/服务器 \d+ 标签/)
    expect(tags).toHaveLength(3)
    await user.clear(tags[2])
    await user.type(tags[2], 'dns-backup')
    await user.type(screen.getByLabelText('服务器 3 地址'), '9.9.9.9')
    expect(screen.getByRole('button', { name: '保存配置' })).toBeEnabled()

    await user.click(screen.getByRole('button', { name: '删除服务器 1' }))
    expect(await screen.findByText(/必须选择默认服务器/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()

    await user.selectOptions(screen.getByLabelText('默认服务器'), 'dns-remote')
    expect(screen.getByRole('button', { name: '保存配置' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '保存配置' }))

    await waitFor(() => expect(api.updateDnsProfile).toHaveBeenCalledWith({
      servers: [
        { tag: 'dns-remote', type: 'udp', server: '1.1.1.1' },
        { tag: 'dns-backup', type: 'udp', server: '9.9.9.9' },
      ],
      final: 'dns-remote',
      strategy: 'prefer_ipv4',
      fakeIP: { enabled: false },
    }))
  })
})
