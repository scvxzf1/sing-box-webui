import type { LucideIcon } from 'lucide-react'
import { Activity, Box, Radio } from 'lucide-react'
import type { StatusResponse } from '../api/types'
import type { EventStreamState } from '../hooks/useEventStream'

interface StatusGridProps {
  status: StatusResponse
  eventStream: EventStreamState
}

interface Item {
  key: string
  label: string
  value: string
  detail: string
  tone: 'ok' | 'idle' | 'error'
  icon: LucideIcon
}

export function StatusGrid({ status, eventStream }: StatusGridProps) {
  const web = status.components.web
  const core = status.components.core
  const singBox = status.components.singBox

  const items: Item[] = [
    {
      key: 'web',
      label: 'Web API',
      value: web?.state === 'healthy' ? '正常' : '异常',
      detail: `版本 ${status.version}`,
      tone: web?.state === 'healthy' ? 'ok' : 'error',
      icon: Activity,
    },
    {
      key: 'core',
      label: 'Core',
      value: core?.state === 'healthy' ? '正常' : '未配置',
      detail: core?.state === 'healthy' ? '核心进程管理器' : '等待核心进程管理器',
      tone: core?.state === 'healthy' ? 'ok' : 'idle',
      icon: Box,
    },
    {
      key: 'sing-box',
      label: 'sing-box',
      value: singBox?.state === 'healthy' ? '运行中' : '未连接',
      detail: singBox?.state === 'healthy' ? '受管进程正常' : '等待受管进程',
      tone: singBox?.state === 'healthy' ? 'ok' : 'idle',
      icon: Radio,
    },
    {
      key: 'events',
      label: '事件通道',
      value: eventStream === 'connected' ? '已连接' : eventStream === 'error' ? '已中断' : '等待连接',
      detail: 'Server-Sent Events',
      tone: eventStream === 'connected' ? 'ok' : eventStream === 'error' ? 'error' : 'idle',
      icon: Radio,
    },
  ]

  return (
    <div className="status-grid">
      {items.map((item) => {
        const Icon = item.icon
        return (
          <article className="status-item" key={item.key}>
            <div className={`status-icon status-icon--${item.tone}`}>
              <Icon size={18} aria-hidden="true" />
            </div>
            <div className="status-copy">
              <span className="status-label">{item.label}</span>
              <strong>{item.value}</strong>
              <span className="status-detail">{item.detail}</span>
            </div>
          </article>
        )
      })}
    </div>
  )
}
