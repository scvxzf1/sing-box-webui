import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Clipboard, Download, Globe2, LockKeyhole, Network, Plus, Save, Server, Trash2 } from 'lucide-react'
import {
  createProxyChannel,
  deleteProxyChannel,
  getRuntime,
  getSubscription,
  listProxyChannels,
  listSubscriptions,
  updateProxyChannel,
} from '../api/client'
import type { CreateProxyChannel, Node, ProxyChannel } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

type NodeOption = { subscriptionId: string; subscriptionName: string; node: Node }

const protocolLabels: Record<ProxyChannel['protocol'], string> = { socks5: 'SOCKS5', http: 'HTTP', https: 'HTTPS' }
const defaultPorts: Record<ProxyChannel['protocol'], number> = { socks5: 1080, http: 8080, https: 8443 }

export function ChannelsView() {
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState('')
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [protocol, setProtocol] = useState<ProxyChannel['protocol']>('socks5')
  const [direction, setDirection] = useState<ProxyChannel['direction']>('forward')
  const [port, setPort] = useState(1080)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [nodeKeyValue, setNodeKeyValue] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [copiedId, setCopiedId] = useState('')

  const channelsQuery = useQuery({ queryKey: ['channels'], queryFn: ({ signal }) => listProxyChannels(signal) })
  const subscriptionsQuery = useQuery({ queryKey: ['subscriptions'], queryFn: ({ signal }) => listSubscriptions(signal) })
  const nodesQuery = useQuery({
    queryKey: ['channels', 'nodes', subscriptionsQuery.data?.map((item) => item.id).join(',')],
    enabled: Boolean(subscriptionsQuery.data),
    queryFn: async () => {
      const details = await Promise.all((subscriptionsQuery.data ?? []).map((item) => getSubscription(item.id)))
      return details.flatMap((item) => (item.nodes ?? []).map((node) => ({ subscriptionId: item.id, subscriptionName: item.name, node })))
    },
  })
  const runtimeQuery = useQuery({ queryKey: ['runtime'], queryFn: ({ signal }) => getRuntime(signal), refetchInterval: 3000 })
  const channels = useMemo(() => channelsQuery.data ?? [], [channelsQuery.data])
  const nodeOptions = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data])
  const selected = channels.find((item) => item.id === selectedId)

  useEffect(() => {
    if (!creating && !selectedId && channels.length) setSelectedId(channels[0].id)
    if (!creating && selectedId && !channels.some((item) => item.id === selectedId)) setSelectedId(channels[0]?.id ?? '')
  }, [channels, creating, selectedId])

  useEffect(() => {
    if (creating || !selected) return
    setName(selected.name)
    setProtocol(selected.protocol)
    setDirection(selected.direction)
    setPort(selected.port)
    setUsername(selected.username ?? '')
    setPassword(selected.password ?? '')
    setNodeKeyValue(nodeKey(selected.node.subscriptionId, selected.node.nodeId))
    setEnabled(selected.enabled)
  }, [creating, selected])

  useEffect(() => {
    if (!nodeOptions.length || nodeOptions.some((option) => optionKey(option) === nodeKeyValue)) return
    setNodeKeyValue(optionKey(nodeOptions[0]))
  }, [nodeKeyValue, nodeOptions])

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ['channels'] })
    await queryClient.invalidateQueries({ queryKey: ['runtime'] })
  }
  const saveMutation = useMutation({
    mutationFn: async () => {
      const node = parseNodeKey(nodeKeyValue)
      if (!node) throw new Error('请选择上游节点')
      const input: CreateProxyChannel = { name: name.trim(), protocol, direction, port, username: username.trim(), password, node, enabled }
      return creating ? createProxyChannel(input) : updateProxyChannel(selectedId, input)
    },
    onSuccess: async (channel) => {
      setCreating(false)
      setSelectedId(channel.id)
      await invalidate()
    },
  })
  const deleteMutation = useMutation({
    mutationFn: deleteProxyChannel,
    onSuccess: async () => {
      setSelectedId('')
      await invalidate()
    },
  })

  const resetForCreate = () => {
    setCreating(true)
    setSelectedId('')
    setName('')
    setProtocol('socks5')
    setDirection('forward')
    setPort(defaultPorts.socks5)
    setUsername('')
    setPassword('')
    setNodeKeyValue(nodeOptions[0] ? optionKey(nodeOptions[0]) : '')
    setEnabled(true)
  }
  const changeProtocol = (value: ProxyChannel['protocol']) => {
    setProtocol(value)
    if (creating) setPort(defaultPorts[value])
  }
  const credentialsValid = (username === '' && password === '') || (username.trim() !== '' && password !== '')
  const canSave = Boolean(name.trim() && nodeKeyValue && port >= 1024 && port <= 65535 && credentialsValid && (direction === 'forward' || (username.trim() && password)) && !saveMutation.isPending)
  const runtimeRunning = runtimeQuery.data?.state === 'running'
  const error = channelsQuery.error ?? subscriptionsQuery.error ?? nodesQuery.error ?? runtimeQuery.error ?? saveMutation.error ?? deleteMutation.error

  const copyAddress = async (channel: ProxyChannel, address: string) => {
    const copyKey = channel.id + ':' + address
    await navigator.clipboard.writeText(channelURI(channel, address))
    setCopiedId(copyKey)
    window.setTimeout(() => setCopiedId((value) => value === copyKey ? '' : value), 1500)
  }

  return (
    <>
      <PageHeading eyebrow="APPLICATION PROXY ENDPOINTS" title="代理通道" action={<button className="button" type="button" onClick={resetForCreate}><Plus size={16} />新建通道</button>} />
      {error && <InlineError error={error} />}
      <section className="channel-status-band">
        <div><span className={'status-dot ' + (runtimeRunning ? 'status-dot--ok' : 'status-dot--warn')} /><span>运行时</span><strong>{runtimeRunning ? '代理运行中' : '代理已停止'}</strong></div>
        <div><Network size={17} /><span>已启用</span><strong>{channels.filter((item) => item.enabled).length}</strong></div>
        <div><Globe2 size={17} /><span>共享入口</span><strong>{channels.filter((item) => item.direction === 'reverse' && item.enabled).length}</strong></div>
        <a className="button button--ghost button--sm" href="/api/v1/channels/certificate" download><Download size={15} />HTTPS 证书</a>
      </section>

      <section className="channels-layout">
        <aside className="channel-list panel" aria-label="代理通道列表">
          {channels.map((channel) => {
            const active = channel.enabled && channel.available && runtimeRunning
            return (
              <button className={'channel-row ' + (channel.id === selectedId && !creating ? 'channel-row--selected' : '')} type="button" key={channel.id} onClick={() => { setCreating(false); setSelectedId(channel.id) }}>
                <span className={'status-dot ' + (active ? 'status-dot--ok' : channel.enabled ? 'status-dot--warn' : '')} />
                <span><strong>{channel.name}</strong><small>{protocolLabels[channel.protocol]} · {channel.accessAddresses[0] ?? channel.listenAddress}</small></span>
                <em>{channel.direction === 'forward' ? '正向' : '共享'}</em>
              </button>
            )
          })}
          {!channelsQuery.isPending && !channels.length && <div className="empty-state">尚未创建代理通道</div>}
        </aside>

        <div className="channel-editor panel">
          {(creating || selected) ? (
            <>
              <div className="channel-editor-header">
                <label><span>名称</span><input value={name} maxLength={80} onChange={(event) => setName(event.target.value)} placeholder="例如：浏览器 SOCKS" /></label>
                <div className="channel-editor-actions">
                  {!creating && <button className="icon-button icon-button--danger" type="button" title="删除通道" aria-label="删除通道" disabled={deleteMutation.isPending} onClick={() => selected && window.confirm('删除代理通道“' + selected.name + '”？') && deleteMutation.mutate(selected.id)}><Trash2 size={16} /></button>}
                  <button className="button button--primary" type="button" disabled={!canSave} onClick={() => saveMutation.mutate()}><Save size={16} />{saveMutation.isPending ? '保存中' : '保存'}</button>
                </div>
              </div>

              <div className="channel-editor-grid">
                <fieldset className="channel-field channel-field--wide"><legend>协议</legend><div className="segmented-control">{(['socks5', 'http', 'https'] as const).map((value) => <button className={protocol === value ? 'segmented-control--active' : ''} type="button" key={value} onClick={() => changeProtocol(value)}>{value === 'socks5' ? <Network size={16} /> : value === 'https' ? <LockKeyhole size={16} /> : <Globe2 size={16} />}{protocolLabels[value]}</button>)}</div></fieldset>
                <fieldset className="channel-field channel-field--wide"><legend>方向</legend><div className="segmented-control"><button className={direction === 'forward' ? 'segmented-control--active' : ''} type="button" onClick={() => setDirection('forward')}><Server size={16} />正向代理</button><button className={direction === 'reverse' ? 'segmented-control--active' : ''} type="button" onClick={() => setDirection('reverse')}><Globe2 size={16} />反向/共享</button></div></fieldset>
                <label className="channel-field channel-field--wide"><span>上游节点</span><select aria-label="选择通道节点" value={nodeKeyValue} onChange={(event) => setNodeKeyValue(event.target.value)}>{groupNodes(nodeOptions).map(([group, options]) => <optgroup label={group} key={group}>{options.map((option) => <option value={optionKey(option)} key={optionKey(option)}>{option.node.name} · {option.node.type}</option>)}</optgroup>)}</select></label>
                <label className="channel-field"><span>监听端口</span><input type="number" min={1024} max={65535} value={port} onChange={(event) => setPort(Number(event.target.value))} /></label>
                <label className="channel-field"><span>用户名</span><input value={username} maxLength={128} autoComplete="off" onChange={(event) => setUsername(event.target.value)} /></label>
                <label className="channel-field"><span>密码</span><input type="password" value={password} maxLength={256} autoComplete="new-password" onChange={(event) => setPassword(event.target.value)} /></label>
                <label className="channel-enable"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span aria-hidden="true" /><strong>{enabled ? '已启用' : '已停用'}</strong></label>
              </div>

              {!credentialsValid && <div className="channel-validation channel-validation--error">用户名与密码必须同时填写</div>}
              {direction === 'reverse' && (!username.trim() || !password) && <div className="channel-validation channel-validation--error">反向/共享入口必须启用认证</div>}
              {selected && !selected.available && <div className="channel-validation channel-validation--error">{selected.unavailableReason}</div>}
              {selected && (
                <div className="channel-endpoint-list">
                  {(selected.accessAddresses.length ? selected.accessAddresses : [selected.listenAddress]).map((address) => {
                    const copyKey = selected.id + ':' + address
                    return <div className="channel-endpoint" key={address}>
                      <div><span>{selected.direction === 'reverse' ? '局域网访问地址' : selected.enabled && runtimeRunning ? '本机可用地址' : '本机配置地址'}</span><strong>{channelURI(selected, address)}</strong><small>{selected.nodeName || '节点不可用'}</small></div>
                      <button className="icon-button" type="button" title="复制连接地址" aria-label={'复制连接地址 ' + address} onClick={() => void copyAddress(selected, address)}>{copiedId === copyKey ? <Check size={16} /> : <Clipboard size={16} />}</button>
                    </div>
                  })}
                </div>
              )}
            </>
          ) : <div className="empty-state">选择一条通道，或新建代理通道</div>}
        </div>
      </section>
    </>
  )
}

function nodeKey(subscriptionId: string, nodeId: string) { return subscriptionId + '::' + nodeId }
function optionKey(option: NodeOption) { return nodeKey(option.subscriptionId, option.node.id) }
function parseNodeKey(value: string) {
  const separator = value.indexOf('::')
  if (separator < 1) return null
  return { subscriptionId: value.slice(0, separator), nodeId: value.slice(separator + 2) }
}
function groupNodes(options: NodeOption[]) {
  const groups = new Map<string, NodeOption[]>()
  for (const option of options) groups.set(option.subscriptionName, [...(groups.get(option.subscriptionName) ?? []), option])
  return [...groups.entries()]
}
function channelURI(channel: ProxyChannel, address: string) {
  const credentials = channel.username && channel.password ? encodeURIComponent(channel.username) + ':' + encodeURIComponent(channel.password) + '@' : ''
  return channel.protocol + '://' + credentials + address
}
