import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { NodePool, TrafficPolicy } from '../api/types'
import { TrafficPolicyView } from './TrafficPolicyView'

const api = vi.hoisted(() => ({
  getTrafficPolicy: vi.fn(), listNodePools: vi.fn(), updateTrafficPolicy: vi.fn(),
}))

vi.mock('../api/client', () => api)

const policy: TrafficPolicy = {
  enabled: false, downloadPoolId: '', triggerRateBytesPerSecond: 5 << 20, triggerDurationSeconds: 5,
  releaseRateBytesPerSecond: 1 << 20, releaseDurationSeconds: 60, cooldownSeconds: 600,
  state: 'disabled', currentDownloadBps: 0, activeConnections: 0, triggerProgressSeconds: 0,
  releaseProgressSeconds: 0, events: [],
}

const pool = {
  id: 'download-pool', name: '高速下载池', members: [], memberCount: 2, availableCount: 2,
  probeIntervalSeconds: 60, toleranceMs: 80, probeUrl: 'https://example.com', fallbackProbeUrls: [],
  idleTimeoutSeconds: 1800, highLatencyThresholdMs: 3000, consecutiveFailures: 2, recoverySuccesses: 2,
  maxBackoffSeconds: 300, interruptExistingConnections: true,
  createdAt: '2026-08-05T12:00:00Z', updatedAt: '2026-08-05T12:00:00Z',
} as NodePool

describe('TrafficPolicyView', () => {
  afterEach(cleanup)
  beforeEach(() => {
    for (const mock of Object.values(api)) mock.mockReset()
    api.getTrafficPolicy.mockResolvedValue(policy)
    api.listNodePools.mockResolvedValue([pool])
    api.updateTrafficPolicy.mockImplementation(async (input) => ({ ...policy, ...input }))
  })

  it('persists an enabled download policy through the backend API', async () => {
    const user = userEvent.setup()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><TrafficPolicyView /></QueryClientProvider>)

    expect(await screen.findByRole('heading', { name: '流量策略' })).toBeInTheDocument()
    await user.selectOptions(await screen.findByLabelText('下载代理池'), 'download-pool')
    await user.click(screen.getByRole('checkbox'))
    await user.click(screen.getByRole('button', { name: '保存策略' }))

    await waitFor(() => expect(api.updateTrafficPolicy).toHaveBeenCalledWith({
      enabled: true, downloadPoolId: 'download-pool', triggerRateBytesPerSecond: 5 << 20,
      triggerDurationSeconds: 5, releaseRateBytesPerSecond: 1 << 20,
      releaseDurationSeconds: 60, cooldownSeconds: 600,
    }))
  })
})
