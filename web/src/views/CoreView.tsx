import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Cpu, Download, RotateCcw, TriangleAlert } from 'lucide-react'
import { getCore, getRuntime, rollbackCore, updateCore } from '../api/client'
import { InlineError } from '../components/InlineError'
import { PageHeading } from '../components/PageHeading'

export function CoreView() {
  const queryClient = useQueryClient()
  const [version, setVersion] = useState('')
  const coreQuery = useQuery({
    queryKey: ['core'],
    queryFn: ({ signal }) => getCore(signal),
  })
  const runtimeQuery = useQuery({
    queryKey: ['runtime'],
    queryFn: ({ signal }) => getRuntime(signal),
    refetchInterval: 3_000,
  })
  const refreshCore = async () => {
    await queryClient.invalidateQueries({ queryKey: ['core'] })
    await queryClient.invalidateQueries({ queryKey: ['status'] })
    setVersion('')
  }
  const updateMutation = useMutation({
    mutationFn: updateCore,
    onSuccess: refreshCore,
  })
  const rollbackMutation = useMutation({
    mutationFn: rollbackCore,
    onSuccess: refreshCore,
  })
  const core = coreQuery.data
  const runtimeBusy = runtimeQuery.data?.state !== 'stopped' && runtimeQuery.data?.state !== 'failed'
  const operationPending = updateMutation.isPending || rollbackMutation.isPending
  const operationError = updateMutation.error ?? rollbackMutation.error

  return (
    <>
      <PageHeading
        eyebrow="MANAGED RUNTIME"
        title="sing-box 核心"
        action={
          <span className={`runtime-badge ${core?.source === 'managed' ? 'runtime-badge--running' : ''}`}>
            {core?.source === 'managed' ? '内嵌托管' : '外部核心'}
          </span>
        }
      />

      {operationError && <InlineError error={operationError} />}

      <section className="panel" aria-labelledby="core-status-title">
        <div className="section-heading">
          <div>
            <h2 id="core-status-title">版本状态</h2>
            <p>{core?.platform ?? '正在检测平台'}</p>
          </div>
          <Cpu size={19} aria-hidden="true" />
        </div>
        {coreQuery.isPending ? (
          <div className="loading-state">正在读取核心状态</div>
        ) : coreQuery.isError ? (
          <div className="error-state" role="alert">无法读取核心状态</div>
        ) : (
          <dl className="core-version-grid">
            <div>
              <dt>当前版本</dt>
              <dd>{core?.currentVersion || '未知'}</dd>
            </div>
            <div>
              <dt>回退版本</dt>
              <dd>{core?.previousVersion || '尚无'}</dd>
            </div>
            <div>
              <dt>内嵌基线</dt>
              <dd>{core?.embeddedVersion || '未知'}</dd>
            </div>
          </dl>
        )}
      </section>

      <section className="panel panel--spaced" aria-labelledby="core-update-title">
        <div className="section-heading">
          <div>
            <h2 id="core-update-title">核心更新</h2>
            <p>官方稳定版本</p>
          </div>
          <Download size={19} aria-hidden="true" />
        </div>
        <div className="core-update-form">
          <label>
            指定版本
            <input
              value={version}
              placeholder="留空更新到最新版"
              inputMode="decimal"
              onChange={(event) => setVersion(event.target.value)}
            />
          </label>
          <button
            className="button button--primary"
            type="button"
            disabled={!core?.updateSupported || runtimeBusy || operationPending}
            onClick={() => updateMutation.mutate(version ? { version } : {})}
          >
            <Download size={15} aria-hidden="true" />
            {updateMutation.isPending ? '正在安装' : '下载并切换'}
          </button>
          <button
            className="button"
            type="button"
            disabled={!core?.updateSupported || !core.previousVersion || runtimeBusy || operationPending}
            onClick={() => rollbackMutation.mutate()}
          >
            <RotateCcw size={15} aria-hidden="true" />
            {rollbackMutation.isPending ? '正在回滚' : '回滚'}
          </button>
        </div>
        {runtimeBusy && (
          <div className="core-update-note">
            <TriangleAlert size={15} aria-hidden="true" />
            请先停止代理再切换核心版本
          </div>
        )}
      </section>
    </>
  )
}
