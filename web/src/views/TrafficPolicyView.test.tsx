import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
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

function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><TrafficPolicyView /></QueryClientProvider>)
}

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
    renderView()

    expect(await screen.findByRole('heading', { name: '流量策略' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存策略' })).toBeDisabled()
    await user.selectOptions(await screen.findByLabelText('下载代理池'), 'download-pool')
    await user.click(screen.getByRole('checkbox'))
    await user.click(screen.getByRole('button', { name: '保存策略' }))

    await waitFor(() => expect(api.updateTrafficPolicy).toHaveBeenCalledWith({
      enabled: true, downloadPoolId: 'download-pool', triggerRateBytesPerSecond: 5 << 20,
      triggerDurationSeconds: 5, releaseRateBytesPerSecond: 1 << 20,
      releaseDurationSeconds: 60, cooldownSeconds: 600,
    }))
  })

  it('keeps unsaved configuration out of live status and can discard it', async () => {
    const user = userEvent.setup()
    api.getTrafficPolicy.mockResolvedValue({
      ...policy,
      enabled: true,
      downloadPoolId: 'download-pool',
      state: 'monitoring',
      triggerProgressSeconds: 2,
    })
    renderView()

    const status = await screen.findByRole('region', { name: '流量策略状态' })
    expect(within(status).getByText('2 / 5 秒')).toBeInTheDocument()
    expect(screen.getByText('高速下载池', { selector: '.traffic-handover strong' })).toBeInTheDocument()

    await user.clear(screen.getByLabelText('持续时间'))
    await user.type(screen.getByLabelText('持续时间'), '30')
    expect(within(status).getByText('2 / 5 秒')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '放弃更改' })).toBeEnabled()

    await user.click(screen.getByRole('button', { name: '放弃更改' }))
    expect(screen.getByLabelText('持续时间')).toHaveValue(5)
    expect(screen.getByRole('button', { name: '保存策略' })).toBeDisabled()
  })

  it('explains invalid configuration instead of only disabling save', async () => {
    const user = userEvent.setup()
    renderView()

    await user.click(await screen.findByRole('checkbox'))
    expect(screen.getByRole('alert')).toHaveTextContent('启用前请选择下载代理池')

    await user.click(screen.getByRole('checkbox'))
    await user.clear(screen.getByLabelText('回落速率'))
    await user.type(screen.getByLabelText('回落速率'), '6')
    expect(screen.getByRole('alert')).toHaveTextContent('回落速率必须低于触发速率')
    expect(screen.getByRole('button', { name: '保存策略' })).toBeDisabled()
  })

  it('allows only disabling while the download pool is active', async () => {
    const user = userEvent.setup()
    api.getTrafficPolicy.mockResolvedValue({
      ...policy,
      enabled: true,
      downloadPoolId: 'download-pool',
      state: 'active',
      originalPoolName: '日常代理池',
      releaseProgressSeconds: 12,
    })
    renderView()

    expect(await screen.findByText('下载池接管期间只能停用策略')).toBeInTheDocument()
    expect(screen.getByLabelText('触发速率')).toBeDisabled()
    expect(screen.getByRole('region', { name: '流量策略状态' })).toHaveTextContent('回落进度')

    await user.click(screen.getByRole('checkbox'))
    await user.click(screen.getByRole('button', { name: '保存策略' }))
    await waitFor(() => expect(api.updateTrafficPolicy).toHaveBeenCalledWith(expect.objectContaining({ enabled: false })))
  })

  it('shows a retry action when the policy cannot be loaded', async () => {
    api.getTrafficPolicy.mockRejectedValueOnce(new Error('网络中断')).mockResolvedValue(policy)
    renderView()

    expect(await screen.findByText('流量策略加载失败')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('heading', { name: '下载代理策略' })).toBeInTheDocument()
  })
})
