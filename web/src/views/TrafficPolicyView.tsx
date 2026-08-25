import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, AlertTriangle, ArrowRightLeft, Clock3, Gauge, RefreshCcw, RotateCcw, Save, Waves } from 'lucide-react'
import { getTrafficPolicy, listNodePools, updateTrafficPolicy } from '../api/client'
import type { TrafficPolicy, UpdateTrafficPolicy } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

type Draft = UpdateTrafficPolicy

const stateLabels: Record<TrafficPolicy['state'], string> = {
  disabled: '已停用', waiting: '等待代理池', monitoring: '监控中', triggering: '切换中',
  active: '下载池已接管', recovering: '恢复中', cooldown: '冷却中', error: '异常',
}

export function TrafficPolicyView() {
  const queryClient = useQueryClient()
  const policyQuery = useQuery({ queryKey: ['traffic-policy'], queryFn: ({ signal }) => getTrafficPolicy(signal), refetchInterval: 1000 })
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: ({ signal }) => listNodePools(signal) })
  const [draft, setDraft] = useState<Draft | null>(null)
  const savedDraft = useRef<Draft | null>(null)

  useEffect(() => {
    if (!policyQuery.data) return
    const next = toDraft(policyQuery.data)
    setDraft((current) => !current || draftsEqual(current, savedDraft.current) ? next : current)
    savedDraft.current = next
  }, [policyQuery.data])

  const saveMutation = useMutation({
    mutationFn: (input: Draft) => updateTrafficPolicy(input),
    onSuccess: async (value) => {
      const next = toDraft(value)
      savedDraft.current = next
      setDraft(next)
      queryClient.setQueryData(['traffic-policy'], value)
    },
  })
  const policy = policyQuery.data
  const pools = poolsQuery.data ?? []
  const selectedDraftPool = pools.find((pool) => pool.id === draft?.downloadPoolId)
  const selectedSavedPool = pools.find((pool) => pool.id === policy?.downloadPoolId)
  const busy = policy?.state === 'triggering' || policy?.state === 'recovering'
  const active = policy?.state === 'active'
  const dirty = Boolean(draft && !draftsEqual(draft, savedDraft.current))
  const poolsReady = poolsQuery.data !== undefined
  const validationError = draft ? validateDraft(draft, selectedDraftPool, poolsReady, active && dirty) : null
  const waitingForPools = Boolean(draft?.enabled && !poolsReady)
  const changeDraft = (next: Draft) => {
    saveMutation.reset()
    setDraft(next)
  }
  const progress = policy ? progressMetric(policy) : null

  return (
    <>
      <PageHeading
        eyebrow="TRAFFIC POLICY"
        title="流量策略"
        action={(
          <div className="page-heading-actions">
            <button className="button button--ghost" type="button" disabled={!draft || !dirty || saveMutation.isPending} onClick={() => savedDraft.current && changeDraft({ ...savedDraft.current })}><RotateCcw size={16} />放弃更改</button>
            <button className="button button--primary" type="button" disabled={!draft || !dirty || Boolean(validationError) || waitingForPools || busy || saveMutation.isPending} onClick={() => draft && saveMutation.mutate(draft)}><Save size={16} />{saveMutation.isPending ? '保存中' : '保存策略'}</button>
          </div>
        )}
      />
      {(policyQuery.error || poolsQuery.error || saveMutation.error) && <InlineError error={policyQuery.error ?? poolsQuery.error ?? saveMutation.error} />}

      {policy && draft ? (
        <div className="traffic-policy-layout">
          <section className="traffic-status-band" aria-label="流量策略状态">
            <div className={`traffic-state traffic-state--${policy.state}`}><span className="status-dot" /><div><span>当前状态</span><strong>{stateLabels[policy.state]}</strong></div></div>
            <Metric icon={Waves} label="实时下行" value={formatRate(policy.currentDownloadBps)} />
            <Metric icon={Activity} label="活动连接" value={`${policy.activeConnections}`} />
            <Metric icon={Clock3} label={progress!.label} value={progress!.value} />
          </section>

          {policy.lastError && <div className="traffic-policy-alert"><AlertTriangle size={17} /><span>{policy.lastError}</span></div>}
          {(active || busy) && <div className="traffic-policy-notice" role="note">{active ? '下载池接管期间只能停用策略' : '策略正在切换代理池，完成前不能修改配置'}</div>}

          <section className="traffic-policy-section" aria-labelledby="download-policy-heading">
            <div className="traffic-section-heading"><div><Gauge size={18} /><h2 id="download-policy-heading">下载代理策略</h2></div><label className="settings-switch"><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={(event) => changeDraft(active && !event.target.checked ? { ...toDraft(policy), enabled: false } : { ...draft, enabled: event.target.checked })} /><span aria-hidden="true" /><strong>{draft.enabled ? '已启用' : '已停用'}</strong></label></div>
            <div className="traffic-policy-fields">
              <label className="traffic-field traffic-field--wide"><span>下载代理池</span><select value={draft.downloadPoolId} disabled={active || busy} onChange={(event) => changeDraft({ ...draft, downloadPoolId: event.target.value })}><option value="">选择节点池</option>{pools.map((pool) => <option value={pool.id} key={pool.id} disabled={pool.availableCount < 2}>{pool.name} · {pool.availableCount}/{pool.memberCount} 可用</option>)}</select></label>
              <NumberField label="触发速率" value={bytesToMiB(draft.triggerRateBytesPerSecond)} suffix="MiB/s" min={0.0625} max={10240} step={0.5} disabled={active || busy} onChange={(value) => changeDraft({ ...draft, triggerRateBytesPerSecond: mibToBytes(value) })} />
              <NumberField label="持续时间" value={draft.triggerDurationSeconds} suffix="秒" min={2} max={300} step={1} disabled={active || busy} onChange={(value) => changeDraft({ ...draft, triggerDurationSeconds: value })} />
              <NumberField label="回落速率" value={bytesToMiB(draft.releaseRateBytesPerSecond)} suffix="MiB/s" min={0} max={10239} step={0.5} disabled={active || busy} onChange={(value) => changeDraft({ ...draft, releaseRateBytesPerSecond: mibToBytes(value) })} />
              <NumberField label="回落等待" value={draft.releaseDurationSeconds} suffix="秒" min={5} max={3600} step={5} disabled={active || busy} onChange={(value) => changeDraft({ ...draft, releaseDurationSeconds: value })} />
              <NumberField label="冷却时间" value={draft.cooldownSeconds} suffix="秒" min={0} max={86400} step={30} disabled={active || busy} onChange={(value) => changeDraft({ ...draft, cooldownSeconds: value })} />
            </div>
            {validationError && <div className="traffic-field-error" role="alert"><AlertTriangle size={15} />{validationError}</div>}
          </section>

          <section className="traffic-policy-section" aria-labelledby="handover-heading">
            <div className="traffic-section-heading"><div><ArrowRightLeft size={18} /><h2 id="handover-heading">接管状态</h2></div>{policy.activatedAt && <time>{formatTime(policy.activatedAt)}</time>}</div>
            <div className="traffic-handover">
              <div><span>原代理池</span><strong>{policy.originalPoolName || '未接管'}</strong></div><ArrowRightLeft size={18} /><div><span>下载代理池</span><strong>{selectedSavedPool?.name || (policy.downloadPoolId ? poolsQuery.isPending ? '正在加载节点池' : poolsQuery.isError ? '节点池状态未知' : '节点池已不存在' : '未选择')}</strong></div>
            </div>
          </section>

          <section className="traffic-policy-section" aria-labelledby="traffic-events-heading">
            <div className="traffic-section-heading"><div><Clock3 size={18} /><h2 id="traffic-events-heading">近期事件</h2></div><span>{policy.events.length} 条</span></div>
            <div className="traffic-events">{policy.events.map((event, index) => <div className="traffic-event" key={`${event.timestamp}-${event.type}-${index}`}><time>{formatTime(event.timestamp)}</time><span className={`traffic-event-type traffic-event-type--${event.type}`}>{eventTypeLabel(event.type)}</span><strong>{event.message}</strong></div>)}{!policy.events.length && <div className="traffic-events-empty">暂无策略事件</div>}</div>
          </section>
        </div>
      ) : policyQuery.isError ? <div className="empty-state traffic-policy-load-error"><span>流量策略加载失败</span><button className="button" type="button" onClick={() => void Promise.all([policyQuery.refetch(), poolsQuery.refetch()])}><RefreshCcw size={15} />重试</button></div> : <div className="empty-state">加载流量策略...</div>}
    </>
  )
}

function Metric({ icon: Icon, label, value }: { icon: typeof Activity; label: string; value: string }) { return <div className="traffic-metric"><Icon size={18} /><div><span>{label}</span><strong>{value}</strong></div></div> }
function NumberField({ label, value, suffix, min, max, step, disabled, onChange }: { label: string; value: number; suffix: string; min: number; max: number; step: number; disabled: boolean; onChange: (value: number) => void }) { return <label className="traffic-field"><span>{label}</span><div><input aria-label={label} type="number" value={value} min={min} max={max} step={step} disabled={disabled} onChange={(event) => onChange(Number(event.target.value))} /><em>{suffix}</em></div></label> }
function toDraft(policy: TrafficPolicy): Draft { return { enabled: policy.enabled, downloadPoolId: policy.downloadPoolId, triggerRateBytesPerSecond: policy.triggerRateBytesPerSecond, triggerDurationSeconds: policy.triggerDurationSeconds, releaseRateBytesPerSecond: policy.releaseRateBytesPerSecond, releaseDurationSeconds: policy.releaseDurationSeconds, cooldownSeconds: policy.cooldownSeconds } }
function draftsEqual(left: Draft | null, right: Draft | null) { return Boolean(left && right && Object.keys(left).every((key) => left[key as keyof Draft] === right[key as keyof Draft])) }
function validateDraft(draft: Draft, pool: { availableCount: number } | undefined, poolsReady: boolean, activeAndDirty: boolean) {
  if (activeAndDirty && draft.enabled) return '下载池接管期间只能停用策略'
  if (!Number.isFinite(draft.triggerRateBytesPerSecond) || draft.triggerRateBytesPerSecond < 64 << 10 || draft.triggerRateBytesPerSecond > 10 * (1 << 30)) return '触发速率必须在 64 KiB/s 到 10 GiB/s 之间'
  if (!Number.isFinite(draft.releaseRateBytesPerSecond) || draft.releaseRateBytesPerSecond < 0 || draft.releaseRateBytesPerSecond >= draft.triggerRateBytesPerSecond) return '回落速率必须低于触发速率'
  if (!Number.isInteger(draft.triggerDurationSeconds) || draft.triggerDurationSeconds < 2 || draft.triggerDurationSeconds > 300) return '持续时间必须在 2 到 300 秒之间'
  if (!Number.isInteger(draft.releaseDurationSeconds) || draft.releaseDurationSeconds < 5 || draft.releaseDurationSeconds > 3600) return '回落等待必须在 5 到 3600 秒之间'
  if (!Number.isInteger(draft.cooldownSeconds) || draft.cooldownSeconds < 0 || draft.cooldownSeconds > 86400) return '冷却时间必须在 0 到 86400 秒之间'
  if (draft.enabled && !draft.downloadPoolId) return '启用前请选择下载代理池'
  if (draft.enabled && poolsReady && !pool) return '所选下载代理池已不存在'
  if (draft.enabled && pool.availableCount < 2) return '下载代理池至少需要 2 个可用节点'
  return null
}
function progressMetric(policy: TrafficPolicy) {
  if (policy.state === 'active' || policy.state === 'recovering') return { label: '回落进度', value: `${Math.min(policy.releaseProgressSeconds, policy.releaseDurationSeconds)} / ${policy.releaseDurationSeconds} 秒` }
  return { label: '触发进度', value: `${Math.min(policy.triggerProgressSeconds, policy.triggerDurationSeconds)} / ${policy.triggerDurationSeconds} 秒` }
}
function bytesToMiB(value: number) { return Number((value / (1 << 20)).toFixed(2)) }
function mibToBytes(value: number) { return Math.round(value * (1 << 20)) }
function formatRate(value: number) { if (value >= 1 << 20) return `${(value / (1 << 20)).toFixed(1)} MiB/s`; if (value >= 1 << 10) return `${(value / (1 << 10)).toFixed(0)} KiB/s`; return `${value} B/s` }
function formatTime(value: string) { return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(value)) }
function eventTypeLabel(type: string) { return type === 'activated' ? '接管' : type === 'recovered' ? '恢复' : type === 'error' ? '异常' : type === 'cancelled' ? '取消' : '配置' }
