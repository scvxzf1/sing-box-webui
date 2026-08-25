import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowRight, Gauge, GitBranch, Layers3, ListTree, LoaderCircle, Plus, Save, Search, Trash2 } from 'lucide-react'
import {
  createProxyChain,
  deleteProxyChain,
  getSubscription,
  listNodePools,
  listProxyChains,
  listSubscriptions,
  testProxyChainLatency,
  updateProxyChain,
} from '../api/client'
import type { CreateProxyChain, LatencyResult, Node, ProxyChain } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

type EntryType = ProxyChain['entryType']
type NodeOption = { subscriptionId: string; subscriptionName: string; node: Node }

export function ChainsView() {
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState('')
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [entryType, setEntryType] = useState<EntryType>('node')
  const [entryNodeKey, setEntryNodeKey] = useState('')
  const [entryPoolId, setEntryPoolId] = useState('')
  const [exitNodeKey, setExitNodeKey] = useState('')
  const [latencyResults, setLatencyResults] = useState<LatencyResult[]>([])

  const chainsQuery = useQuery({ queryKey: ['chains'], queryFn: ({ signal }) => listProxyChains(signal) })
  const subscriptionsQuery = useQuery({ queryKey: ['subscriptions'], queryFn: ({ signal }) => listSubscriptions(signal) })
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: ({ signal }) => listNodePools(signal) })
  const nodesQuery = useQuery({
    queryKey: ['chains', 'nodes', subscriptionsQuery.data?.map((item) => item.id).join(',')],
    enabled: Boolean(subscriptionsQuery.data),
    queryFn: async () => {
      const details = await Promise.all((subscriptionsQuery.data ?? []).map((item) => getSubscription(item.id)))
      return details.flatMap((item) => (item.nodes ?? []).map((node) => ({ subscriptionId: item.id, subscriptionName: item.name, node })))
    },
  })
  const chains = useMemo(() => chainsQuery.data ?? [], [chainsQuery.data])
  const nodeOptions = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data])
  const selected = chains.find((item) => item.id === selectedId)

  useEffect(() => {
    if (!creating && !selectedId && chains.length) setSelectedId(chains[0].id)
    if (!creating && selectedId && !chains.some((item) => item.id === selectedId)) setSelectedId(chains[0]?.id ?? '')
  }, [chains, creating, selectedId])

  useEffect(() => {
    if (creating || !selected) return
    setName(selected.name)
    setEntryType(selected.entryType)
    setEntryNodeKey(selected.entryNode ? nodeKey(selected.entryNode.subscriptionId, selected.entryNode.nodeId) : '')
    setEntryPoolId(selected.entryPoolId ?? '')
    setExitNodeKey(nodeKey(selected.exitNode.subscriptionId, selected.exitNode.nodeId))
    setLatencyResults([])
  }, [creating, selected])

  useEffect(() => {
    if (!nodeOptions.length) return
    const effectiveEntryKey = nodeOptions.some((option) => optionKey(option) === entryNodeKey) ? entryNodeKey : optionKey(nodeOptions[0])
    if (effectiveEntryKey !== entryNodeKey) setEntryNodeKey(effectiveEntryKey)
    const defaultExit = nodeOptions.find((option) => optionKey(option) !== effectiveEntryKey)
    if (!nodeOptions.some((option) => optionKey(option) === exitNodeKey) || exitNodeKey === effectiveEntryKey) {
      setExitNodeKey(defaultExit ? optionKey(defaultExit) : '')
    }
  }, [entryNodeKey, exitNodeKey, nodeOptions])

  useEffect(() => {
    const pools = poolsQuery.data ?? []
    if (!pools.some((pool) => pool.id === entryPoolId)) setEntryPoolId(pools[0]?.id ?? '')
  }, [entryPoolId, poolsQuery.data])

  const selectedEntryPool = poolsQuery.data?.find((pool) => pool.id === entryPoolId)
  const exitRef = parseNodeKey(exitNodeKey)
  const poolContainsExit = Boolean(selectedEntryPool?.members.some((member) => member.subscriptionId === exitRef?.subscriptionId && member.nodeId === exitRef?.nodeId))
  const sameNode = entryType === 'node' && entryNodeKey !== '' && entryNodeKey === exitNodeKey

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ['chains'] })
    await queryClient.invalidateQueries({ queryKey: ['runtime'] })
  }
  const saveMutation = useMutation({
    mutationFn: async () => {
      const exitNode = parseNodeKey(exitNodeKey)
      if (!exitNode) throw new Error('请选择出口节点')
      const input: CreateProxyChain = {
        name: name.trim(), entryType, exitNode,
        ...(entryType === 'node' ? { entryNode: parseNodeKey(entryNodeKey)! } : { entryPoolId }),
      }
      return creating ? createProxyChain(input) : updateProxyChain(selectedId, input)
    },
    onSuccess: async (chain) => {
      setCreating(false)
      setSelectedId(chain.id)
      await invalidate()
    },
  })
  const deleteMutation = useMutation({
    mutationFn: deleteProxyChain,
    onSuccess: async () => {
      setSelectedId('')
      await invalidate()
    },
  })
  const latencyMutation = useMutation({
    mutationFn: testProxyChainLatency,
    onSuccess: (response) => setLatencyResults(response.items),
  })

  const resetForCreate = () => {
    setCreating(true)
    setSelectedId('')
    setName('')
    setEntryType('node')
    setEntryNodeKey(nodeOptions[0] ? optionKey(nodeOptions[0]) : '')
    setExitNodeKey(nodeOptions[1] ? optionKey(nodeOptions[1]) : '')
    setEntryPoolId(poolsQuery.data?.[0]?.id ?? '')
    setLatencyResults([])
  }
  const entryLabel = entryType === 'node'
    ? nodeOptions.find((option) => optionKey(option) === entryNodeKey)?.node.name
    : selectedEntryPool?.name
  const exitLabel = nodeOptions.find((option) => optionKey(option) === exitNodeKey)?.node.name
  const canSave = Boolean(name.trim() && exitRef && (entryType === 'node' ? entryNodeKey && !sameNode : entryPoolId && !poolContainsExit) && !saveMutation.isPending)
  const passedResults = latencyResults.filter((item) => item.status === 'ok' && item.latencyMs !== undefined)
  const averageLatency = passedResults.length ? Math.round(passedResults.reduce((sum, item) => sum + (item.latencyMs ?? 0), 0) / passedResults.length) : undefined
  const error = chainsQuery.error ?? subscriptionsQuery.error ?? poolsQuery.error ?? nodesQuery.error ?? saveMutation.error ?? deleteMutation.error ?? latencyMutation.error

  return (
    <>
      <PageHeading eyebrow="MULTI-HOP ROUTES" title="链式代理" action={<button className="button" type="button" onClick={resetForCreate}><Plus size={16} />新建链路</button>} />
      {error && <InlineError error={error} />}
      <section className="chains-layout">
        <aside className="chain-list panel" aria-label="链式代理列表">
          {chains.map((chain) => (
            <button className={`chain-row ${chain.id === selectedId && !creating ? 'chain-row--selected' : ''}`} type="button" key={chain.id} onClick={() => { setCreating(false); setSelectedId(chain.id) }}>
              <span className={`status-dot ${chain.available ? 'status-dot--ok' : 'status-dot--warn'}`} />
              <span><strong>{chain.name}</strong><small>{chain.entryName || '入口失效'} → {chain.exitName || '出口失效'}</small></span>
              <GitBranch size={16} aria-hidden="true" />
            </button>
          ))}
          {!chainsQuery.isPending && !chains.length && <div className="empty-state">尚未创建链式代理</div>}
        </aside>

        <div className="chain-editor panel">
          {(creating || selected) ? (
            <>
              <div className="chain-editor-header">
                <label><span>名称</span><input value={name} maxLength={80} onChange={(event) => setName(event.target.value)} placeholder="例如：香港中转到日本" /></label>
                <div className="chain-editor-actions">
                  {!creating && selected?.available && <button className="button" type="button" disabled={latencyMutation.isPending} onClick={() => latencyMutation.mutate(selected.id)}>{latencyMutation.isPending ? <LoaderCircle className="spin" size={16} /> : <Gauge size={16} />}{latencyMutation.isPending ? '测试中' : '测试链路'}</button>}
                  {!creating && <button className="icon-button icon-button--danger" type="button" title="删除链路" aria-label="删除链路" disabled={deleteMutation.isPending} onClick={() => selected && window.confirm(`删除链式代理“${selected.name}”？`) && deleteMutation.mutate(selected.id)}><Trash2 size={16} /></button>}
                  <button className="button button--primary" type="button" disabled={!canSave} onClick={() => saveMutation.mutate()}><Save size={16} />{saveMutation.isPending ? '保存中' : '保存'}</button>
                </div>
              </div>

              <div className="chain-type-control segmented-control" role="group" aria-label="入口目标类型">
                <button className={entryType === 'node' ? 'segmented-control--active' : ''} type="button" onClick={() => setEntryType('node')}><ListTree size={16} />节点对节点</button>
                <button className={entryType === 'pool' ? 'segmented-control--active' : ''} type="button" onClick={() => setEntryType('pool')}><Layers3 size={16} />节点池对节点</button>
              </div>

              <div className="chain-path-editor">
                <ChainTarget label="入口" icon={entryType === 'node' ? <ListTree size={18} /> : <Layers3 size={18} />}>
                  {entryType === 'node' ? (
                    <NodeSelect ariaLabel="选择入口节点" options={nodeOptions} value={entryNodeKey} onChange={setEntryNodeKey} />
                  ) : (
                    <select aria-label="选择入口节点池" value={entryPoolId} onChange={(event) => setEntryPoolId(event.target.value)}>
                      {(poolsQuery.data ?? []).map((pool) => <option value={pool.id} key={pool.id}>{pool.name} · {pool.availableCount}/{pool.memberCount} 可用</option>)}
                    </select>
                  )}
                </ChainTarget>
                <ArrowRight className="chain-path-arrow" size={22} aria-label="经由" />
                <ChainTarget label="出口" icon={<ListTree size={18} />}>
                  <NodeSelect ariaLabel="选择出口节点" options={nodeOptions} value={exitNodeKey} onChange={setExitNodeKey} />
                </ChainTarget>
              </div>

              <div className={`chain-validation ${sameNode || poolContainsExit || selected?.unavailableReason ? 'chain-validation--error' : ''}`}>
                {(sameNode || poolContainsExit || selected?.unavailableReason) && <AlertTriangle size={16} />}
                <span>{sameNode ? '入口与出口必须是不同节点' : poolContainsExit ? '入口节点池不能包含出口节点' : selected?.unavailableReason ?? `${entryLabel || '未选择入口'} → ${exitLabel || '未选择出口'}`}</span>
              </div>
              {latencyResults.length > 0 && (
                <div className="chain-test-results">
                  <div><strong>{passedResults.length}/{latencyResults.length} 条路径通过</strong><span>{averageLatency !== undefined ? `平均 ${averageLatency} ms` : '链路不可用'}</span></div>
                  <div>{latencyResults.map((item) => <span className={`latency-result latency-result--${item.status}`} title={item.detail} key={item.nodeId}>{(item.path?.length ? item.path.join(' → ') : item.name)} · {item.status === 'ok' ? `${item.latencyMs} ms` : item.status === 'timeout' ? '超时' : '失败'}</span>)}</div>
                </div>
              )}
            </>
          ) : <div className="empty-state">选择一条链路，或新建链式代理</div>}
        </div>
      </section>
    </>
  )
}

function ChainTarget({ label, icon, children }: { label: string; icon: ReactNode; children: ReactNode }) {
  return <div className="chain-target"><div><span>{icon}</span><strong>{label}</strong></div>{children}</div>
}

function NodeSelect({ ariaLabel, options, value, onChange }: { ariaLabel: string; options: NodeOption[]; value: string; onChange: (value: string) => void }) {
  const [query, setQuery] = useState('')
  const groups = useMemo(() => {
    const result = new Map<string, NodeOption[]>()
    for (const option of options) result.set(option.subscriptionName, [...(result.get(option.subscriptionName) ?? []), option])
    return [...result.entries()]
  }, [options])
  const filteredGroups = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase()
    if (!normalizedQuery) return groups
    return groups
      .map(([name, items]) => [name, items.filter((option) => searchableNodeText(option).includes(normalizedQuery))] as const)
      .filter(([, items]) => items.length > 0)
  }, [groups, query])
  const visibleOptions = filteredGroups.flatMap(([, items]) => items)
  const selectedOption = options.find((option) => optionKey(option) === value)
  const selectedFilteredOut = Boolean(selectedOption && !visibleOptions.some((option) => optionKey(option) === value))

  useEffect(() => {
    setQuery('')
  }, [value])

  const handleChange = (nextValue: string) => {
    setQuery('')
    onChange(nextValue)
  }

  return (
    <div className="node-select">
      <label className="node-select-search">
        <Search size={14} aria-hidden="true" />
        <span className="sr-only">{ariaLabel}搜索</span>
        <input
          type="search"
          aria-label={`${ariaLabel}搜索`}
          placeholder="搜索节点"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </label>
      <select aria-label={ariaLabel} value={value} onChange={(event) => handleChange(event.target.value)}>
        {selectedFilteredOut && selectedOption && <option value={value} hidden>{nodeOptionLabel(selectedOption)}</option>}
        {filteredGroups.length > 0 ? (
          filteredGroups.map(([name, items]) => <optgroup label={name} key={name}>{items.map((option) => <option value={optionKey(option)} key={optionKey(option)}>{nodeOptionLabel(option)}</option>)}</optgroup>)
        ) : (
          <option value="">没有匹配的节点</option>
        )}
      </select>
    </div>
  )
}

function nodeOptionLabel(option: NodeOption) {
  return `${option.node.name} · ${option.node.type}`
}

function searchableNodeText(option: NodeOption) {
  return [option.node.name, option.node.id, option.node.type, option.subscriptionName, option.subscriptionId, option.node.server]
    .filter(Boolean)
    .join(' ')
    .toLocaleLowerCase()
}

function optionKey(option: NodeOption) {
  return nodeKey(option.subscriptionId, option.node.id)
}

function nodeKey(subscriptionId: string, nodeId: string) {
  return `${subscriptionId}\u0000${nodeId}`
}

function parseNodeKey(value: string) {
  const [subscriptionId, nodeId] = value.split('\u0000')
  return subscriptionId && nodeId ? { subscriptionId, nodeId } : null
}
