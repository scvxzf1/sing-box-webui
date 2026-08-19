import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Globe, Plus, RotateCcw, Save, Trash2 } from 'lucide-react'
import { getDnsProfile, getRuntime, updateDnsProfile } from '../api/client'
import type { DnsProfile, DnsServer, UpdateDnsProfile } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

type Draft = UpdateDnsProfile

const serverTypes: Array<{ value: DnsServer['type']; label: string }> = [
  { value: 'udp', label: 'UDP' },
  { value: 'tcp', label: 'TCP' },
  { value: 'tls', label: 'DoT' },
  { value: 'https', label: 'DoH' },
  { value: 'quic', label: 'DoQ' },
  { value: 'h3', label: 'DoH3' },
  { value: 'local', label: '系统' },
  { value: 'hosts', label: 'Hosts' },
]

const strategies: Array<{ value: DnsProfile['strategy']; label: string }> = [
  { value: 'prefer_ipv4', label: '优先 IPv4' },
  { value: 'prefer_ipv6', label: '优先 IPv6' },
  { value: 'ipv4_only', label: '仅 IPv4' },
  { value: 'ipv6_only', label: '仅 IPv6' },
]

const defaultProfile: DnsProfile = {
  servers: [{ tag: 'dns-google', type: 'udp', server: '8.8.8.8' }],
  final: 'dns-google',
  strategy: 'prefer_ipv4',
  fakeIP: { enabled: false },
}

export function DnsView() {
  const queryClient = useQueryClient()
  const profileQuery = useQuery({ queryKey: ['dns-profile'], queryFn: ({ signal }) => getDnsProfile(signal) })
  const runtimeQuery = useQuery({ queryKey: ['runtime'], queryFn: ({ signal }) => getRuntime(signal) })
  const [draft, setDraft] = useState<Draft | null>(null)

  useEffect(() => {
    if (!profileQuery.data || draft) return
    setDraft(toDraft(profileQuery.data))
  }, [draft, profileQuery.data])

  const saveMutation = useMutation({
    mutationFn: (input: Draft) => updateDnsProfile(input),
    onSuccess: (value) => {
      setDraft(toDraft(value))
      queryClient.setQueryData(['dns-profile'], value)
    },
  })

  const tunUnavailable = !runtimeQuery.data?.capabilities.tun.available
  const validation = draft ? validateDraft(draft) : null
  const dirty = Boolean(draft && profileQuery.data && JSON.stringify(draft) !== JSON.stringify(toDraft(profileQuery.data)))

  return (
    <>
      <PageHeading
        eyebrow="DNS"
        title="DNS"
        action={(
          <div className="page-heading-actions">
            <button className="button button--ghost" type="button" disabled={!draft || saveMutation.isPending} onClick={() => setDraft(toDraft(defaultProfile))}>
              <RotateCcw size={16} />恢复默认
            </button>
            <button className="button button--primary" type="button" disabled={!draft || !validation?.ok || !dirty || saveMutation.isPending} onClick={() => draft && saveMutation.mutate(draft)}>
              <Save size={16} />{saveMutation.isPending ? '保存中' : '保存配置'}
            </button>
          </div>
        )}
      />
      {(profileQuery.error || runtimeQuery.error || saveMutation.error) && <InlineError error={profileQuery.error ?? runtimeQuery.error ?? saveMutation.error} />}
      {runtimeQuery.data && tunUnavailable && (
        <div className="dns-notice" role="note">
          DNS 配置仅在 TUN 代理模式下生效{runtimeQuery.data?.capabilities.tun.detail ? `：${runtimeQuery.data.capabilities.tun.detail}` : '。'}
        </div>
      )}

      {draft ? (
        <div className="dns-layout">
          <section className="traffic-policy-section" aria-labelledby="dns-servers-heading">
            <div className="traffic-section-heading">
              <div><Globe size={18} /><h2 id="dns-servers-heading">上游服务器</h2></div>
              <button
                className="button button--ghost button--sm"
                type="button"
                disabled={draft.servers.length >= 8}
                onClick={() => setDraft({ ...draft, servers: [...draft.servers, { tag: nextTag(draft.servers), type: 'udp', server: '' }] })}
              >
                <Plus size={14} />添加服务器
              </button>
            </div>
            <div className="dns-servers">
              {draft.servers.map((server, index) => {
                const needsAddress = server.type !== 'local' && server.type !== 'hosts'
                return (
                  <div className="dns-server-row" key={index}>
                    <input aria-label={`服务器 ${index + 1} 标签`} placeholder="tag" value={server.tag} onChange={(event) => updateServer(draft, setDraft, index, { tag: event.target.value })} />
                    <select aria-label={`服务器 ${index + 1} 类型`} value={server.type} onChange={(event) => updateServer(draft, setDraft, index, { type: event.target.value as DnsServer['type'] })}>
                      {serverTypes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                    </select>
                    <input
                      aria-label={`服务器 ${index + 1} 地址`}
                      placeholder={needsAddress ? 'IP 或域名' : '无需地址'}
                      value={server.server ?? ''}
                      disabled={!needsAddress}
                      onChange={(event) => updateServer(draft, setDraft, index, { server: event.target.value })}
                    />
                    <input
                      aria-label={`服务器 ${index + 1} 端口`}
                      placeholder="默认端口"
                      type="number"
                      min={1}
                      max={65535}
                      value={server.port ?? ''}
                      disabled={!needsAddress}
                      onChange={(event) => updateServer(draft, setDraft, index, { port: event.target.value === '' ? undefined : Number(event.target.value) })}
                    />
                    <button
                      className="icon-button icon-button--danger"
                      type="button"
                      aria-label={`删除服务器 ${index + 1}`}
                      disabled={draft.servers.length <= 1}
                      onClick={() => removeServer(draft, setDraft, index)}
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                )
              })}
            </div>
          </section>

          <section className="traffic-policy-section" aria-labelledby="dns-resolution-heading">
            <div className="traffic-section-heading"><div><Globe size={18} /><h2 id="dns-resolution-heading">解析策略</h2></div></div>
            <div className="traffic-policy-fields">
              <label className="traffic-field">
                <span>默认服务器</span>
                <select value={draft.final} onChange={(event) => setDraft({ ...draft, final: event.target.value })}>
                  <option value="" disabled={draft.servers.length > 1}>自动</option>
                  {draft.servers.map((server) => <option key={server.tag} value={server.tag}>{server.tag}</option>)}
                </select>
              </label>
              <label className="traffic-field">
                <span>地址族策略</span>
                <select value={draft.strategy} onChange={(event) => setDraft({ ...draft, strategy: event.target.value as DnsProfile['strategy'] })}>
                  {strategies.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                </select>
              </label>
            </div>
          </section>

          <section className="traffic-policy-section" aria-labelledby="dns-fakeip-heading">
            <div className="traffic-section-heading">
              <div><Globe size={18} /><h2 id="dns-fakeip-heading">FakeIP</h2></div>
              <label className="settings-switch">
                <input type="checkbox" checked={draft.fakeIP.enabled} onChange={(event) => setDraft({ ...draft, fakeIP: { ...draft.fakeIP, enabled: event.target.checked } })} />
                <span aria-hidden="true" />
                <strong>{draft.fakeIP.enabled ? '已启用' : '已停用'}</strong>
              </label>
            </div>
            {draft.fakeIP.enabled ? (
              <div className="traffic-policy-fields">
                <label className="traffic-field">
                  <span>IPv4 地址段</span>
                  <input value={draft.fakeIP.inet4Range ?? ''} placeholder="198.18.0.0/15" onChange={(event) => setDraft({ ...draft, fakeIP: { ...draft.fakeIP, inet4Range: event.target.value } })} />
                </label>
                <label className="traffic-field">
                  <span>IPv6 地址段</span>
                  <input value={draft.fakeIP.inet6Range ?? ''} placeholder="fc00::/18" onChange={(event) => setDraft({ ...draft, fakeIP: { ...draft.fakeIP, inet6Range: event.target.value } })} />
                </label>
              </div>
            ) : (
              <p className="dns-section-note">启用后 DNS 直接返回虚拟地址，避免 DNS 污染并减少代理建连延迟；适合网页浏览场景。</p>
            )}
          </section>

          {validation && !validation.ok && <div className="traffic-field-error">{validation.message}</div>}
        </div>
      ) : <div className="empty-state">加载 DNS 配置...</div>}
    </>
  )
}

function toDraft(profile: DnsProfile): Draft {
  return {
    servers: profile.servers.map((server) => ({ ...server })),
    final: profile.final,
    strategy: profile.strategy,
    fakeIP: { ...profile.fakeIP },
  }
}

function updateServer(draft: Draft, setDraft: (draft: Draft) => void, index: number, patch: Partial<DnsServer>) {
  const servers = draft.servers.map((server, item) => (item === index ? { ...server, ...patch } : server))
  const next: Draft = { ...draft, servers }
  if (patch.tag !== undefined && draft.final === draft.servers[index].tag) {
    next.final = patch.tag
  }
  setDraft(next)
}

function removeServer(draft: Draft, setDraft: (draft: Draft) => void, index: number) {
  const removed = draft.servers[index]
  const servers = draft.servers.filter((_, item) => item !== index)
  const next: Draft = { ...draft, servers }
  if (draft.final === removed.tag) {
    next.final = servers.length === 1 ? servers[0].tag : ''
  }
  setDraft(next)
}

function nextTag(servers: DnsServer[]): string {
  const used = new Set(servers.map((server) => server.tag))
  for (let index = servers.length + 1; ; index += 1) {
    const candidate = `dns-${index}`
    if (!used.has(candidate)) return candidate
  }
}

function validateDraft(draft: Draft): { ok: boolean; message?: string } {
  const tags = new Set<string>()
  for (const server of draft.servers) {
    if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(server.tag)) {
      return { ok: false, message: `标签「${server.tag}」须为 1-32 位小写字母、数字、- 或 _` }
    }
    if (tags.has(server.tag)) {
      return { ok: false, message: `标签「${server.tag}」重复` }
    }
    tags.add(server.tag)
    const needsAddress = server.type !== 'local' && server.type !== 'hosts'
    if (needsAddress && !(server.server ?? '').trim()) {
      return { ok: false, message: `服务器「${server.tag}」缺少地址` }
    }
    if (!needsAddress && (server.server ?? '').trim()) {
      return { ok: false, message: `「${server.tag}」为${serverTypes.find((item) => item.value === server.type)?.label ?? server.type}类型，不应填写地址` }
    }
    if (server.port !== undefined && (server.port < 1 || server.port > 65535 || !Number.isInteger(server.port))) {
      return { ok: false, message: `服务器「${server.tag}」端口须在 1-65535 之间` }
    }
  }
  const finalValid = !draft.final || tags.has(draft.final)
  if (!finalValid) {
    return { ok: false, message: `默认服务器「${draft.final}」不存在` }
  }
  if (!draft.final && draft.servers.length > 1) {
    return { ok: false, message: '配置了多台服务器时必须选择默认服务器' }
  }
  if (draft.fakeIP.enabled) {
    const inet4 = (draft.fakeIP.inet4Range ?? '').trim() || '198.18.0.0/15'
    const inet6 = (draft.fakeIP.inet6Range ?? '').trim() || 'fc00::/18'
    if (!/^\d+\.\d+\.\d+\.\d+\/\d+$/.test(inet4)) {
      return { ok: false, message: 'FakeIP IPv4 地址段须为 CIDR 格式' }
    }
    if (!/^[0-9a-fA-F:]+\/\d+$/.test(inet6)) {
      return { ok: false, message: 'FakeIP IPv6 地址段须为 CIDR 格式' }
    }
    if (draft.strategy !== 'ipv4_only' && draft.strategy !== 'prefer_ipv4') {
      return { ok: false, message: 'FakeIP 需要优先 IPv4 的地址族策略' }
    }
  }
  return { ok: true }
}
