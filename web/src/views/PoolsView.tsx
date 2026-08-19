import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowRight, Gauge, GripVertical, Layers3, LoaderCircle, Plus, Save, Search, Settings2, Trash2, X } from 'lucide-react'
import { createNodePool, deleteNodePool, listNodePools, reorderNodePools, testNodeLatency, updateNodePool } from '../api/client'
import type { LatencyResult, NodePool } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

const defaultInterval = 60
const defaultTolerance = 80
const defaultProbeUrl = 'https://cp.cloudflare.com/generate_204'
const defaultIdleTimeout = 1800
const defaultHighLatencyThreshold = 3000
const defaultConsecutiveFailures = 2
const defaultRecoverySuccesses = 2
const defaultMaxBackoff = 300
const maxConcurrentManualTests = 4
const poolGridColumnsStorageKey = 'sing-box-webui:pools-grid-columns'

type PoolMember = NodePool['members'][number]
type PoolLatencyResult = { key: string; result: LatencyResult }
type PoolGridColumns = 1 | 2 | 3
type PoolSettingsDraft = {
  probeUrl: string
  fallbackProbeUrls: string
  interval: number
  tolerance: number
  idleTimeout: number
  highLatencyThreshold: number
  consecutiveFailures: number
  recoverySuccesses: number
  maxBackoff: number
  interruptExistingConnections: boolean
}

export function PoolsView() {
  const queryClient = useQueryClient()
  const [selectedPoolId, setSelectedPoolId] = useState('')
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [interval, setInterval] = useState(defaultInterval)
  const [tolerance, setTolerance] = useState(defaultTolerance)
  const [probeUrl, setProbeUrl] = useState(defaultProbeUrl)
  const [fallbackProbeUrls, setFallbackProbeUrls] = useState<string[]>([])
  const [idleTimeout, setIdleTimeout] = useState(defaultIdleTimeout)
  const [highLatencyThreshold, setHighLatencyThreshold] = useState(defaultHighLatencyThreshold)
  const [consecutiveFailures, setConsecutiveFailures] = useState(defaultConsecutiveFailures)
  const [recoverySuccesses, setRecoverySuccesses] = useState(defaultRecoverySuccesses)
  const [maxBackoff, setMaxBackoff] = useState(defaultMaxBackoff)
  const [interruptExistingConnections, setInterruptExistingConnections] = useState(false)
  const [settingsDraft, setSettingsDraft] = useState<PoolSettingsDraft | null>(null)
  const [members, setMembers] = useState<PoolMember[]>([])
  const [memberSearch, setMemberSearch] = useState('')
  const [latencyResults, setLatencyResults] = useState<Record<string, LatencyResult>>({})
  const [testingKeys, setTestingKeys] = useState<Set<string>>(new Set())
  const [testingAll, setTestingAll] = useState(false)
  const [latencyError, setLatencyError] = useState<unknown>(null)
  const [gridColumns, setGridColumns] = useState<PoolGridColumns>(readPoolGridColumns)
  // Tracks which pool (or the create form) the editor fields currently mirror.
  // Background refetches must not clobber unsaved edits for the same pool.
  const syncedFormKeyRef = useRef('')
  const activePoolKeyRef = useRef('')

  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: ({ signal }) => listNodePools(signal) })
  const [draggingId, setDraggingId] = useState<string | null>(null)
  const [dropIndicator, setDropIndicator] = useState<{ id: string; position: 'before' | 'after' } | null>(null)
  const selectedPool = poolsQuery.data?.find((pool) => pool.id === selectedPoolId)

  useEffect(() => {
    if (!creating && !selectedPoolId && poolsQuery.data?.length) setSelectedPoolId(poolsQuery.data[0].id)
    if (!creating && selectedPoolId && poolsQuery.data && !poolsQuery.data.some((pool) => pool.id === selectedPoolId)) {
      setSelectedPoolId(poolsQuery.data[0]?.id ?? '')
    }
  }, [creating, poolsQuery.data, selectedPoolId])

  useEffect(() => {
    activePoolKeyRef.current = creating ? '__create__' : selectedPoolId
    if (creating || !selectedPool) return
    if (syncedFormKeyRef.current === selectedPool.id) return
    syncedFormKeyRef.current = selectedPool.id
    setName(selectedPool.name)
    setInterval(selectedPool.probeIntervalSeconds)
    setTolerance(selectedPool.toleranceMs)
    setProbeUrl(selectedPool.probeUrl)
    setFallbackProbeUrls(selectedPool.fallbackProbeUrls)
    setIdleTimeout(selectedPool.idleTimeoutSeconds)
    setHighLatencyThreshold(selectedPool.highLatencyThresholdMs)
    setConsecutiveFailures(selectedPool.consecutiveFailures)
    setRecoverySuccesses(selectedPool.recoverySuccesses)
    setMaxBackoff(selectedPool.maxBackoffSeconds)
    setInterruptExistingConnections(selectedPool.interruptExistingConnections)
    setMembers(selectedPool.members)
    setMemberSearch('')
    setLatencyResults({})
    setTestingKeys(new Set())
    setTestingAll(false)
    setLatencyError(null)
  }, [creating, selectedPool])

  useEffect(() => {
    try {
      window.localStorage.setItem(poolGridColumnsStorageKey, String(gridColumns))
    } catch {
      // The layout still works when browser storage is unavailable.
    }
  }, [gridColumns])

  const resetForCreate = () => {
    syncedFormKeyRef.current = '__create__'
    activePoolKeyRef.current = '__create__'
    setCreating(true)
    setSelectedPoolId('')
    setName('')
    setInterval(defaultInterval)
    setTolerance(defaultTolerance)
    setProbeUrl(defaultProbeUrl)
    setFallbackProbeUrls([])
    setIdleTimeout(defaultIdleTimeout)
    setHighLatencyThreshold(defaultHighLatencyThreshold)
    setConsecutiveFailures(defaultConsecutiveFailures)
    setRecoverySuccesses(defaultRecoverySuccesses)
    setMaxBackoff(defaultMaxBackoff)
    setInterruptExistingConnections(false)
    setSettingsDraft(null)
    setMembers([])
    setMemberSearch('')
    setLatencyResults({})
    setTestingKeys(new Set())
    setTestingAll(false)
    setLatencyError(null)
  }
  const invalidate = async () => queryClient.invalidateQueries({ queryKey: ['pools'] })
  const saveMutation = useMutation({
    mutationFn: async () => {
      const input = {
        name: name.trim(),
        members: members.map((member) => ({ subscriptionId: member.subscriptionId, nodeId: member.nodeId })),
        probeIntervalSeconds: interval,
        toleranceMs: tolerance,
        probeUrl,
        fallbackProbeUrls,
        idleTimeoutSeconds: idleTimeout,
        highLatencyThresholdMs: highLatencyThreshold,
        consecutiveFailures,
        recoverySuccesses,
        maxBackoffSeconds: maxBackoff,
        interruptExistingConnections,
      }
      return creating ? createNodePool(input) : updateNodePool(selectedPoolId, input)
    },
    onSuccess: async (pool) => {
      syncedFormKeyRef.current = ''
      activePoolKeyRef.current = pool.id
      setCreating(false)
      setSelectedPoolId(pool.id)
      await invalidate()
    },
  })
  const deleteMutation = useMutation({
    mutationFn: deleteNodePool,
    onSuccess: async () => {
      activePoolKeyRef.current = ''
      setSelectedPoolId('')
      await invalidate()
    },
  })
  const reorderMutation = useMutation({
    mutationFn: reorderNodePools,
    onMutate: async (ids) => {
      await queryClient.cancelQueries({ queryKey: ['pools'] })
      const previous = queryClient.getQueryData<NodePool[]>(['pools'])
      if (previous) {
        const byID = new Map(previous.map((item) => [item.id, item]))
        queryClient.setQueryData(
          ['pools'],
          ids.map((id) => byID.get(id)).filter((item): item is NodePool => Boolean(item)),
        )
      }
      return { previous }
    },
    onSuccess: (items) => queryClient.setQueryData(['pools'], items),
    onError: (_error, _ids, context) => {
      if (context?.previous) queryClient.setQueryData(['pools'], context.previous)
    },
    onSettled: async () => {
      setDraggingId(null)
      setDropIndicator(null)
      await queryClient.invalidateQueries({ queryKey: ['pools'] })
    },
  })
  useEffect(() => {
    saveMutation.reset()
    deleteMutation.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedPoolId, creating])

  const testMembers = async (targets: PoolMember[]) => {
    const requestPoolKey = activePoolKeyRef.current
    const targetKeys = targets.map((member) => memberKey(member.subscriptionId, member.nodeId))
    const grouped = new Map<string, string[]>()
    for (const member of targets.filter((item) => item.available)) {
      grouped.set(member.subscriptionId, [...(grouped.get(member.subscriptionId) ?? []), member.nodeId])
    }
    setTestingKeys((current) => new Set([...current, ...targetKeys]))
    setLatencyError(null)
    try {
      const entries = [...grouped]
      const results: PoolLatencyResult[] = []
      for (let index = 0; index < entries.length; index += maxConcurrentManualTests) {
        const responses = await Promise.all(entries.slice(index, index + maxConcurrentManualTests).map(async ([subscriptionId, nodeIds]) => ({
          subscriptionId,
          response: await testNodeLatency(subscriptionId, { nodeIds }),
        })))
        for (const { subscriptionId, response } of responses) {
          for (const result of response.items) results.push({ key: memberKey(subscriptionId, result.nodeId), result })
        }
      }
      if (activePoolKeyRef.current !== requestPoolKey) return
      setLatencyResults((current) => {
        const next = { ...current }
        for (const item of results) next[item.key] = item.result
        return next
      })
    } catch (error) {
      if (activePoolKeyRef.current === requestPoolKey) setLatencyError(error)
    } finally {
      if (activePoolKeyRef.current !== requestPoolKey) return
      setTestingKeys((current) => {
        const next = new Set(current)
        for (const key of targetKeys) next.delete(key)
        return next
      })
    }
  }

  const testAllMembers = async () => {
    const requestPoolKey = activePoolKeyRef.current
    setTestingAll(true)
    await testMembers(availableMembers)
    if (activePoolKeyRef.current === requestPoolKey) setTestingAll(false)
  }

  const availableMembers = members.filter((member) => member.available)
  const visibleMembers = useMemo(() => {
    const normalized = memberSearch.trim().toLowerCase()
    if (!normalized) return members
    return members.filter((member) =>
      (member.nodeName ?? '').toLowerCase().includes(normalized) ||
      (member.subscriptionName ?? '').toLowerCase().includes(normalized) ||
      (member.type ?? '').toLowerCase().includes(normalized) ||
      (member.server ?? '').toLowerCase().includes(normalized),
    )
  }, [members, memberSearch])
  const canSave = name.trim().length > 0 && !saveMutation.isPending
  const openSettings = () => setSettingsDraft({
    probeUrl, fallbackProbeUrls: fallbackProbeUrls.join('\n'), interval, tolerance, idleTimeout,
    highLatencyThreshold, consecutiveFailures, recoverySuccesses, maxBackoff, interruptExistingConnections,
  })
  const applySettings = () => {
    if (!settingsDraft || !isValidSettings(settingsDraft)) return
    setProbeUrl(settingsDraft.probeUrl.trim())
    setFallbackProbeUrls(parseProbeURLs(settingsDraft.fallbackProbeUrls))
    setInterval(settingsDraft.interval)
    setTolerance(settingsDraft.tolerance)
    setIdleTimeout(settingsDraft.idleTimeout)
    setHighLatencyThreshold(settingsDraft.highLatencyThreshold)
    setConsecutiveFailures(settingsDraft.consecutiveFailures)
    setRecoverySuccesses(settingsDraft.recoverySuccesses)
    setMaxBackoff(settingsDraft.maxBackoff)
    setInterruptExistingConnections(settingsDraft.interruptExistingConnections)
    setSettingsDraft(null)
  }
  const pools = poolsQuery.data ?? []
  const handleDragStart = (event: DragEvent<HTMLButtonElement>, id: string) => {
    if (reorderMutation.isPending) return
    setDraggingId(id)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', id)
  }
  const handleDragOver = (event: DragEvent<HTMLButtonElement>, id: string) => {
    event.preventDefault()
    if (!draggingId || draggingId === id || reorderMutation.isPending) return
    const bounds = event.currentTarget.getBoundingClientRect()
    setDropIndicator({ id, position: event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after' })
    event.dataTransfer.dropEffect = 'move'
  }
  const handleDrop = (event: DragEvent<HTMLButtonElement>, targetID: string) => {
    event.preventDefault()
    const sourceID = draggingId ?? event.dataTransfer.getData('text/plain')
    const indicator = dropIndicator
    setDraggingId(null)
    setDropIndicator(null)
    if (!sourceID || sourceID === targetID || !indicator || indicator.id !== targetID || reorderMutation.isPending) return
    const ids = movePoolID(pools.map((item) => item.id), sourceID, targetID, indicator.position)
    if (ids) reorderMutation.mutate(ids)
  }

  return (
    <>
      <PageHeading
        eyebrow="FAILOVER POOLS"
        title="节点池"
        action={<button className="button" type="button" onClick={resetForCreate}><Plus size={16} />新建池</button>}
      />
      {(poolsQuery.error || saveMutation.error || deleteMutation.error || reorderMutation.error || latencyError) && (
        <InlineError error={poolsQuery.error ?? saveMutation.error ?? deleteMutation.error ?? reorderMutation.error ?? latencyError} />
      )}
      <section className="pools-layout">
        <aside className="pool-list panel" aria-label="节点池列表">
          {pools.map((pool) => (
            <button
              className={`pool-row ${pool.id === selectedPoolId && !creating ? 'pool-row--selected' : ''} ${draggingId === pool.id ? 'pool-row--dragging' : ''} ${dropIndicator?.id === pool.id ? `pool-row--drop-${dropIndicator.position}` : ''}`}
              type="button"
              key={pool.id}
              draggable={!reorderMutation.isPending}
              aria-grabbed={draggingId === pool.id}
              onDragStart={(event) => handleDragStart(event, pool.id)}
              onDragOver={(event) => handleDragOver(event, pool.id)}
              onDrop={(event) => handleDrop(event, pool.id)}
              onDragEnd={() => { setDraggingId(null); setDropIndicator(null) }}
              onClick={() => { activePoolKeyRef.current = pool.id; setCreating(false); setSelectedPoolId(pool.id) }}
            >
              <GripVertical className="drag-handle" size={16} aria-hidden="true" />
              <span><strong>{pool.name}</strong><small>{pool.availableCount}/{pool.memberCount} 可用</small></span>
              <Layers3 size={16} aria-hidden="true" />
            </button>
          ))}
          {!poolsQuery.isPending && !poolsQuery.data?.length && <div className="empty-state">尚未创建节点池</div>}
        </aside>

        <div className="pool-editor panel">
          {(creating || selectedPool) ? (
            <>
              <div className="pool-editor-header">
                <label><span>名称</span><input value={name} maxLength={80} onChange={(event) => setName(event.target.value)} placeholder="例如：日常线路" /></label>
                <div className="pool-settings-summary"><span>健康检查与自动选路</span><strong>每 {formatDuration(interval)} · {formatDuration(highLatencyThreshold / 1000)} 高延迟 · {consecutiveFailures} 次失败隔离</strong></div>
                <div className="pool-editor-actions">
                  <button className="icon-button" title="节点池设置" aria-label="节点池设置" type="button" onClick={openSettings}><Settings2 size={16} /></button>
                  {!creating && <button className="icon-button icon-button--danger" title="删除节点池" aria-label="删除节点池" type="button" disabled={deleteMutation.isPending} onClick={() => selectedPool && window.confirm(`删除节点池“${selectedPool.name}”？`) && deleteMutation.mutate(selectedPool.id)}><Trash2 size={16} /></button>}
                  <button className="button button--primary" type="button" disabled={!canSave} onClick={() => saveMutation.mutate()}><Save size={16} />{saveMutation.isPending ? '保存中' : '保存'}</button>
                </div>
              </div>
              <div className="pool-member-toolbar">
                <div>
                  <strong>池内节点</strong>
                  <span>{availableMembers.length}/{members.length} 可用{memberSearch.trim() ? ` · 匹配 ${visibleMembers.length}` : ''}</span>
                </div>
                <label className="search-field pool-member-search">
                  <Search size={16} aria-hidden="true" />
                  <input
                    aria-label="搜索池内节点"
                    value={memberSearch}
                    onChange={(event) => setMemberSearch(event.target.value)}
                    placeholder="搜索节点"
                  />
                </label>
                <label className="pool-grid-control">
                  <span>每行列数</span>
                  <select
                    aria-label="池内节点每行列数"
                    value={gridColumns}
                    onChange={(event) => setGridColumns(Number(event.target.value) as PoolGridColumns)}
                  >
                    <option value={1}>1 列</option>
                    <option value={2}>2 列</option>
                    <option value={3}>3 列</option>
                  </select>
                </label>
                <button className="button" type="button" disabled={!availableMembers.length || testingKeys.size > 0} onClick={() => void testAllMembers()}>
                  {testingAll ? <LoaderCircle className="spin" size={16} /> : <Gauge size={16} />}{testingAll ? '测试中' : '测试全部'}
                </button>
              </div>
              <div className={`pool-members pool-members--${gridColumns}`} role="list">
                {visibleMembers.map((member) => {
                  const key = memberKey(member.subscriptionId, member.nodeId)
                  const result = latencyResults[key]
                  const testing = testingKeys.has(key)
                  return (
                    <article className={`pool-member-row ${member.available ? '' : 'pool-member-row--missing'}`} role="listitem" key={key}>
                      <div className="pool-member-state">{member.available ? <span className="status-dot status-dot--ok" /> : <AlertTriangle size={15} />}</div>
                      <div className="pool-member-copy">
                        <strong title={member.nodeName || member.nodeId}>{member.nodeName || `失效节点 ${member.nodeId.slice(0, 8)}`}</strong>
                        <span>{member.subscriptionName || member.subscriptionId}{member.available ? ` · ${member.type} · ${member.server}:${member.port}` : ' · 引用失效'}</span>
                      </div>
                      <span className={`latency-result latency-result--${result?.status ?? 'idle'}`} title={result?.detail}>{formatLatency(result, testing)}</span>
                      <button className="icon-button" type="button" title={`测试 ${member.nodeName || member.nodeId} 延迟`} aria-label={`测试 ${member.nodeName || member.nodeId} 延迟`} disabled={!member.available || testing || testingKeys.size >= maxConcurrentManualTests} onClick={() => void testMembers([member])}>
                        {testing ? <LoaderCircle className="spin" size={16} /> : <Gauge size={16} />}
                      </button>
                      <button className="icon-button icon-button--danger" type="button" title="移出节点池" aria-label={`移除 ${member.nodeName || member.nodeId}`} onClick={() => setMembers((current) => current.filter((item) => memberKey(item.subscriptionId, item.nodeId) !== key))}><X size={16} /></button>
                    </article>
                  )
                })}
                {!members.length && (
                  <div className="pool-members-empty">
                    <span>池内还没有节点</span>
                    <button className="button" type="button" onClick={() => { window.location.hash = '#nodes' }}>前往节点页<ArrowRight size={16} /></button>
                  </div>
                )}
                {members.length > 0 && !visibleMembers.length && (
                  <div className="pool-members-empty">
                    <span>没有匹配“{memberSearch.trim()}”的节点</span>
                    <button className="button" type="button" onClick={() => setMemberSearch('')}>清除搜索</button>
                  </div>
                )}
              </div>
            </>
          ) : <div className="empty-state">选择一个节点池，或新建节点池</div>}
        </div>
      </section>
      {settingsDraft && (
        <div className="modal-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) setSettingsDraft(null) }}>
          <form className="pool-settings-dialog" role="dialog" aria-modal="true" aria-labelledby="pool-settings-title" onSubmit={(event) => { event.preventDefault(); applySettings() }}>
            <div className="pool-settings-heading">
              <div><strong id="pool-settings-title">节点池设置</strong><span>健康检查与自动选路</span></div>
              <button className="icon-button" type="button" aria-label="关闭节点池设置" title="关闭" onClick={() => setSettingsDraft(null)}><X size={16} /></button>
            </div>
            <div className="pool-settings-body">
              <section className="pool-settings-section" aria-labelledby="health-check-heading">
                <h2 id="health-check-heading">健康检查</h2>
                <div className="pool-settings-grid">
                  <label className="pool-settings-field pool-settings-field--wide"><span>探测地址</span><input type="url" required maxLength={2048} value={settingsDraft.probeUrl} aria-invalid={!isValidProbeUrl(settingsDraft.probeUrl)} onChange={(event) => setSettingsDraft({ ...settingsDraft, probeUrl: event.target.value })} /></label>
                  <label className="pool-settings-field pool-settings-field--wide"><span>备用探测地址</span><textarea rows={3} maxLength={8192} value={settingsDraft.fallbackProbeUrls} aria-invalid={!areValidFallbackProbeUrls(settingsDraft.fallbackProbeUrls)} placeholder="每行一个 HTTPS 地址，最多 4 个" onChange={(event) => setSettingsDraft({ ...settingsDraft, fallbackProbeUrls: event.target.value })} /></label>
                  <label className="pool-settings-field"><span>探测间隔</span><select value={settingsDraft.interval} onChange={(event) => setSettingsDraft({ ...settingsDraft, interval: Number(event.target.value) })}>
                    <option value={15}>15 秒</option><option value={30}>30 秒</option><option value={60}>1 分钟</option><option value={180}>3 分钟</option><option value={300}>5 分钟</option><option value={600}>10 分钟</option><option value={1800}>30 分钟</option><option value={3600}>1 小时</option>
                  </select></label>
                  <label className="pool-settings-field"><span>高延迟阈值</span><select value={settingsDraft.highLatencyThreshold} onChange={(event) => setSettingsDraft({ ...settingsDraft, highLatencyThreshold: Number(event.target.value) })}>
                    <option value={500}>500 ms</option><option value={1000}>1 秒</option><option value={1500}>1.5 秒</option><option value={2000}>2 秒</option><option value={3000}>3 秒</option><option value={5000}>5 秒</option><option value={10000}>10 秒</option>
                  </select></label>
                </div>
              </section>
              <section className="pool-settings-section" aria-labelledby="routing-heading">
                <h2 id="routing-heading">自动选路</h2>
                <div className="pool-settings-grid">
                  <label className="pool-settings-field"><span>切换容差</span><select value={settingsDraft.tolerance} onChange={(event) => setSettingsDraft({ ...settingsDraft, tolerance: Number(event.target.value) })}>
                    <option value={50}>50 ms</option><option value={80}>80 ms</option><option value={150}>150 ms</option><option value={300}>300 ms</option><option value={500}>500 ms</option><option value={1000}>1000 ms</option>
                  </select></label>
                  <label className="pool-settings-field"><span>空闲超时</span><select value={settingsDraft.idleTimeout} onChange={(event) => setSettingsDraft({ ...settingsDraft, idleTimeout: Number(event.target.value) })}>
                    <option value={60}>1 分钟</option><option value={300}>5 分钟</option><option value={900}>15 分钟</option><option value={1800}>30 分钟</option><option value={3600}>1 小时</option><option value={14400}>4 小时</option><option value={86400}>24 小时</option>
                  </select></label>
                  <label className="pool-settings-field"><span>连续失败隔离</span><select value={settingsDraft.consecutiveFailures} onChange={(event) => setSettingsDraft({ ...settingsDraft, consecutiveFailures: Number(event.target.value) })}>
                    <option value={1}>1 次</option><option value={2}>2 次</option><option value={3}>3 次</option><option value={5}>5 次</option>
                  </select></label>
                  <label className="pool-settings-field"><span>恢复成功次数</span><select value={settingsDraft.recoverySuccesses} onChange={(event) => setSettingsDraft({ ...settingsDraft, recoverySuccesses: Number(event.target.value) })}>
                    <option value={1}>1 次</option><option value={2}>2 次</option><option value={3}>3 次</option><option value={5}>5 次</option>
                  </select></label>
                  <label className="pool-settings-field"><span>最大退避时间</span><select value={settingsDraft.maxBackoff} onChange={(event) => setSettingsDraft({ ...settingsDraft, maxBackoff: Number(event.target.value) })}>
                    <option value={30}>30 秒</option><option value={60}>1 分钟</option><option value={120}>2 分钟</option><option value={300}>5 分钟</option><option value={600}>10 分钟</option><option value={1800}>30 分钟</option><option value={3600}>1 小时</option>
                  </select></label>
                  <label className="pool-settings-toggle pool-settings-field--wide"><input type="checkbox" checked={settingsDraft.interruptExistingConnections} onChange={(event) => setSettingsDraft({ ...settingsDraft, interruptExistingConnections: event.target.checked })} /><span><strong>切换时中断现有连接</strong><small>让已建立的连接立即使用新节点</small></span></label>
                </div>
              </section>
            </div>
            <div className="pool-settings-actions">
              <button className="button" type="button" onClick={() => setSettingsDraft(null)}>取消</button>
              <button className="button button--primary" type="submit" disabled={!isValidSettings(settingsDraft)}>应用设置</button>
            </div>
          </form>
        </div>
      )}
    </>
  )
}

function memberKey(subscriptionId: string, nodeId: string) {
  return `${subscriptionId}:${nodeId}`
}

function movePoolID(ids: string[], sourceID: string, targetID: string, position: 'before' | 'after') {
  const sourceIndex = ids.indexOf(sourceID)
  const targetIndex = ids.indexOf(targetID)
  if (sourceIndex < 0 || targetIndex < 0 || sourceID === targetID) return null
  const next = ids.filter((id) => id !== sourceID)
  const nextTargetIndex = next.indexOf(targetID)
  next.splice(nextTargetIndex + (position === 'after' ? 1 : 0), 0, sourceID)
  return next
}

function readPoolGridColumns(): PoolGridColumns {
  try {
    const value = Number(window.localStorage.getItem(poolGridColumnsStorageKey))
    if (value === 1 || value === 2 || value === 3) return value
  } catch {
    // Ignore unavailable browser storage and use the existing single-column layout.
  }
  return 1
}

function formatLatency(result: LatencyResult | undefined, testing: boolean) {
  if (testing) return '测试中'
  if (!result) return '未测试'
  if (result.status === 'ok') return result.latencyMs !== undefined ? `${result.latencyMs} ms` : '可用'
  if (result.status === 'timeout') return '超时'
  return '失败'
}

function isValidProbeUrl(value: string) {
  if (value.length > 2048) return false
  try {
    const url = new URL(value.trim())
    return url.protocol === 'https:' && Boolean(url.hostname)
  } catch {
    return false
  }
}

function parseProbeURLs(value: string) {
  return [...new Set(value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean))]
}

function areValidFallbackProbeUrls(value: string) {
  const urls = parseProbeURLs(value)
  return urls.length <= 4 && urls.every(isValidProbeUrl)
}

function isValidSettings(settings: PoolSettingsDraft) {
  return isValidProbeUrl(settings.probeUrl)
    && areValidFallbackProbeUrls(settings.fallbackProbeUrls)
    && settings.interval <= settings.idleTimeout
}

function formatDuration(seconds: number) {
  if (seconds >= 3600 && seconds % 3600 === 0) return `${seconds / 3600} 小时`
  if (seconds >= 60 && seconds % 60 === 0) return `${seconds / 60} 分钟`
  return `${seconds} 秒`
}
