import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { QuickTest } from './QuickTest'

const api = vi.hoisted(() => ({
  createConnectivityTarget: vi.fn(),
  deleteConnectivityTarget: vi.fn(),
  listConnectivityTargets: vi.fn(),
  testAllConnectivity: vi.fn(),
  testConnectivity: vi.fn(),
}))

vi.mock('../api/client', () => api)

describe('QuickTest', () => {
  afterEach(cleanup)

  beforeEach(() => {
    window.localStorage.clear()
    api.listConnectivityTargets.mockResolvedValue([{ id: 'target-1', name: 'Example', url: 'https://example.com' }])
    api.testConnectivity.mockReset()
  })

  it('prevents duplicate tests and aborts the active request on unmount', async () => {
    let requestSignal: AbortSignal | undefined
    api.testConnectivity.mockImplementation((_id: string, signal?: AbortSignal) => {
      requestSignal = signal
      return new Promise(() => undefined)
    })
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const rendered = render(<QueryClientProvider client={client}><QuickTest step={1} /></QueryClientProvider>)

    const button = await screen.findByRole('button', { name: '测试 Example' })
    await user.click(button)
    expect(button).toBeDisabled()
    await user.click(button)
    expect(api.testConnectivity).toHaveBeenCalledTimes(1)
    expect(requestSignal?.aborted).toBe(false)

    rendered.unmount()
    await waitFor(() => expect(requestSignal?.aborted).toBe(true))
  })
})
