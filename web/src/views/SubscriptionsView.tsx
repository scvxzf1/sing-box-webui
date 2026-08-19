import { useEffect, useState, type DragEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, GripVertical, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import {
  activateSubscription,
  createSubscription,
  deleteSubscription,
  getSubscription,
  listSubscriptions,
  reorderSubscriptions,
  refreshSubscription,
  updateSubscription,
} from '../api/client'
import type { CreateSubscription, Subscription } from '../api/types'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

const initialForm: CreateSubscription = {
  name: '',
  url: '',
  autoUpdate: true,
  updateIntervalMinutes: 360,
}

export function SubscriptionsView() {
  const queryClient = useQueryClient()
  const [selectedId, setSelectedId] = useState('')
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<CreateSubscription>(initialForm)
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState<Error | null>(null)
  const subscriptionsQuery = useQuery({
    queryKey: ['subscriptions'],
    queryFn: ({ signal }) => listSubscriptions(signal),
    refetchInterval: 60_000,
  })
  const [draggingId, setDraggingId] = useState<string | null>(null)
  const [dropIndicator, setDropIndicator] = useState<{ id: string; position: 'before' | 'after' } | null>(null)

  useEffect(() => {
    if (!selectedId && subscriptionsQuery.data?.length) {
      setSelectedId(subscriptionsQuery.data.find((item) => item.active)?.id ?? subscriptionsQuery.data[0].id)
    }
  }, [selectedId, subscriptionsQuery.data])

  useEffect(() => {
    setCopied(false)
    setCopyError(null)
  }, [selectedId])

  const detailQuery = useQuery({
    queryKey: ['subscription', selectedId],
    queryFn: ({ signal }) => getSubscription(selectedId, signal),
    enabled: selectedId !== '',
  })

  const invalidate = async (id?: string) => {
    await queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
    if (id) await queryClient.invalidateQueries({ queryKey: ['subscription', id] })
  }

  const createMutation = useMutation({
    mutationFn: createSubscription,
    onSuccess: async (item) => {
      setSelectedId(item.id)
      setAdding(false)
      setForm(initialForm)
      await invalidate(item.id)
    },
  })
  const refreshMutation = useMutation({
    mutationFn: refreshSubscription,
    onSuccess: (item) => invalidate(item.id),
  })
  const activateMutation = useMutation({
    mutationFn: activateSubscription,
    onSuccess: (item) => invalidate(item.id),
  })
  const deleteMutation = useMutation({
    mutationFn: deleteSubscription,
    onSuccess: async () => {
      setSelectedId('')
      await invalidate()
    },
  })
  const updateMutation = useMutation({
    mutationFn: ({ id, autoUpdate, interval }: { id: string; autoUpdate: boolean; interval: number }) =>
      updateSubscription(id, { autoUpdate, updateIntervalMinutes: interval }),
    onSuccess: (item) => invalidate(item.id),
  })
  const reorderMutation = useMutation({
    mutationFn: reorderSubscriptions,
    onMutate: async (ids) => {
      await queryClient.cancelQueries({ queryKey: ['subscriptions'] })
      const previous = queryClient.getQueryData<Subscription[]>(['subscriptions'])
      if (previous) {
        const byID = new Map(previous.map((item) => [item.id, item]))
        queryClient.setQueryData(
          ['subscriptions'],
          ids.map((id) => byID.get(id)).filter((item): item is Subscription => Boolean(item)),
        )
      }
      return { previous }
    },
    onSuccess: (items) => queryClient.setQueryData(['subscriptions'], items),
    onError: (_error, _ids, context) => {
      if (context?.previous) queryClient.setQueryData(['subscriptions'], context.previous)
    },
    onSettled: async () => {
      setDraggingId(null)
      setDropIndicator(null)
      await queryClient.invalidateQueries({ queryKey: ['subscriptions'] })
    },
  })

  useEffect(() => {
    createMutation.reset()
    refreshMutation.reset()
    activateMutation.reset()
    deleteMutation.reset()
    updateMutation.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId, adding])

  const mutationError =
    copyError ??
    createMutation.error ??
    refreshMutation.error ??
    activateMutation.error ??
    deleteMutation.error ??
    updateMutation.error
  const selected = detailQuery.data
  const subscriptions = subscriptionsQuery.data ?? []
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
    const ids = moveID(subscriptions.map((item) => item.id), sourceID, targetID, indicator.position)
    if (ids) reorderMutation.mutate(ids)
  }

  return (
    <>
      <PageHeading
        eyebrow="SUBSCRIPTION SOURCES"
        title="订阅管理"
        action={
          <button className="button button--primary" type="button" onClick={() => setAdding(true)}>
            <Plus size={16} aria-hidden="true" />
            添加订阅
          </button>
        }
      />

      {adding && (
        <section className="panel add-subscription" aria-labelledby="add-subscription-title">
          <div className="section-heading">
            <div>
              <h2 id="add-subscription-title">新订阅</h2>
              <p>HTTPS 或 HTTP URI 列表</p>
            </div>
            <button className="icon-button" type="button" title="关闭" onClick={() => setAdding(false)}>
              <X size={17} aria-hidden="true" />
            </button>
          </div>
          <form
            className="subscription-form"
            onSubmit={(event) => {
              event.preventDefault()
              createMutation.mutate(form)
            }}
          >
            <label>
              <span>名称</span>
              <input
                required
                maxLength={80}
                value={form.name}
                onChange={(event) => setForm({ ...form, name: event.target.value })}
                placeholder="主订阅"
              />
            </label>
            <label className="form-field--wide">
              <span>订阅地址</span>
              <input
                required
                type="url"
                value={form.url}
                onChange={(event) => setForm({ ...form, url: event.target.value })}
                placeholder="https://example.com/subscription"
              />
            </label>
            <label className="checkbox-field">
              <input
                type="checkbox"
                checked={form.autoUpdate}
                onChange={(event) => setForm({ ...form, autoUpdate: event.target.checked })}
              />
              <span>自动更新</span>
            </label>
            <label>
              <span>更新间隔</span>
              <select
                value={form.updateIntervalMinutes}
                onChange={(event) => setForm({ ...form, updateIntervalMinutes: Number(event.target.value) })}
              >
                <option value={60}>每小时</option>
                <option value={360}>每 6 小时</option>
                <option value={720}>每 12 小时</option>
                <option value={1440}>每天</option>
              </select>
            </label>
            <button className="button button--primary form-submit" disabled={createMutation.isPending} type="submit">
              {createMutation.isPending ? '正在拉取' : '保存并拉取'}
            </button>
          </form>
        </section>
      )}

      {(mutationError || reorderMutation.error) && <InlineError error={mutationError ?? reorderMutation.error} />}

      <div className="subscriptions-layout">
        <section className="panel subscription-list" aria-label="订阅列表">
          {subscriptionsQuery.isPending ? (
            <div className="loading-state">正在读取订阅</div>
          ) : subscriptions.length ? (
            subscriptions.map((item) => (
              <button
                className={`subscription-row ${selectedId === item.id ? 'subscription-row--selected' : ''} ${draggingId === item.id ? 'subscription-row--dragging' : ''} ${dropIndicator?.id === item.id ? `subscription-row--drop-${dropIndicator.position}` : ''}`}
                key={item.id}
                type="button"
                draggable={!reorderMutation.isPending}
                aria-grabbed={draggingId === item.id}
                onDragStart={(event) => handleDragStart(event, item.id)}
                onDragOver={(event) => handleDragOver(event, item.id)}
                onDrop={(event) => handleDrop(event, item.id)}
                onDragEnd={() => { setDraggingId(null); setDropIndicator(null) }}
                onClick={() => setSelectedId(item.id)}
              >
                <GripVertical className="drag-handle" size={16} aria-hidden="true" />
                <span className="subscription-row-main">
                  <strong>{item.name}</strong>
                  <span>{item.nodeCount} 个节点</span>
                </span>
                {item.active && <span className="active-mark">活动</span>}
              </button>
            ))
          ) : (
            <div className="empty-state">暂无订阅</div>
          )}
        </section>

        <section className="panel subscription-detail" aria-label="订阅详情">
          {detailQuery.isPending && selectedId ? (
            <div className="loading-state">正在读取订阅详情</div>
          ) : detailQuery.isError ? (
            <div className="empty-state">
              <InlineError error={detailQuery.error} />
              <button className="button" type="button" onClick={() => void detailQuery.refetch()}>重试</button>
            </div>
          ) : selected ? (
            <>
              <div className="section-heading">
                <div>
                  <h2>{selected.name}</h2>
                  <p>{selected.nodeCount} 个节点</p>
                </div>
                <div className="toolbar">
                  <button
                    className="icon-button"
                    title="立即更新"
                    type="button"
                    disabled={refreshMutation.isPending}
                    onClick={() => refreshMutation.mutate(selected.id)}
                  >
                    <RefreshCw size={17} aria-hidden="true" />
                  </button>
                  <button
                    className="icon-button icon-button--danger"
                    title="删除订阅"
                    type="button"
                    aria-label={`删除订阅 ${selected.name}`}
                    disabled={deleteMutation.isPending}
                    onClick={() =>
                      window.confirm(`删除订阅“${selected.name}”？其中 ${selected.nodeCount} 个节点及其选择状态将一并移除。`) &&
                      deleteMutation.mutate(selected.id)
                    }
                  >
                    <Trash2 size={17} aria-hidden="true" />
                  </button>
                </div>
              </div>
              <div className="detail-body">
                <div className="subscription-url-field">
                  <label htmlFor="subscription-url">订阅地址</label>
                  <div>
                    <input id="subscription-url" readOnly spellCheck={false} value={selected.url} />
                    <button
                      className="icon-button"
                      type="button"
                      title={copied ? '已复制' : '复制订阅地址'}
                      aria-label={copied ? '订阅地址已复制' : '复制订阅地址'}
                      onClick={() => {
                        navigator.clipboard.writeText(selected.url).then(
                          () => {
                            setCopied(true)
                            setCopyError(null)
                          },
                          (error: unknown) => {
                            setCopied(false)
                            setCopyError(error instanceof Error ? error : new Error('复制订阅地址失败'))
                          },
                        )
                      }}
                    >
                      {copied ? <Check size={17} aria-hidden="true" /> : <Copy size={17} aria-hidden="true" />}
                    </button>
                  </div>
                </div>
                <div className="detail-metrics">
                  <div><span>节点</span><strong>{selected.nodeCount}</strong></div>
                  <div>
                    <span>最近更新</span>
                    <strong>
                      {formatTime(selected.lastUpdated)}
                      {selected.lastFetchPath === 'proxy' && <em className="fetch-path-badge">经代理</em>}
                    </strong>
                  </div>
                  <div><span>状态</span><strong>{selected.lastError ? '更新失败' : '可用'}</strong></div>
                </div>
                {selected.lastError && <InlineError error={new Error(selected.lastError)} />}
                <div className="settings-row">
                  <label className="checkbox-field">
                    <input
                      type="checkbox"
                      checked={selected.autoUpdate}
                      onChange={(event) =>
                        updateMutation.mutate({
                          id: selected.id,
                          autoUpdate: event.target.checked,
                          interval: selected.updateIntervalMinutes,
                        })
                      }
                    />
                    <span>自动更新</span>
                  </label>
                  <select
                    aria-label="自动更新间隔"
                    value={selected.updateIntervalMinutes}
                    onChange={(event) =>
                      updateMutation.mutate({
                        id: selected.id,
                        autoUpdate: selected.autoUpdate,
                        interval: Number(event.target.value),
                      })
                    }
                  >
                    <option value={60}>每小时</option>
                    <option value={360}>每 6 小时</option>
                    <option value={720}>每 12 小时</option>
                    <option value={1440}>每天</option>
                  </select>
                  {!selected.active && (
                    <button className="button" type="button" onClick={() => activateMutation.mutate(selected.id)}>
                      <Check size={16} aria-hidden="true" />
                      切换到此订阅
                    </button>
                  )}
                </div>
              </div>
            </>
          ) : (
            <div className="empty-state">选择一个订阅查看详情</div>
          )}
        </section>
      </div>
    </>
  )
}

function formatTime(value?: string) {
  if (!value) return '从未'
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value))
}

function moveID(ids: string[], sourceID: string, targetID: string, position: 'before' | 'after') {
  const sourceIndex = ids.indexOf(sourceID)
  const targetIndex = ids.indexOf(targetID)
  if (sourceIndex < 0 || targetIndex < 0 || sourceID === targetID) return null
  const next = ids.filter((id) => id !== sourceID)
  const nextTargetIndex = next.indexOf(targetID)
  next.splice(nextTargetIndex + (position === 'after' ? 1 : 0), 0, sourceID)
  return next
}
