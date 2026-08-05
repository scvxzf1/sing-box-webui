import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Plus, RefreshCw, Trash2, X } from 'lucide-react'
import {
  activateSubscription,
  createSubscription,
  deleteSubscription,
  getSubscription,
  listSubscriptions,
  refreshSubscription,
  updateSubscription,
} from '../api/client'
import type { CreateSubscription } from '../api/types'
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

      {mutationError && <InlineError error={mutationError} />}

      <div className="subscriptions-layout">
        <section className="panel subscription-list" aria-label="订阅列表">
          {subscriptionsQuery.isPending ? (
            <div className="loading-state">正在读取订阅</div>
          ) : subscriptionsQuery.data?.length ? (
            subscriptionsQuery.data.map((item) => (
              <button
                className={`subscription-row ${selectedId === item.id ? 'subscription-row--selected' : ''}`}
                key={item.id}
                type="button"
                onClick={() => setSelectedId(item.id)}
              >
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
                  <div><span>最近更新</span><strong>{formatTime(selected.lastUpdated)}</strong></div>
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
