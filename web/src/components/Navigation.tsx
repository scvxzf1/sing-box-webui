import { Activity, Cable, Cpu, Gauge, Globe, Layers3, ListTree, RadioTower, Route, ShieldCheck, Waypoints } from 'lucide-react'

export type ViewName = 'overview' | 'subscriptions' | 'nodes' | 'pools' | 'rules' | 'traffic' | 'connection' | 'links' | 'dns' | 'core'

interface NavigationProps {
  active: ViewName
  onChange: (view: ViewName) => void
}

const items = [
  { id: 'connection' as const, label: '连接', icon: Cable },
  { id: 'subscriptions' as const, label: '订阅', icon: RadioTower },
  { id: 'links' as const, label: '链接状态', icon: Waypoints },
  { id: 'nodes' as const, label: '节点', icon: ListTree },
  { id: 'pools' as const, label: '节点池', icon: Layers3 },
  { id: 'rules' as const, label: '规则', icon: Route },
  { id: 'traffic' as const, label: '流量策略', icon: Gauge },
  { id: 'dns' as const, label: 'DNS', icon: Globe },
  { id: 'core' as const, label: '核心', icon: Cpu },
  { id: 'overview' as const, label: '概览', icon: Activity },
]

export function Navigation({ active, onChange }: NavigationProps) {
  return (
    <aside className="sidebar" aria-label="主导航">
      <div className="sidebar-label">控制面</div>
      <nav className="nav-list">
        {items.map((item) => {
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
      <div className="sidebar-security">
        <ShieldCheck size={17} aria-hidden="true" />
        <span>本机访问</span>
      </div>
    </aside>
  )
}
