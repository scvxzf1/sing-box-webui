import { useEffect, useState, type CSSProperties, type DragEvent, type KeyboardEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, ArrowRight, ArrowRightLeft, Cable, CircleStop, GitBranch, GripVertical, Layers3, ListTree, Network, Play, RotateCcw, Shield, TriangleAlert } from 'lucide-react'
import {
  applyRuntime,
  getRuntime,
  getSubscription,
  listNodePools,
  listProxyChains,
  listSubscriptions,
  stopRuntime,
  updateRuntimePreferences,
} from '../api/client'
import type { ApplyRuntime } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'
import { QuickTest } from './QuickTest'
import { NodeDiagnostic } from './NodeDiagnostic'

type Mode = ApplyRuntime['mode']
type TargetType = 'node' | 'pool' | 'chain' | 'direct'
type ConnectionLayoutItemID = 'target' | 'selection' | 'mode' | 'lan' | 'quality' | 'exit' | 'quick'

const targetTypeStorageKey = 'sing-box-webui:connection-target-type'
const subscriptionIdStorageKey = 'sing-box-webui:connection-subscription-id'
const poolIdStorageKey = 'sing-box-webui:connection-pool-id'
const chainIdStorageKey = 'sing-box-webui:connection-chain-id'
const layoutOrderStorageKey = 'sing-box-webui:connection-layout-order-v1'
const defaultLayoutOrder: ConnectionLayoutItemID[] = ['target', 'selection', 'mode', 'lan', 'quality', 'exit', 'quick']
const layoutLabels: Record<ConnectionLayoutItemID, string> = {
  target: '连接目标',
  selection: '使用节点',
  mode: '代理模式',
  lan: '局域网连接',
  quality: '节点质量检测',
  exit: '节点落地检测',
  quick: '快速测试',
}

export function ConnectionView() {
  const queryClient = useQueryClient()
  const [subscriptionId, setSubscriptionId] = useState(readStoredSubscriptionId)
  const [poolId, setPoolId] = useState(readStoredPoolId)
  const [chainId, setChainId] = useState(readStoredChainId)
  const [targetType, setTargetType] = useState<TargetType>(readStoredTargetType)
  const [mode, setMode] = useState<Mode>('tun')
  const [allowLan, setAllowLan] = useState(false)
  const [layoutOrder, setLayoutOrder] = useState<ConnectionLayoutItemID[]>(readStoredLayoutOrder)
  const [draggingItem, setDraggingItem] = useState<ConnectionLayoutItemID | null>(null)
  const [dropIndicator, setDropIndicator] = useState<{ id: ConnectionLayoutItemID; position: 'before' | 'after' } | null>(null)
  const subscriptionsQuery = useQuery({
    queryKey: ['subscriptions'],
    queryFn: ({ signal }) => listSubscriptions(signal),
  })
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: ({ signal }) => listNodePools(signal) })
  const chainsQuery = useQuery({ queryKey: ['chains'], queryFn: ({ signal }) => listProxyChains(signal) })
  useEffect(() => {
    if (!subscriptionsQuery.data || subscriptionsQuery.data.some((item) => item.id === subscriptionId)) return
    setSubscriptionId(subscriptionsQuery.data.find((item) => item.active)?.id ?? subscriptionsQuery.data[0]?.id ?? '')
  }, [subscriptionId, subscriptionsQuery.data])
  useEffect(() => {
    if (!poolsQuery.data || poolsQuery.data.some((pool) => pool.id === poolId)) return
    setPoolId(poolsQuery.data[0]?.id ?? '')
  }, [poolId, poolsQuery.data])
  useEffect(() => {
    if (!chainsQuery.data || chainsQuery.data.some((chain) => chain.id === chainId)) return
    setChainId(chainsQuery.data[0]?.id ?? '')
  }, [chainId, chainsQuery.data])
  useEffect(() => {
    window.localStorage.setItem(targetTypeStorageKey, targetType)
  }, [targetType])
  useEffect(() => {
    window.localStorage.setItem(subscriptionIdStorageKey, subscriptionId)
  }, [subscriptionId])
  useEffect(() => {
    window.localStorage.setItem(poolIdStorageKey, poolId)
  }, [poolId])
  useEffect(() => {
    window.localStorage.setItem(chainIdStorageKey, chainId)
  }, [chainId])
  useEffect(() => {
    window.localStorage.setItem(layoutOrderStorageKey, JSON.stringify(layoutOrder))
  }, [layoutOrder])
  const hasSelectedSubscription = Boolean(subscriptionsQuery.data?.some((item) => item.id === subscriptionId))
  const detailQuery = useQuery({
    queryKey: ['subscription', subscriptionId],
    queryFn: ({ signal }) => getSubscription(subscriptionId, signal),
    enabled: targetType === 'node' && hasSelectedSubscription,
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
  const preferencesMutation = useMutation({
    mutationFn: updateRuntimePreferences,
    onSuccess: (updatedRuntime) => queryClient.setQueryData(['runtime'], updatedRuntime),
    onError: () => setAllowLan(Boolean(runtime?.allowLan)),
  })
  const stopMutation = useMutation({
    mutationFn: stopRuntime,
    onSuccess: async () => {
      applyMutation.reset()
      await invalidate()
    },
  })
  const subscription = detailQuery.data
  const selectedNode = subscription?.nodes?.find((node) => node.selected)
  const selectedPool = poolsQuery.data?.find((pool) => pool.id === poolId)
  const selectedChain = chainsQuery.data?.find((chain) => chain.id === chainId)
  const runtime = runtimeQuery.data
  const applyError = stopMutation.isSuccess ? undefined : applyMutation.error
  const capability = mode === 'system-proxy' ? runtime?.capabilities.systemProxy : runtime?.capabilities.tun
  const hasTarget = targetType === 'node'
    ? Boolean(selectedNode)
    : targetType === 'pool'
      ? Boolean(selectedPool && selectedPool.availableCount >= 2)
      : targetType === 'chain'
        ? Boolean(selectedChain?.available)
        : true
  const canApply = Boolean(hasTarget && runtime?.capabilities.singBox.available && capability?.available && !applyMutation.isPending)
  const targetLabel = targetType === 'node' ? selectedNode?.name : targetType === 'pool' ? selectedPool?.name : targetType === 'chain' ? selectedChain?.name : '直连'
  const activePoolHealth = runtime?.state === 'running' && runtime.poolId === selectedPool?.id ? runtime.poolHealth : undefined
  const selectedPoolMemberHealth = activePoolHealth?.members.find((member) => member.nodeId === activePoolHealth.selectedNodeId)
  const isRunning = runtime?.state === 'running'
  const isApplying = applyMutation.isPending
  const isStopping = stopMutation.isPending
  const isBusy = isApplying || isStopping
  const applyingLabel = targetType === 'pool' && selectedPool
    ? `正在检测 ${selectedPool.availableCount} 个节点…`
    : targetType === 'chain' && selectedChain?.entryType === 'pool'
      ? `正在检测 ${selectedChain.entryMemberCount ?? 0} 条链路…`
      : isRunning ? '正在切换…' : '正在开启…'
  const isActiveTarget = targetType === 'direct'
    ? runtime?.targetType === 'direct'
    : targetType === 'pool'
    ? Boolean(selectedPool && runtime?.targetType === 'pool' && runtime.poolId === selectedPool.id)
    : targetType === 'chain'
      ? Boolean(selectedChain && runtime?.targetType === 'chain' && runtime.chainId === selectedChain.id)
      : Boolean(selectedNode && runtime?.targetType === 'node' && runtime.subscriptionId === subscriptionId && runtime.nodeId === selectedNode.id)
  const applySelectedTarget = () => {
    stopMutation.reset()
    if (targetType === 'pool') {
      if (selectedPool) applyMutation.mutate({ poolId: selectedPool.id, mode, allowLan })
      return
    }
    if (targetType === 'chain') {
      if (selectedChain) applyMutation.mutate({ chainId: selectedChain.id, mode, allowLan })
      return
    }
    if (targetType === 'direct') {
      applyMutation.mutate({ direct: true, mode, allowLan })
      return
    }
    if (selectedNode) applyMutation.mutate({ subscriptionId, nodeId: selectedNode.id, mode, allowLan })
  }

  const moveLayoutItem = (id: ConnectionLayoutItemID, direction: -1 | 1) => {
    setLayoutOrder((current) => {
      const index = current.indexOf(id)
      const nextIndex = index + direction
      if (index < 0 || nextIndex < 0 || nextIndex >= current.length) return current
      const next = [...current]
      ;[next[index], next[nextIndex]] = [next[nextIndex], next[index]]
      return next
    })
  }

  const handleLayoutDragStart = (event: DragEvent<HTMLElement>, id: ConnectionLayoutItemID) => {
    setDraggingItem(id)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }

  const handleLayoutDragOver = (event: DragEvent<HTMLElement>, id: ConnectionLayoutItemID) => {
    event.preventDefault()
    if (!draggingItem || draggingItem === id) return
    const bounds = event.currentTarget.getBoundingClientRect()
    const nearMiddleRow = Math.abs(event.clientY - (bounds.top + bounds.height / 2)) <= bounds.height * 0.3
    const position = nearMiddleRow
      ? event.clientX < bounds.left + bounds.width / 2 ? 'before' : 'after'
      : event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
    setDropIndicator({ id, position })
    event.dataTransfer.dropEffect = 'move'
  }

  const handleLayoutDrop = (event: DragEvent<HTMLElement>, targetID: ConnectionLayoutItemID) => {
    event.preventDefault()
    const sourceID = draggingItem ?? event.dataTransfer.getData('text/plain') as ConnectionLayoutItemID
    const indicator = dropIndicator
    setDraggingItem(null)
    setDropIndicator(null)
    if (!defaultLayoutOrder.includes(sourceID) || sourceID === targetID || !indicator || indicator.id !== targetID) return
    setLayoutOrder((current) => {
      const next = current.filter((id) => id !== sourceID)
      const targetIndex = next.indexOf(targetID)
      if (targetIndex < 0) return current
      next.splice(targetIndex + (indicator.position === 'after' ? 1 : 0), 0, sourceID)
      return next
    })
  }

  const handleLayoutKeyDown = (event: KeyboardEvent<HTMLButtonElement>, id: ConnectionLayoutItemID) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowUp' && event.key !== 'ArrowRight' && event.key !== 'ArrowDown') return
    event.preventDefault()
    moveLayoutItem(id, event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1)
  }

  const layoutItemProps = (id: ConnectionLayoutItemID, className: string) => ({
    className: `${className} connection-layout-item ${draggingItem === id ? 'connection-layout-item--dragging' : ''} ${dropIndicator?.id === id ? `connection-layout-item--drop-${dropIndicator.position}` : ''}`,
    role: 'group',
    'aria-label': `${layoutLabels[id]}，可拖动排序`,
    'data-layout-id': id,
    style: { order: layoutOrder.indexOf(id) } as CSSProperties,
    onDragOver: (event: DragEvent<HTMLElement>) => handleLayoutDragOver(event, id),
    onDrop: (event: DragEvent<HTMLElement>) => handleLayoutDrop(event, id),
  })

  const layoutHandle = (id: ConnectionLayoutItemID) => (
    <button
      className="connection-layout-handle"
      type="button"
      draggable
      title="拖动调整位置"
      aria-label={`调整 ${layoutLabels[id]} 位置`}
      onDragStart={(event) => handleLayoutDragStart(event, id)}
      onDragEnd={() => { setDraggingItem(null); setDropIndicator(null) }}
      onKeyDown={(event) => handleLayoutKeyDown(event, id)}
    >
      <GripVertical size={15} aria-hidden="true" />
    </button>
  )

  const stepNumber = (id: ConnectionLayoutItemID) => layoutOrder.indexOf(id) + 1

  useEffect(() => {
    if (runtime?.state === 'running' && runtime.mode) setMode(runtime.mode)
  }, [runtime?.mode, runtime?.state])

  useEffect(() => {
    if (runtime?.state) setAllowLan(Boolean(runtime.allowLan))
  }, [runtime?.allowLan, runtime?.state])

  return (
    <>
      <PageHeading
        eyebrow="CONNECTION MODE"
        title="连接与应用"
        action={
          <div className="page-heading-actions">
            <span className={`runtime-badge runtime-badge--${runtime?.state ?? 'stopped'}`}>
              {runtime?.state === 'running' ? (runtime.targetType === 'direct' ? '直连运行中' : '代理运行中') : runtime?.state === 'failed' ? '运行失败' : '代理已停止'}
            </span>
            <button
              className="icon-button"
              type="button"
              title="恢复默认排列"
              aria-label="恢复连接配置默认排列"
              disabled={layoutOrder.every((id, index) => id === defaultLayoutOrder[index])}
              onClick={() => setLayoutOrder([...defaultLayoutOrder])}
            >
              <RotateCcw size={15} aria-hidden="true" />
            </button>
          </div>
        }
      />

      {(subscriptionsQuery.error || poolsQuery.error || chainsQuery.error || detailQuery.error || runtimeQuery.error || applyError || stopMutation.error || preferencesMutation.error) && (
        <InlineError error={subscriptionsQuery.error ?? poolsQuery.error ?? chainsQuery.error ?? detailQuery.error ?? runtimeQuery.error ?? applyError ?? stopMutation.error ?? preferencesMutation.error} />
      )}

      <section className="connection-layout">
        <aside className="apply-panel" aria-label="应用代理">
          <div className="apply-panel__summary">
            <span>{isRunning ? (isActiveTarget ? '当前运行' : '即将热切换') : '即将应用'}</span>
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
              <>
                {!isActiveTarget && (
                  <button
                    className="button button--primary apply-panel__switch-button"
                    type="button"
                    disabled={!canApply || isBusy}
                    onClick={applySelectedTarget}
                  >
                    <ArrowRightLeft size={16} aria-hidden="true" />
                    {isApplying ? applyingLabel : '热切换'}
                  </button>
                )}
                <button
                  className="button button--stop"
                  type="button"
                  disabled={isStopping}
                  onClick={() => stopMutation.mutate()}
                >
                  <CircleStop size={16} aria-hidden="true" />
                  {isStopping ? '正在停止…' : isApplying ? '取消并停止' : '停止'}
                </button>
              </>
            ) : (
              <button
                className="button button--primary apply-panel__switch-button"
                type="button"
                disabled={!canApply || isBusy}
                onClick={applySelectedTarget}
              >
                <Play size={16} aria-hidden="true" />
                {isApplying ? applyingLabel : '开启'}
              </button>
            )}
          </div>
        </aside>

        <div className="connection-main">
          <div {...layoutItemProps('target', 'connection-step connection-step--panel')}>
            {layoutHandle('target')}
            <span className="step-index">{stepNumber('target')}</span>
            <div>
              <h2>连接目标</h2>
              <div className="segmented-control connection-target-control" role="group" aria-label="连接目标类型">
                <button className={targetType === 'node' ? 'segmented-control--active' : ''} type="button" onClick={() => setTargetType('node')}><ListTree size={16} />单节点</button>
                <button className={targetType === 'pool' ? 'segmented-control--active' : ''} type="button" onClick={() => setTargetType('pool')}><Layers3 size={16} />节点池</button>
                <button className={targetType === 'chain' ? 'segmented-control--active' : ''} type="button" onClick={() => setTargetType('chain')}><GitBranch size={16} />链式代理</button>
                <button className={targetType === 'direct' ? 'segmented-control--active' : ''} type="button" onClick={() => setTargetType('direct')}><Cable size={16} />直连</button>
              </div>
              {targetType === 'direct' ? (
                <div className="selected-node-line"><strong>直连</strong><span>不经过代理节点</span></div>
              ) : targetType === 'node' ? (
                <select aria-label="选择订阅" value={subscriptionId} onChange={(event) => setSubscriptionId(event.target.value)}>
                  {(subscriptionsQuery.data ?? []).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                </select>
              ) : targetType === 'pool' ? (
                <select aria-label="选择节点池" value={poolId} onChange={(event) => setPoolId(event.target.value)}>
                  {(poolsQuery.data ?? []).map((pool) => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
                </select>
              ) : (
                <select aria-label="选择链式代理" value={chainId} onChange={(event) => setChainId(event.target.value)}>
                  {(chainsQuery.data ?? []).map((chain) => <option key={chain.id} value={chain.id}>{chain.name}{chain.available ? '' : ' · 不可用'}</option>)}
                </select>
              )}
            </div>
          </div>

          <div {...layoutItemProps('selection', 'connection-step connection-step--panel')}>
            {layoutHandle('selection')}
            <span className="step-index">{stepNumber('selection')}</span>
            <div>
              <h2>{targetType === 'node' ? '使用节点' : targetType === 'pool' ? '池状态' : targetType === 'chain' ? '链路状态' : '连接状态'}</h2>
              {targetType === 'direct' ? (
                <div className="selected-node-line"><strong>直连</strong><span>当前流量将直接访问目标地址</span></div>
              ) : targetType === 'node' && selectedNode ? (
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
                      {activePoolHealth.selectedName && <span>当前：{activePoolHealth.selectedName}{selectedPoolMemberHealth ? ` · 快测 ${selectedPoolMemberHealth.passedTests}/${selectedPoolMemberHealth.totalTests}` : ''}</span>}
                    </div>
                  )}
                </div>
              ) : targetType === 'chain' && selectedChain ? (
                <div className="selected-node-line chain-runtime-summary">
                  <strong>{selectedChain.name}</strong>
                  <span><em>{selectedChain.entryName || '入口失效'}</em><ArrowRight size={15} /><em>{selectedChain.exitName || '出口失效'}</em></span>
                  <span>{selectedChain.entryType === 'pool' ? `节点池入口 · ${selectedChain.entryMemberCount ?? 0} 个可用成员` : '单节点入口'} · 完整链路探测</span>
                  {!selectedChain.available && <span className="chain-runtime-error"><TriangleAlert size={14} />{selectedChain.unavailableReason || '链路不可用'}</span>}
                </div>
              ) : (
                <div className="muted-line">{targetType === 'pool' ? '请先创建至少包含两个可用成员的节点池' : targetType === 'chain' ? '请先在链式代理页创建可用链路' : '请先在节点页选择一个节点'}</div>
              )}
            </div>
          </div>

          <div {...layoutItemProps('mode', 'connection-step connection-step--panel')}>
            {layoutHandle('mode')}
            <span className="step-index">{stepNumber('mode')}</span>
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

          <div {...layoutItemProps('lan', 'connection-step connection-step--panel')}>
            {layoutHandle('lan')}
            <span className="step-index">{stepNumber('lan')}</span>
            <div>
              <h2>局域网连接</h2>
              <label className="lan-toggle">
                <input
                  type="checkbox"
                  checked={allowLan}
                  disabled={isRunning || preferencesMutation.isPending}
                  onChange={(event) => {
                    const next = event.target.checked
                    setAllowLan(next)
                    preferencesMutation.mutate({ allowLan: next })
                  }}
                />
                <span aria-hidden="true" />
                <em>{allowLan ? '已开放' : '仅本机'}</em>
              </label>
              <div className="capability-note capability-note--ok">
                {allowLan
                  ? '局域网内其他设备可将代理指向本机 IP 出网（监听 0.0.0.0）'
                  : '仅本机使用代理，不对外暴露监听端口'}
                {isRunning && <span className="capability-hint">停止代理后可切换</span>}
              </div>
            </div>
          </div>

          <div {...layoutItemProps('quality', 'connection-layout-wrapper connection-layout-item--wide')}>
            {layoutHandle('quality')}
            <NodeDiagnostic kind="quality" step={stepNumber('quality')} mode={runtime?.state === 'running' ? runtime.mode : undefined} />
          </div>
          <div {...layoutItemProps('exit', 'connection-layout-wrapper connection-layout-item--wide')}>
            {layoutHandle('exit')}
            <NodeDiagnostic kind="exit" step={stepNumber('exit')} mode={runtime?.state === 'running' ? runtime.mode : undefined} />
          </div>
          <div {...layoutItemProps('quick', 'connection-layout-wrapper connection-layout-item--full')}>
            {layoutHandle('quick')}
            <QuickTest step={stepNumber('quick')} mode={runtime?.state === 'running' ? runtime.mode : undefined} />
          </div>
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

function readStoredTargetType(): TargetType {
  const value = window.localStorage.getItem(targetTypeStorageKey)
  return value === 'pool' || value === 'chain' || value === 'direct' ? value : 'node'
}

function readStoredSubscriptionId() {
  return window.localStorage.getItem(subscriptionIdStorageKey) ?? ''
}

function readStoredPoolId() {
  return window.localStorage.getItem(poolIdStorageKey) ?? ''
}

function readStoredChainId() {
  return window.localStorage.getItem(chainIdStorageKey) ?? ''
}

function readStoredLayoutOrder(): ConnectionLayoutItemID[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(layoutOrderStorageKey) ?? '[]') as unknown
    if (!Array.isArray(parsed)) return [...defaultLayoutOrder]
    const known = new Set(defaultLayoutOrder)
    const stored = parsed.filter((id): id is ConnectionLayoutItemID => typeof id === 'string' && known.has(id as ConnectionLayoutItemID))
    return [...new Set([...stored, ...defaultLayoutOrder])]
  } catch {
    return [...defaultLayoutOrder]
  }
}
