import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Subscription } from '../api/types'
import { SubscriptionsView } from './SubscriptionsView'

const api = vi.hoisted(() => ({
  activateSubscription: vi.fn(),
  createSubscription: vi.fn(),
  deleteSubscription: vi.fn(),
  getSubscription: vi.fn(),
  listSubscriptions: vi.fn(),
  refreshSubscription: vi.fn(),
  reorderSubscriptions: vi.fn(),
  updateSubscription: vi.fn(),
}))

vi.mock('../api/client', () => api)

const alpha: Subscription = {
  id: 'sub-a', name: 'Alpha', url: 'https://alpha.example.com', autoUpdate: true,
  updateIntervalMinutes: 360, active: true, nodeCount: 1, nodes: [],
}
const beta: Subscription = {
  id: 'sub-b', name: 'Beta', url: 'https://beta.example.com', autoUpdate: true,
  updateIntervalMinutes: 360, active: false, nodeCount: 2, nodes: [],
}
const gamma: Subscription = {
  id: 'sub-c', name: 'Gamma', url: 'https://gamma.example.com', autoUpdate: true,
  updateIntervalMinutes: 360, active: false, nodeCount: 3, nodes: [],
}

describe('SubscriptionsView', () => {
  afterEach(cleanup)

  beforeEach(() => {
    vi.clearAllMocks()
    api.listSubscriptions.mockResolvedValue([alpha, beta])
    api.getSubscription.mockResolvedValue(alpha)
    api.reorderSubscriptions.mockResolvedValue([beta, alpha])
  })

  it('previews and persists a dragged subscription position', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><SubscriptionsView /></QueryClientProvider>)

    const firstRow = await screen.findByRole('button', { name: /Alpha/ })
    const secondRow = screen.getByRole('button', { name: /Beta/ })
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn(), getData: vi.fn(() => 'sub-a') }
    fireEvent.dragStart(firstRow, { dataTransfer })
    fireEvent.dragOver(secondRow, { dataTransfer, clientY: -1 })
    expect(secondRow).toHaveClass('subscription-row--drop-after')
    fireEvent.drop(secondRow, { dataTransfer })

    await waitFor(() => expect(api.reorderSubscriptions.mock.calls[0]?.[0]).toEqual(['sub-b', 'sub-a']))
  })

  it('ignores a drop whose target does not match the current indicator', async () => {
    api.listSubscriptions.mockResolvedValue([alpha, beta, gamma])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><SubscriptionsView /></QueryClientProvider>)

    const firstRow = await screen.findByRole('button', { name: /Alpha/ })
    const secondRow = screen.getByRole('button', { name: /Beta/ })
    const thirdRow = screen.getByRole('button', { name: /Gamma/ })
    const dataTransfer = { effectAllowed: '', dropEffect: '', setData: vi.fn(), getData: vi.fn(() => 'sub-a') }
    fireEvent.dragStart(firstRow, { dataTransfer })
    fireEvent.dragOver(secondRow, { dataTransfer, clientY: -1 })
    fireEvent.drop(thirdRow, { dataTransfer })

    expect(api.reorderSubscriptions).not.toHaveBeenCalled()
  })

  it('shows a retryable error when subscription details fail', async () => {
    api.getSubscription.mockRejectedValueOnce(new Error('详情读取失败'))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><SubscriptionsView /></QueryClientProvider>)

    expect((await screen.findAllByText('详情读取失败')).length).toBeGreaterThanOrEqual(1)
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })
})
