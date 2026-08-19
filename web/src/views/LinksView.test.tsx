import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Link, LinkSnapshot } from '../api/types'
import { LinksView } from './LinksView'

const api = vi.hoisted(() => ({
  clearLinks: vi.fn(),
  listLinks: vi.fn(),
}))

vi.mock('../api/client', () => api)

function link(partial: Partial<Link>): Link {
  return {
    id: 'id',
    host: 'example.com:443',
    network: 'tcp',
    type: 'http',
    upload: 0,
    download: 0,
    uploadRate: 0,
    downloadRate: 0,
    node: 'Tokyo',
    active: true,
    startedAt: '2026-08-05T00:00:00Z',
    firstSeenAt: '2026-08-05T00:00:00Z',
    lastSeenAt: '2026-08-05T00:00:00Z',
    ...partial,
  }
}

function snapshot(links: Link[]): LinkSnapshot {
  return {
    running: true,
    updatedAt: '2026-08-05T00:00:00Z',
    stats: {
      active: links.filter((item) => item.active).length,
      total: links.length,
      uploadTotal: 0,
      downloadTotal: 0,
      uploadRate: 0,
      downloadRate: 0,
      trackedCapacity: 1000,
    },
    links,
  }
}

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <LinksView />
    </QueryClientProvider>,
  )
}

describe('LinksView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    api.listLinks.mockResolvedValue(
      snapshot([
      link({ id: 'a', host: 'a.example.com:443', download: 2048, downloadRate: 1024, node: 'Tokyo' }),
        link({ id: 'b', host: 'b.example.com:443', url: 'https://b.example.com/news', download: 1024, downloadRate: 512, node: 'London' }),
      ]),
    )
    api.clearLinks.mockResolvedValue(undefined)
  })

  it('renders the observed links with host, bytes, rate, and node', async () => {
    renderView()
    expect(await screen.findByText('a.example.com:443')).toBeInTheDocument()
    expect(screen.getByText('b.example.com:443')).toBeInTheDocument()
    expect(screen.getByText('Tokyo')).toBeInTheDocument()
    expect(screen.getByText('London')).toBeInTheDocument()
    expect(screen.getByText('https://b.example.com/news')).toBeInTheDocument()
    // 2 KiB download and 1 KiB/s rate formatted.
    expect(screen.getByText('2.0 KiB')).toBeInTheDocument()
    expect(screen.getByText('1.0 KiB/s')).toBeInTheDocument()
  })

  it('sends the search term to the API', async () => {
    const user = userEvent.setup()
    renderView()
    const input = await screen.findByLabelText('搜索链接')
    await user.type(input, 'tokyo')
    await waitFor(() => {
      const calls = api.listLinks.mock.calls.map((call) => call[0])
      expect(calls.some((params) => params.search === 'tokyo')).toBe(true)
    })
  })

  it('toggles sort direction on repeated header clicks', async () => {
    const user = userEvent.setup()
    renderView()
    const header = await screen.findByRole('button', { name: /下载量/ })
    await user.click(header)
    await waitFor(() => {
      const lastCall = api.listLinks.mock.calls.at(-1)?.[0]
      expect(lastCall.sort).toBe('-download')
    })
    await user.click(header)
    await waitFor(() => {
      const lastCall = api.listLinks.mock.calls.at(-1)?.[0]
      expect(lastCall.sort).toBe('download')
    })
  })

  it('sorts by the reported URL or domain', async () => {
    const user = userEvent.setup()
    renderView()
    const header = await screen.findByRole('button', { name: /网址 \/ 域名/ })
    await user.click(header)
    await waitFor(() => {
      const lastCall = api.listLinks.mock.calls.at(-1)?.[0]
      expect(lastCall.sort).toBe('-url')
    })
  })

  it('appends a secondary sort column when shift-clicking', async () => {
    const user = userEvent.setup()
    renderView()
    const hostHeader = await screen.findByRole('button', { name: /主机/ })
    const downloadHeader = await screen.findByRole('button', { name: /下载量/ })
    await user.click(hostHeader)
    await user.keyboard('{Shift>}')
    await user.click(downloadHeader)
    await user.keyboard('{/Shift}')
    await waitFor(() => {
      const lastCall = api.listLinks.mock.calls.at(-1)?.[0]
      expect(lastCall.sort).toBe('-host,-download')
    })
  })

  it('clears the cache via the clear button', async () => {
    const user = userEvent.setup()
    renderView()
    const button = await screen.findByRole('button', { name: /清空记录/ })
    await user.click(button)
    await waitFor(() => expect(api.clearLinks).toHaveBeenCalled())
  })

  it('shows an empty state when there is nothing to display', async () => {
    api.listLinks.mockResolvedValue(snapshot([]))
    renderView()
    expect(await screen.findByText(/暂无链接/)).toBeInTheDocument()
    const table = screen.queryByRole('table')
    expect(table).not.toBeInTheDocument()
  })

  it('does not report the proxy as stopped when the status is unavailable', async () => {
    api.listLinks.mockRejectedValue(new Error('API unavailable'))
    renderView()
    expect(await screen.findByText('状态未知')).toBeInTheDocument()
    expect(await screen.findByText('无法读取连接状态')).toBeInTheDocument()
    expect(screen.queryByText('代理未运行。请先在「连接」页启动代理。')).not.toBeInTheDocument()
  })

  it('marks closed connections', async () => {
    api.listLinks.mockResolvedValue(snapshot([link({ id: 'a', active: false, node: 'direct' })]))
    renderView()
    const host = await screen.findByText('example.com:443')
    const row = host.closest('tr')
    expect(row).toHaveClass('links-row--closed')
    expect(within(row as HTMLElement).getByText('直连')).toBeInTheDocument()
  })
})
