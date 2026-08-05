import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, AlertTriangle, ArrowRightLeft, Clock3, Gauge, Save, Waves } from 'lucide-react'
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

  useEffect(() => {
    if (!policyQuery.data || draft) return
    setDraft(toDraft(policyQuery.data))
  }, [draft, policyQuery.data])

  const saveMutation = useMutation({
    mutationFn: (input: Draft) => updateTrafficPolicy(input),
    onSuccess: async (value) => {
      setDraft(toDraft(value))
      queryClient.setQueryData(['traffic-policy'], value)
    },
  })
  const policy = policyQuery.data
  const pools = poolsQuery.data ?? []
  const selectedPool = pools.find((pool) => pool.id === draft?.downloadPoolId)
  const valid = Boolean(draft)
    && (!draft!.enabled || Boolean(draft!.downloadPoolId && selectedPool && selectedPool.availableCount >= 2))
    && draft!.releaseRateBytesPerSecond < draft!.triggerRateBytesPerSecond

  return (
    <>
      <PageHeading
        eyebrow="TRAFFIC POLICY"
        title="流量策略"
        action={<button className="button button--primary" type="button" disabled={!valid || saveMutation.isPending} onClick={() => draft && saveMutation.mutate(draft)}><Save size={16} />{saveMutation.isPending ? '保存中' : '保存策略'}</button>}
      />
      {(policyQuery.error || poolsQuery.error || saveMutation.error) && <InlineError error={policyQuery.error ?? poolsQuery.error ?? saveMutation.error} />}

      {policy && draft ? (
        <div className="traffic-policy-layout">
          <section className="traffic-status-band" aria-label="流量策略状态">
            <div className={`traffic-state traffic-state--${policy.state}`}><span className="status-dot" /><div><span>当前状态</span><strong>{stateLabels[policy.state]}</strong></div></div>
            <Metric icon={Waves} label="实时下行" value={formatRate(policy.currentDownloadBps)} />
            <Metric icon={Activity} label="活动连接" value={`${policy.activeConnections}`} />
            <Metric icon={Clock3} label="触发进度" value={`${Math.min(policy.triggerProgressSeconds, draft.triggerDurationSeconds)} / ${draft.triggerDurationSeconds} 秒`} />
          </section>

          {policy.lastError && <div className="traffic-policy-alert"><AlertTriangle size={17} /><span>{policy.lastError}</span></div>}

          <section className="traffic-policy-section" aria-labelledby="download-policy-heading">
            <div className="traffic-section-heading"><div><Gauge size={18} /><h2 id="download-policy-heading">下载代理策略</h2></div><label className="settings-switch"><input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })} /><span aria-hidden="true" /><strong>{draft.enabled ? '已启用' : '已停用'}</strong></label></div>
            <div className="traffic-policy-fields">
              <label className="traffic-field traffic-field--wide"><span>下载代理池</span><select value={draft.downloadPoolId} onChange={(event) => setDraft({ ...draft, downloadPoolId: event.target.value })}><option value="">选择节点池</option>{pools.map((pool) => <option value={pool.id} key={pool.id} disabled={pool.availableCount < 2}>{pool.name} · {pool.availableCount}/{pool.memberCount} 可用</option>)}</select></label>
              <NumberField label="触发速率" value={bytesToMiB(draft.triggerRateBytesPerSecond)} suffix="MiB/s" min={0.0625} max={10240} step={0.5} onChange={(value) => setDraft({ ...draft, triggerRateBytesPerSecond: mibToBytes(value) })} />
              <NumberField label="持续时间" value={draft.triggerDurationSeconds} suffix="秒" min={2} max={300} step={1} onChange={(value) => setDraft({ ...draft, triggerDurationSeconds: value })} />
              <NumberField label="回落速率" value={bytesToMiB(draft.releaseRateBytesPerSecond)} suffix="MiB/s" min={0} max={10239} step={0.5} onChange={(value) => setDraft({ ...draft, releaseRateBytesPerSecond: mibToBytes(value) })} />
              <NumberField label="回落等待" value={draft.releaseDurationSeconds} suffix="秒" min={5} max={3600} step={5} onChange={(value) => setDraft({ ...draft, releaseDurationSeconds: value })} />
              <NumberField label="冷却时间" value={draft.cooldownSeconds} suffix="秒" min={0} max={86400} step={30} onChange={(value) => setDraft({ ...draft, cooldownSeconds: value })} />
            </div>
            {draft.enabled && selectedPool && selectedPool.availableCount < 2 && <div className="traffic-field-error">下载代理池至少需要 2 个可用节点</div>}
          </section>

          <section className="traffic-policy-section" aria-labelledby="handover-heading">
            <div className="traffic-section-heading"><div><ArrowRightLeft size={18} /><h2 id="handover-heading">接管状态</h2></div>{policy.activatedAt && <time>{formatTime(policy.activatedAt)}</time>}</div>
            <div className="traffic-handover">
              <div><span>原代理池</span><strong>{policy.originalPoolName || '未接管'}</strong></div><ArrowRightLeft size={18} /><div><span>下载代理池</span><strong>{selectedPool?.name || '未选择'}</strong></div>
            </div>
          </section>

          <section className="traffic-policy-section" aria-labelledby="traffic-events-heading">
            <div className="traffic-section-heading"><div><Clock3 size={18} /><h2 id="traffic-events-heading">近期事件</h2></div><span>{policy.events.length} 条</span></div>
            <div className="traffic-events">{policy.events.map((event) => <div className="traffic-event" key={`${event.timestamp}-${event.type}`}><time>{formatTime(event.timestamp)}</time><span className={`traffic-event-type traffic-event-type--${event.type}`}>{eventTypeLabel(event.type)}</span><strong>{event.message}</strong></div>)}{!policy.events.length && <div className="traffic-events-empty">暂无策略事件</div>}</div>
          </section>
        </div>
      ) : <div className="empty-state">加载流量策略...</div>}
    </>
  )
}

function Metric({ icon: Icon, label, value }: { icon: typeof Activity; label: string; value: string }) { return <div className="traffic-metric"><Icon size={18} /><div><span>{label}</span><strong>{value}</strong></div></div> }
function NumberField({ label, value, suffix, min, max, step, onChange }: { label: string; value: number; suffix: string; min: number; max: number; step: number; onChange: (value: number) => void }) { return <label className="traffic-field"><span>{label}</span><div><input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(Number(event.target.value))} /><em>{suffix}</em></div></label> }
function toDraft(policy: TrafficPolicy): Draft { return { enabled: policy.enabled, downloadPoolId: policy.downloadPoolId, triggerRateBytesPerSecond: policy.triggerRateBytesPerSecond, triggerDurationSeconds: policy.triggerDurationSeconds, releaseRateBytesPerSecond: policy.releaseRateBytesPerSecond, releaseDurationSeconds: policy.releaseDurationSeconds, cooldownSeconds: policy.cooldownSeconds } }
function bytesToMiB(value: number) { return Number((value / (1 << 20)).toFixed(2)) }
function mibToBytes(value: number) { return Math.round(value * (1 << 20)) }
function formatRate(value: number) { if (value >= 1 << 20) return `${(value / (1 << 20)).toFixed(1)} MiB/s`; if (value >= 1 << 10) return `${(value / (1 << 10)).toFixed(0)} KiB/s`; return `${value} B/s` }
function formatTime(value: string) { return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(value)) }
function eventTypeLabel(type: string) { return type === 'activated' ? '接管' : type === 'recovered' ? '恢复' : type === 'error' ? '异常' : type === 'cancelled' ? '取消' : '配置' }
