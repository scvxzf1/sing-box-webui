import { useQuery } from '@tanstack/react-query'
import { Radio, Server } from 'lucide-react'
import { getRuntime, getStatus, listSubscriptions } from '../api/client'
import { PageHeading } from '../components/PageHeading'
import { StatusGrid } from '../components/StatusGrid'
import { InlineError } from '../components/InlineError'
import type { EventStreamState } from '../hooks/useEventStream'

export function OverviewView({ eventStream }: { eventStream: EventStreamState }) {
  const statusQuery = useQuery({
    queryKey: ['status'],
    queryFn: ({ signal }) => getStatus(signal),
    refetchInterval: 30_000,
  })
  const subscriptionsQuery = useQuery({
    queryKey: ['subscriptions'],
    queryFn: ({ signal }) => listSubscriptions(signal),
  })
  const runtimeQuery = useQuery({
    queryKey: ['runtime'],
    queryFn: ({ signal }) => getRuntime(signal),
    refetchInterval: 5_000,
  })
  const activeSubscription = subscriptionsQuery.data?.find((item) => item.active)

  return (
    <>
      <PageHeading
        eyebrow="LOCAL CONTROL PLANE"
        title="运行概览"
        action={
          <div className={`stream-state stream-state--${eventStream}`}>
            <Radio size={15} aria-hidden="true" />
            {eventStream === 'connected'
              ? '事件流已连接'
              : eventStream === 'error'
                ? '事件流中断'
                : '正在连接事件流'}
          </div>
        }
      />

      <section className="panel" aria-labelledby="component-status-title">
        <div className="section-heading">
          <div>
            <h2 id="component-status-title">组件</h2>
            <p>当前进程状态</p>
          </div>
          <Server size={19} aria-hidden="true" />
        </div>
        {statusQuery.isPending ? (
          <div className="loading-state">正在读取本机状态</div>
        ) : statusQuery.isError ? (
          <div className="error-state" role="alert">
            <strong>无法连接本机 API</strong>
            <span>请确认 Go 后端正在 31334 端口运行。</span>
          </div>
        ) : (
          <StatusGrid status={statusQuery.data} eventStream={eventStream} />
        )}
      </section>

      <section className="panel panel--spaced" aria-labelledby="workspace-title">
        <div className="section-heading">
          <div>
            <h2 id="workspace-title">工作区</h2>
            <p>当前选择</p>
          </div>
        </div>
        <dl className="runtime-list">
          <div>
            <dt>活动订阅</dt>
            <dd>{activeSubscription?.name ?? '未添加'}</dd>
          </div>
          <div>
            <dt>已选节点</dt>
            <dd>{runtimeQuery.data?.poolName ?? runtimeQuery.data?.nodeName ?? '未应用'}</dd>
          </div>
          <div>
            <dt>代理状态</dt>
            <dd>{runtimeQuery.isPending || runtimeQuery.isError || !runtimeQuery.data ? '状态未知' : runtimeQuery.data.state === 'running' ? '运行中' : runtimeQuery.data.state === 'stopped' ? '已停止' : '处理中'}</dd>
          </div>
        </dl>
        {runtimeQuery.error && <InlineError error={runtimeQuery.error} />}
      </section>
    </>
  )
}
