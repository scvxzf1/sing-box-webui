import { Activity, Cable, Cpu, Gauge, GitBranch, Globe, Layers3, ListTree, Network, RadioTower, Route, Settings, ShieldCheck, Waypoints } from 'lucide-react'
import type { ConfigurableViewName } from '../uiPreferences'

export type ViewName = 'overview' | 'subscriptions' | 'nodes' | 'pools' | 'chains' | 'channels' | 'rules' | 'traffic' | 'connection' | 'links' | 'dns' | 'core' | 'settings'

interface NavigationProps {
  active: ViewName
  onChange: (view: ViewName) => void
  order: ConfigurableViewName[]
  hidden: ConfigurableViewName[]
}

// oxlint-disable-next-line react/only-export-components -- SettingsView shares this static navigation metadata.
export const navigationItems: Array<{ id: ConfigurableViewName; label: string; icon: typeof Activity }> = [
  { id: 'connection' as const, label: '连接', icon: Cable },
  { id: 'subscriptions' as const, label: '订阅', icon: RadioTower },
  { id: 'links' as const, label: '链接状态', icon: Waypoints },
  { id: 'nodes' as const, label: '节点', icon: ListTree },
  { id: 'pools' as const, label: '节点池', icon: Layers3 },
  { id: 'chains' as const, label: '链式代理', icon: GitBranch },
  { id: 'channels' as const, label: '代理通道', icon: Network },
  { id: 'rules' as const, label: '规则', icon: Route },
  { id: 'traffic' as const, label: '流量策略', icon: Gauge },
  { id: 'dns' as const, label: 'DNS', icon: Globe },
  { id: 'core' as const, label: '核心', icon: Cpu },
  { id: 'overview' as const, label: '概览', icon: Activity },
]

export function Navigation({ active, onChange, order, hidden }: NavigationProps) {
  const byID = new Map<ConfigurableViewName, (typeof navigationItems)[number]>(navigationItems.map((item) => [item.id, item]))
  const hiddenSet = new Set(hidden)
  const visibleItems = order.flatMap((id) => {
    const item = byID.get(id)
    return item && !hiddenSet.has(id) ? [item] : []
  })
  return (
    <aside className="sidebar" aria-label="主导航">
      <div className="sidebar-label">控制面</div>
      <nav className="nav-list">
        {visibleItems.map((item) => {
          const Icon = item.icon
          return (
            <button
              className={`nav-item ${active === item.id ? 'nav-item--active' : ''}`}
              key={item.id}
              onClick={() => onChange(item.id)}
              type="button"
            >
              <Icon size={17} aria-hidden="true" />
              {item.label}
            </button>
          )
        })}
      </nav>
      <button
        className={`nav-item nav-item--settings ${active === 'settings' ? 'nav-item--active' : ''}`}
        onClick={() => onChange('settings')}
        type="button"
      >
        <Settings size={17} aria-hidden="true" />
        全局设置
      </button>
      <div className="sidebar-security">
        <ShieldCheck size={17} aria-hidden="true" />
        <span>本机访问</span>
      </div>
    </aside>
  )
}
