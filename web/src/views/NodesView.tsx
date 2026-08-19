import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createPortal } from 'react-dom'
import { Check, ChevronLeft, ChevronRight, FolderPlus, Gauge, LoaderCircle, LockKeyhole, Search, X } from 'lucide-react'
import { getSubscription, listNodePools, listSubscriptions, selectNode, testNodeLatency, updateNodePool } from '../api/client'
import type { LatencyResult, Node, NodePool } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

type GridColumns = 1 | 2 | 3 | 4
type PageSize = 48 | 72 | 96 | 144

const gridColumnsStorageKey = 'sing-box-webui:nodes-grid-columns'
const pageSizeStorageKey = 'sing-box-webui:nodes-page-size'
const maxConcurrentManualTests = 4
const pageSizeOptions: PageSize[] = [48, 72, 96, 144]

export function NodesView() {
  const queryClient = useQueryClient()
  const [subscriptionId, setSubscriptionId] = useState('')
  const [search, setSearch] = useState('')
  const [gridColumns, setGridColumns] = useState<GridColumns>(readGridColumns)
  const [pageSize, setPageSize] = useState<PageSize>(readPageSize)
  const [page, setPage] = useState(0)
  const [latencyResults, setLatencyResults] = useState<Record<string, LatencyResult>>({})
  const [testingIDs, setTestingIDs] = useState<Set<string>>(new Set())
  const [latencyError, setLatencyError] = useState<unknown>(null)
  const [selectedNodeIDs, setSelectedNodeIDs] = useState<Set<string>>(new Set())
  const [addTargets, setAddTargets] = useState<Node[]>([])
  const selectAllRef = useRef<HTMLInputElement>(null)
  const currentSubscriptionRef = useRef(subscriptionId)
  currentSubscriptionRef.current = subscriptionId
  const subscriptionsQuery = useQuery({
    queryKey: ['subscriptions'],
    queryFn: ({ signal }) => listSubscriptions(signal),
    refetchOnMount: 'always',
  })
  const poolsQuery = useQuery({ queryKey: ['pools'], queryFn: ({ signal }) => listNodePools(signal) })

  useEffect(() => {
    const subscriptions = subscriptionsQuery.data
    if (!subscriptions?.length) {
      if (subscriptionId) setSubscriptionId('')
      return
    }
    if (!subscriptions.some((item) => item.id === subscriptionId)) {
      setSubscriptionId(subscriptions.find((item) => item.active)?.id ?? subscriptions[0].id)
    }
  }, [subscriptionId, subscriptionsQuery.data])

  useEffect(() => {
    try {
      window.localStorage.setItem(gridColumnsStorageKey, String(gridColumns))
    } catch {
      // The layout still works when browser storage is unavailable.
    }
  }, [gridColumns])

  useEffect(() => {
    try {
      window.localStorage.setItem(pageSizeStorageKey, String(pageSize))
    } catch {
      // The layout still works when browser storage is unavailable.
    }
  }, [pageSize])

  useEffect(() => {
    setLatencyResults({})
    setTestingIDs(new Set())
    setLatencyError(null)
    setSelectedNodeIDs(new Set())
    setAddTargets([])
  }, [subscriptionId])

  const detailQuery = useQuery({
    queryKey: ['subscription', subscriptionId],
    queryFn: ({ signal }) => getSubscription(subscriptionId, signal),
    enabled: subscriptionId !== '',
  })
  const selectMutation = useMutation({
    mutationFn: ({ subscriptionId, nodeId }: { subscriptionId: string; nodeId: string }) =>
      selectNode(subscriptionId, nodeId),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['subscription', subscriptionId] })
      await queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
    },
  })
  const testNodes = async (nodeIds: string[]) => {
    const requestSubscription = subscriptionId
    setTestingIDs((current) => new Set([...current, ...nodeIds]))
    setLatencyError(null)
    try {
      const response = await testNodeLatency(requestSubscription, { nodeIds })
      if (currentSubscriptionRef.current !== requestSubscription) return
      setLatencyResults((current) => {
        const next = { ...current }
        for (const result of response.items) next[result.nodeId] = result
        return next
      })
    } catch (error) {
      setLatencyError(error)
    } finally {
      setTestingIDs((current) => {
        const next = new Set(current)
        for (const nodeID of nodeIds) next.delete(nodeID)
        return next
      })
    }
  }
  const addToPoolMutation = useMutation({
    mutationFn: ({ pool, nodes, targetSubscriptionID }: { pool: NodePool; nodes: Node[]; targetSubscriptionID: string }) => {
      const members = pool.members.map((member) => ({ subscriptionId: member.subscriptionId, nodeId: member.nodeId }))
      const existing = new Set(members.map((member) => memberKey(member.subscriptionId, member.nodeId)))
      for (const node of nodes) {
        const key = memberKey(targetSubscriptionID, node.id)
        if (!existing.has(key)) {
          members.push({ subscriptionId: targetSubscriptionID, nodeId: node.id })
          existing.add(key)
        }
      }
      return updateNodePool(pool.id, { members })
    },
    onSuccess: async () => {
      setAddTargets([])
      setSelectedNodeIDs(new Set())
      await queryClient.invalidateQueries({ queryKey: ['pools'] })
    },
  })
  const nodes = useMemo(() => {
    const normalized = search.trim().toLowerCase()
    return (detailQuery.data?.nodes ?? []).filter(
      (node) =>
        !normalized ||
        node.name.toLowerCase().includes(normalized) ||
        node.server.toLowerCase().includes(normalized) ||
        node.type.toLowerCase().includes(normalized),
    )
  }, [detailQuery.data?.nodes, search])
  const allNodes = detailQuery.data?.nodes ?? []
  const pageCount = Math.max(1, Math.ceil(nodes.length / pageSize))
  const currentPage = Math.min(page, pageCount - 1)
  const pageNodes = nodes.slice(currentPage * pageSize, currentPage * pageSize + pageSize)
  const selectedNodes = allNodes.filter((node) => selectedNodeIDs.has(node.id))
  const selectedPageCount = pageNodes.filter((node) => selectedNodeIDs.has(node.id)).length
  const allPageSelected = pageNodes.length > 0 && selectedPageCount === pageNodes.length
  const isTestingAllSelected = selectedNodes.length > 0 && selectedNodes.every((node) => testingIDs.has(node.id))

  useEffect(() => {
    setPage(0)
  }, [search, subscriptionId, pageSize])

  useEffect(() => {
    if (selectAllRef.current) {
      selectAllRef.current.indeterminate = selectedPageCount > 0 && !allPageSelected
    }
  }, [allPageSelected, selectedPageCount])

  const togglePageNodes = (checked: boolean) => {
    setSelectedNodeIDs((current) => {
      const next = new Set(current)
      for (const node of pageNodes) {
        if (checked) next.add(node.id)
        else next.delete(node.id)
      }
      return next
    })
  }

  return (
    <>
      <PageHeading eyebrow="PROXY NODES" title="节点选择" />
      <section className="node-toolbar" aria-label="节点筛选">
        <label>
          <span>订阅</span>
          <select value={subscriptionId} onChange={(event) => setSubscriptionId(event.target.value)}>
            {(subscriptionsQuery.data ?? []).map((item) => (
              <option key={item.id} value={item.id}>{item.name}</option>
            ))}
          </select>
        </label>
        <label className="search-field">
          <Search size={16} aria-hidden="true" />
          <input
            aria-label="搜索节点"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索名称、协议或服务器"
          />
        </label>
        <label>
          <span>每行列数</span>
          <select
            aria-label="每行列数"
            value={gridColumns}
            onChange={(event) => setGridColumns(Number(event.target.value) as GridColumns)}
          >
            <option value={1}>1 列</option>
            <option value={2}>2 列</option>
            <option value={3}>3 列</option>
            <option value={4}>4 列</option>
          </select>
        </label>
        <div className="node-toolbar-actions">
          <label className="node-select-all">
            <input
              ref={selectAllRef}
              type="checkbox"
              checked={allPageSelected}
              disabled={!pageNodes.length}
              onChange={(event) => togglePageNodes(event.target.checked)}
            />
            <span>全选当前页</span>
          </label>
          <span className="node-count">{selectedNodeIDs.size ? `已选 ${selectedNodeIDs.size} · ` : ''}{nodes.length} 个节点</span>
          <button
            className="button"
            type="button"
            disabled={!selectedNodes.length || addToPoolMutation.isPending}
            onClick={() => setAddTargets(selectedNodes)}
          >
            <FolderPlus size={16} aria-hidden="true" />加入节点池{selectedNodes.length ? ` (${selectedNodes.length})` : ''}
          </button>
          <button
            className="button"
            type="button"
            disabled={!selectedNodes.length || testingIDs.size > 0}
            onClick={() => void testNodes(selectedNodes.map((node) => node.id))}
          >
            {isTestingAllSelected ? (
              <LoaderCircle className="spin" size={16} aria-hidden="true" />
            ) : (
              <Gauge size={16} aria-hidden="true" />
            )}
            {isTestingAllSelected ? '测试中' : `测试所选${selectedNodes.length ? ` (${selectedNodes.length})` : ''}`}
          </button>
        </div>
      </section>

      {(subscriptionsQuery.error || poolsQuery.error || detailQuery.error || selectMutation.error || latencyError || addToPoolMutation.error) && (
        <InlineError
          error={subscriptionsQuery.error ?? poolsQuery.error ?? detailQuery.error ?? selectMutation.error ?? latencyError ?? addToPoolMutation.error}
        />
      )}
      <section className={`nodes-panel ${nodes.length ? '' : 'panel'}`} aria-label="节点列表">
        {subscriptionsQuery.isPending || (subscriptionId !== '' && detailQuery.isPending) ? (
          <div className="loading-state">正在读取节点</div>
        ) : nodes.length ? (
          <div className={`node-grid node-grid--${gridColumns}`} role="list">
            {pageNodes.map((node) => {
              const result = latencyResults[node.id]
              const isTesting = testingIDs.has(node.id)
              return (
                <article className={`node-card ${node.selected ? 'node-card--selected' : ''} ${selectedNodeIDs.has(node.id) ? 'node-card--batch-selected' : ''}`} role="listitem" key={node.id}>
                  <div className="node-card-heading">
                    <label className="node-batch-selection" title={`批量选择 ${node.name}`}>
                      <input
                        type="checkbox"
                        aria-label={`批量选择 ${node.name}`}
                        checked={selectedNodeIDs.has(node.id)}
                        onChange={(event) => setSelectedNodeIDs((current) => {
                          const next = new Set(current)
                          if (event.target.checked) next.add(node.id)
                          else next.delete(node.id)
                          return next
                        })}
                      />
                      <span className="protocol-label">{node.type}</span>
                    </label>
                    <span className={node.tls ? 'security-on' : 'security-off'}>
                      {node.tls && <LockKeyhole size={13} aria-hidden="true" />}
                      {node.tls ? 'TLS' : '无 TLS'}
                    </span>
                  </div>
                  <strong className="node-card-name" title={node.name}>{node.name}</strong>
                  <code title={`${node.server}:${node.port}`}>{node.server}:{node.port}</code>
                  <div className="node-card-footer">
                    <label className="node-selection">
                      <input
                        type="radio"
                        name="selected-node"
                        checked={node.selected}
                        disabled={selectMutation.isPending}
                        onChange={() => selectMutation.mutate({ subscriptionId, nodeId: node.id })}
                      />
                      <span>{node.selected ? '已选择' : '选择'}</span>
                    </label>
                    <div className="latency-control">
                      <span
                        className={`latency-result latency-result--${result?.status ?? 'idle'}`}
                        title={result?.detail}
                        aria-live="polite"
                      >
                        {formatLatency(result, isTesting)}
                      </span>
                      <button
                        className="icon-button"
                        type="button"
                        title={`将 ${node.name} 加入节点池`}
                        aria-label={`将 ${node.name} 加入节点池`}
                        onClick={() => setAddTargets([node])}
                      >
                        <FolderPlus size={16} aria-hidden="true" />
                      </button>
                      <button
                        className="icon-button"
                        type="button"
                        title={`测试 ${node.name} 真实代理延迟`}
                        aria-label={`测试 ${node.name} 延迟`}
                        disabled={isTesting || testingIDs.size >= maxConcurrentManualTests}
                        onClick={() => void testNodes([node.id])}
                      >
                        {isTesting ? (
                          <LoaderCircle className="spin" size={16} aria-hidden="true" />
                        ) : (
                          <Gauge size={16} aria-hidden="true" />
                        )}
                      </button>
                    </div>
                  </div>
                </article>
              )
            })}
          </div>
        ) : (
          <div className="empty-state">当前订阅没有可用节点</div>
        )}
        {nodes.length > 0 && (
          <div className="node-pagination">
            <span className="node-count">第 {currentPage + 1} / {pageCount} 页</span>
            <div className="node-pagination-controls">
              <button
                className="icon-button"
                type="button"
                aria-label="上一页"
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                <ChevronLeft size={16} aria-hidden="true" />
              </button>
              <button
                className="icon-button"
                type="button"
                aria-label="下一页"
                disabled={currentPage >= pageCount - 1}
                onClick={() => setPage(currentPage + 1)}
              >
                <ChevronRight size={16} aria-hidden="true" />
              </button>
            </div>
            <label className="node-page-size">
              <span>每页</span>
              <select
                aria-label="每页节点数"
                value={pageSize}
                onChange={(event) => setPageSize(Number(event.target.value) as PageSize)}
              >
                {pageSizeOptions.map((option) => (
                  <option key={option} value={option}>{option}</option>
                ))}
              </select>
            </label>
          </div>
        )}
      </section>
      {addTargets.length > 0 && createPortal(
        <div className="modal-backdrop">
          <section className="pool-picker" role="dialog" aria-modal="true" aria-labelledby="pool-picker-title">
            <div className="pool-picker-heading">
              <div><span>加入节点池</span><strong id="pool-picker-title">{addTargets.length === 1 ? addTargets[0].name : `已选择 ${addTargets.length} 个节点`}</strong></div>
              <button className="icon-button" type="button" title="关闭" aria-label="关闭节点池选择" onClick={() => setAddTargets([])}><X size={16} /></button>
            </div>
            <div className="pool-picker-list">
              {(poolsQuery.data ?? []).map((pool) => {
                const members = pool.members ?? []
                const includedCount = addTargets.filter((node) => members.some((member) => member.subscriptionId === subscriptionId && member.nodeId === node.id)).length
                const allIncluded = includedCount === addTargets.length
                return (
                  <button className="pool-picker-row" type="button" key={pool.id} disabled={allIncluded || addToPoolMutation.isPending} onClick={() => addToPoolMutation.mutate({ pool, nodes: addTargets, targetSubscriptionID: subscriptionId })}>
                    <span><strong>{pool.name}</strong><small>{pool.availableCount}/{pool.memberCount} 可用</small></span>
                    {allIncluded ? <><Check size={16} /><em>已全部加入</em></> : <>{includedCount > 0 && <em>已有 {includedCount}/{addTargets.length}</em>}<FolderPlus size={16} /></>}
                  </button>
                )
              })}
              {!poolsQuery.data?.length && <div className="pool-picker-empty"><span>还没有节点池</span><button className="button" type="button" onClick={() => { window.location.hash = '#pools' }}>新建节点池</button></div>}
            </div>
          </section>
        </div>,
        document.body,
      )}
    </>
  )
}

function readGridColumns(): GridColumns {
  try {
    const value = Number(window.localStorage.getItem(gridColumnsStorageKey))
    if (value === 1 || value === 2 || value === 3 || value === 4) return value
  } catch {
    // Ignore unavailable browser storage and use the default.
  }
  return 2
}

function readPageSize(): PageSize {
  try {
    const value = Number(window.localStorage.getItem(pageSizeStorageKey))
    if (value === 48 || value === 72 || value === 96 || value === 144) return value
  } catch {
    // Ignore unavailable browser storage and use the default.
  }
  return 48
}

function memberKey(subscriptionId: string, nodeId: string) {
  return `${subscriptionId}:${nodeId}`
}

function formatLatency(result: LatencyResult | undefined, isTesting: boolean) {
  if (isTesting) return '测试中'
  if (!result) return '未测试'
  if (result.status === 'ok') return result.latencyMs !== undefined ? `${result.latencyMs} ms` : '可用'
  if (result.status === 'timeout') return '超时'
  return '失败'
}
