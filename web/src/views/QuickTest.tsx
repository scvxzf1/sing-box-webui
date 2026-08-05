import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Gauge, Loader2, Play, Plus, Trash2 } from 'lucide-react'
import {
  createConnectivityTarget,
  deleteConnectivityTarget,
  listConnectivityTargets,
  testAllConnectivity,
  testConnectivity,
} from '../api/client'
import type { ConnectivityPathResult, ConnectivityResult } from '../api/types'
import { InlineError } from '../components/InlineError'

type ResultMap = Record<string, ConnectivityResult>

export function QuickTest() {
  const queryClient = useQueryClient()
  const targetsQuery = useQuery({ queryKey: ['connectivity'], queryFn: ({ signal }) => listConnectivityTargets(signal) })
  const [results, setResults] = useState<ResultMap>({})
  const [testingIds, setTestingIds] = useState<Set<string>>(new Set())
  const [testingAll, setTestingAll] = useState(false)
  const [showForm, setShowForm] = useState(false)
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [testError, setTestError] = useState<unknown>(null)

  const mergeResults = (items: ConnectivityResult[]) => {
    setResults((previous) => {
      const next = { ...previous }
      for (const item of items) next[item.targetId] = item
      return next
    })
  }

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['connectivity'] })

  const createMutation = useMutation({
    mutationFn: createConnectivityTarget,
    onSuccess: () => {
      setName('')
      setUrl('')
      setShowForm(false)
      void invalidate()
    },
  })
  const deleteMutation = useMutation({
    mutationFn: deleteConnectivityTarget,
    onSuccess: () => void invalidate(),
  })

  const runSingle = async (id: string) => {
    setTestError(null)
    setTestingIds((previous) => new Set(previous).add(id))
    try {
      const response = await testConnectivity(id)
      mergeResults(response.items)
    } catch (error) {
      setTestError(error)
    } finally {
      setTestingIds((previous) => {
        const next = new Set(previous)
        next.delete(id)
        return next
      })
    }
  }

  const runAll = async () => {
    setTestError(null)
    setTestingAll(true)
    try {
      const response = await testAllConnectivity()
      mergeResults(response.items)
    } catch (error) {
      setTestError(error)
    } finally {
      setTestingAll(false)
    }
  }

  const targets = targetsQuery.data ?? []
  const busy = testingAll || testingIds.size > 0

  return (
    <div className="connection-step">
      <span className="step-index">4</span>
      <div className="quick-test">
        <div className="quick-test__head">
          <h2><Gauge size={16} aria-hidden="true" /> 快速测试</h2>
          <div className="quick-test__actions">
            <button
              className="button button--ghost button--sm"
              type="button"
              onClick={() => setShowForm((value) => !value)}
            >
              <Plus size={15} aria-hidden="true" /> 添加
            </button>
            <button
              className="button button--primary button--sm"
              type="button"
              disabled={targets.length === 0 || busy}
              onClick={() => void runAll()}
            >
              {testingAll ? <Loader2 size={15} className="spin" aria-hidden="true" /> : <Play size={15} aria-hidden="true" />}
              {testingAll ? '测试中…' : '测试全部'}
            </button>
          </div>
        </div>

        {(targetsQuery.error || createMutation.error || deleteMutation.error || testError) != null && (
          <InlineError error={targetsQuery.error ?? createMutation.error ?? deleteMutation.error ?? testError} />
        )}

        {showForm && (
          <form
            className="quick-test__form"
            onSubmit={(event) => {
              event.preventDefault()
              createMutation.mutate({ name: name.trim(), url: url.trim() })
            }}
          >
            <input
              aria-label="目标名称"
              placeholder="名称，如 Google"
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
            />
            <input
              aria-label="目标地址"
              placeholder="https://…"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              required
            />
            <button className="button button--primary button--sm" type="submit" disabled={createMutation.isPending}>
              {createMutation.isPending ? '保存中…' : '保存'}
            </button>
          </form>
        )}

        {targetsQuery.isLoading ? (
          <div className="muted-line">正在加载测试目标…</div>
        ) : targets.length === 0 ? (
          <div className="muted-line">暂无测试目标，点击"添加"创建一个</div>
        ) : (
          <ul className="quick-test__list">
            {targets.map((target) => {
              const result = results[target.id]
              const testing = testingIds.has(target.id)
              return (
                <li key={target.id} className="quick-test__row">
                  <div className="quick-test__meta">
                    <strong>{target.name}</strong>
                    <span className="quick-test__url">{target.url}</span>
                  </div>
                  <div className="quick-test__results">
                    {result ? <PathBadges result={result} /> : <span className="quick-test__pending">未测试</span>}
                  </div>
                  <div className="quick-test__row-actions">
                    <button
                      className="button button--ghost button--sm"
                      type="button"
                      disabled={busy && !testing}
                      onClick={() => void runSingle(target.id)}
                      aria-label={`测试 ${target.name}`}
                    >
                      {testing ? <Loader2 size={14} className="spin" aria-hidden="true" /> : <Play size={14} aria-hidden="true" />}
                    </button>
                    <button
                      className="icon-button icon-button--danger"
                      type="button"
                      onClick={() => deleteMutation.mutate(target.id)}
                      aria-label={`删除 ${target.name}`}
                    >
                      <Trash2 size={14} aria-hidden="true" />
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </div>
  )
}

function PathBadges({ result }: { result: ConnectivityResult }) {
  return (
    <>
      <PathBadge label="直连" value={result.direct} />
      {result.proxy && <PathBadge label="代理" value={result.proxy} />}
    </>
  )
}

function PathBadge({ label, value }: { label: string; value: ConnectivityPathResult }) {
  if (value.status === 'ok' && value.latencyMs != null) {
    return (
      <span className="latency-result latency-result--ok" title={value.detail}>
        {label} {value.latencyMs} ms
      </span>
    )
  }
  return (
    <span className={`latency-result latency-result--${value.status}`} title={value.detail}>
      {label} {value.status === 'timeout' ? '超时' : '失败'}
    </span>
  )
}
