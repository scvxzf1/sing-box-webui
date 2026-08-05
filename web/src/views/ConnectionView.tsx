import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, CircleStop, Layers3, ListTree, Network, Play, Shield, TriangleAlert } from 'lucide-react'
import {
  applyRuntime,
  getRuntime,
  getSubscription,
  listNodePools,
  listSubscriptions,
  stopRuntime,
} from '../api/client'
import type { ApplyRuntime } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'
import { QuickTest } from './QuickTest'
import { NodeDiagnostic } from './NodeDiagnostic'

type Mode = ApplyRuntime['mode']
type TargetType = 'node' | 'pool'

export function ConnectionView() {
  const queryClient = useQueryClient()
  const [subscriptionId, setSubscriptionId] = useState('')
  const [poolId, setPoolId] = useState('')
  const [targetType, setTargetType] = useState<TargetType>('node')
  const [mode, setMode] = useState<Mode>('tun')
  const subscriptionsQuery = useQuery({
    queryKey: ['subscriptions'],
    queryFn: ({ signal }) => listSubscriptions(signal),
  })
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: ({ signal }) => listNodePools(signal) })
  useEffect(() => {
    if (!subscriptionId && subscriptionsQuery.data?.length) {
      setSubscriptionId(subscriptionsQuery.data.find((item) => item.active)?.id ?? subscriptionsQuery.data[0].id)
    }
  }, [subscriptionId, subscriptionsQuery.data])
  useEffect(() => {
    if (!poolId && poolsQuery.data?.length) setPoolId(poolsQuery.data[0].id)
  }, [poolId, poolsQuery.data])
  const detailQuery = useQuery({
    queryKey: ['subscription', subscriptionId],
    queryFn: ({ signal }) => getSubscription(subscriptionId, signal),
    enabled: targetType === 'node' && subscriptionId !== '',
  })
  const runtimeQuery = useQuery({
    queryKey: ['runtime'],
    queryFn: ({ signal }) => getRuntime(signal),
    refetchInterval: 3_000,
  })
  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ['runtime'] })
    await queryClient.invalidateQueries({ queryKey: ['status'] })
  }
  const applyMutation = useMutation({ mutationFn: applyRuntime, onSuccess: invalidate })
  const stopMutation = useMutation({ mutationFn: stopRuntime, onSuccess: invalidate })
  const subscription = detailQuery.data
  const selectedNode = subscription?.nodes?.find((node) => node.selected)
  const selectedPool = poolsQuery.data?.find((pool) => pool.id === poolId)
  const runtime = runtimeQuery.data
  const capability = mode === 'system-proxy' ? runtime?.capabilities.systemProxy : runtime?.capabilities.tun
  const hasTarget = targetType === 'node' ? Boolean(selectedNode) : Boolean(selectedPool && selectedPool.availableCount >= 2)
  const canApply = Boolean(hasTarget && runtime?.capabilities.singBox.available && capability?.available && !applyMutation.isPending)
  const targetLabel = targetType === 'node' ? selectedNode?.name : selectedPool?.name
  const activePoolHealth = runtime?.state === 'running' && runtime.poolId === selectedPool?.id ? runtime.poolHealth : undefined
  const isRunning = runtime?.state === 'running'
  const isApplying = applyMutation.isPending
  const isStopping = stopMutation.isPending
  const isBusy = isApplying || isStopping

  useEffect(() => {
    if (runtime?.state === 'running' && runtime.mode) setMode(runtime.mode)
  }, [runtime?.mode, runtime?.state])

  return (
    <>
      <PageHeading
        eyebrow="CONNECTION MODE"
        title="连接与应用"
        action={
          <span className={`runtime-badge runtime-badge--${runtime?.state ?? 'stopped'}`}>
            {runtime?.state === 'running' ? '代理运行中' : runtime?.state === 'failed' ? '运行失败' : '代理已停止'}
          </span>
        }
      />

      {(subscriptionsQuery.error || poolsQuery.error || detailQuery.error || runtimeQuery.error || applyMutation.error || stopMutation.error) && (
        <InlineError error={subscriptionsQuery.error ?? poolsQuery.error ?? detailQuery.error ?? runtimeQuery.error ?? applyMutation.error ?? stopMutation.error} />
      )}

      <section className="connection-layout">
        <aside className="apply-panel" aria-label="应用代理">
          <div className="apply-panel__summary">
            <span>即将应用</span>
            <strong>{targetLabel ?? '未选择目标'}</strong>
          </div>
          <div className="apply-panel__mode">
            <span>代理模式</span>
            <strong>{mode === 'system-proxy' ? '系统代理' : 'TUN 代理'}</strong>
          </div>
          {!runtime?.capabilities.singBox.available && (
            <div className="capability-note apply-panel__capability">
              <TriangleAlert size={15} aria-hidden="true" />
              {runtime?.capabilities.singBox.detail ?? 'sing-box 核心不可用'}
            </div>
          )}
          <div className="apply-panel__action">
            {isRunning ? (
              <button
                className="button button--stop"
                type="button"
                disabled={isBusy}
                onClick={() => stopMutation.mutate()}
              >
                <CircleStop size={16} aria-hidden="true" />
                {isStopping ? '正在停止…' : '停止'}
              </button>
            ) : (
              <button
                className="button button--primary"
                type="button"
                disabled={!canApply || isBusy}
                onClick={() =>
                  targetType === 'pool'
                    ? selectedPool && applyMutation.mutate({ poolId: selectedPool.id, mode })
                    : selectedNode && applyMutation.mutate({ subscriptionId, nodeId: selectedNode.id, mode })
                }
              >
                <Play size={16} aria-hidden="true" />
                {isApplying ? '正在开启…' : '开启'}
              </button>
            )}
          </div>
        </aside>

        <div className="connection-main">
          <div className="connection-step connection-step--target">
            <span className="step-index">1</span>
            <div>
              <h2>连接目标</h2>
              <div className="segmented-control connection-target-control" role="group" aria-label="连接目标类型">
                <button className={targetType === 'node' ? 'segmented-control--active' : ''} type="button" onClick={() => setTargetType('node')}><ListTree size={16} />单节点</button>
                <button className={targetType === 'pool' ? 'segmented-control--active' : ''} type="button" onClick={() => setTargetType('pool')}><Layers3 size={16} />节点池</button>
              </div>
              {targetType === 'node' ? (
                <select aria-label="选择订阅" value={subscriptionId} onChange={(event) => setSubscriptionId(event.target.value)}>
                  {(subscriptionsQuery.data ?? []).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                </select>
              ) : (
                <select aria-label="选择节点池" value={poolId} onChange={(event) => setPoolId(event.target.value)}>
                  {(poolsQuery.data ?? []).map((pool) => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
                </select>
              )}
            </div>
          </div>

          <div className="connection-step">
            <span className="step-index">2</span>
            <div>
              <h2>{targetType === 'node' ? '使用节点' : '池状态'}</h2>
              {targetType === 'node' && selectedNode ? (
                <div className="selected-node-line">
                  <strong>{selectedNode.name}</strong>
                  <span>{selectedNode.type} · {selectedNode.server}:{selectedNode.port}</span>
                </div>
              ) : targetType === 'pool' && selectedPool ? (
                <div className="selected-node-line">
                  <strong>{selectedPool.name}</strong>
                  <span>{selectedPool.availableCount}/{selectedPool.memberCount} 个成员可用 · 每 {selectedPool.probeIntervalSeconds} 秒探测</span>
                  {activePoolHealth && (
                    <div className="pool-health-summary">
                      <span className={`pool-health-state pool-health-state--${activePoolHealth.idle ? 'idle' : activePoolHealth.state}`}><Activity size={14} />{activePoolHealth.idle ? '空闲，低频探测' : formatHealthState(activePoolHealth.state)}</span>
                      <span>{activePoolHealth.healthyCount} 健康 · {activePoolHealth.degradedCount} 降级</span>
                      {activePoolHealth.selectedName && <span>当前：{activePoolHealth.selectedName}</span>}
                    </div>
                  )}
                </div>
              ) : (
                <div className="muted-line">{targetType === 'pool' ? '请先创建至少包含两个可用成员的节点池' : '请先在节点页选择一个节点'}</div>
              )}
            </div>
          </div>

          <div className="connection-step">
            <span className="step-index">3</span>
            <div>
              <h2>代理模式</h2>
              <div className="segmented-control" role="group" aria-label="代理模式">
                <button
                  className={mode === 'tun' ? 'segmented-control--active' : ''}
                  type="button"
                  disabled={isRunning}
                  onClick={() => setMode('tun')}
                >
                  <Shield size={16} aria-hidden="true" />
                  TUN
                </button>
                <button
                  className={mode === 'system-proxy' ? 'segmented-control--active' : ''}
                  type="button"
                  disabled={isRunning}
                  onClick={() => setMode('system-proxy')}
                >
                  <Network size={16} aria-hidden="true" />
                  系统代理
                </button>
              </div>
              <div className={`capability-note ${capability?.available ? 'capability-note--ok' : ''}`}>
                {!capability?.available && <TriangleAlert size={15} aria-hidden="true" />}
                {capability?.detail ?? '正在检测系统能力'}
                {mode === 'system-proxy' && capability?.available && (
                  <span className="capability-hint">仅对遵循系统代理的应用生效（如 Firefox / 终端）；Chrome 默认不读取，全局代理请用 TUN</span>
                )}
                {isRunning && <span className="capability-hint">停止代理后可切换模式</span>}
              </div>
            </div>
          </div>

          <QuickTest />
          <NodeDiagnostic kind="quality" step={5} />
          <NodeDiagnostic kind="exit" step={6} />
        </div>
      </section>
    </>
  )
}

function formatHealthState(state: 'unknown' | 'healthy' | 'degraded' | 'outage') {
  if (state === 'healthy') return '节点池健康'
  if (state === 'degraded') return '部分节点降级'
  if (state === 'outage') return '节点池故障'
  return '健康检查中'
}
