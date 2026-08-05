import { useEffect, useState } from 'react'
import { Activity } from 'lucide-react'
import { Navigation, type ViewName } from './components/Navigation'
import { ThemeToggle } from './components/ThemeToggle'
import { useEventStream } from './hooks/useEventStream'
import { ConnectionView } from './views/ConnectionView'
import { CoreView } from './views/CoreView'
import { NodesView } from './views/NodesView'
import { OverviewView } from './views/OverviewView'
import { PoolsView } from './views/PoolsView'
import { RulesView } from './views/RulesView'
import { SubscriptionsView } from './views/SubscriptionsView'
import { TrafficPolicyView } from './views/TrafficPolicyView'
import './App.css'

function App() {
  const [view, setView] = useState<ViewName>(() => viewFromHash(window.location.hash))
  const eventStream = useEventStream('/api/v1/events')

  useEffect(() => {
    const onHashChange = () => setView(viewFromHash(window.location.hash))
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  const changeView = (nextView: ViewName) => {
    setView(nextView)
    window.history.replaceState(null, '', nextView === 'overview' ? '#overview' : `#${nextView}`)
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            <Activity size={18} strokeWidth={2.2} />
          </span>
          <span>sing-box WebUI</span>
        </div>
        <div className="topbar-actions">
          <div className="endpoint">
            <span className="status-dot status-dot--ok" aria-hidden="true" />
            127.0.0.1:11872
          </div>
          <ThemeToggle />
        </div>
      </header>
      <div className="workspace">
        <Navigation active={view} onChange={changeView} />
        <main className="content-scroll" key={view}>
          <div className={`content content--${view}`}>
            {view === 'overview' && <OverviewView eventStream={eventStream} />}
            {view === 'subscriptions' && <SubscriptionsView />}
            {view === 'nodes' && <NodesView />}
            {view === 'pools' && <PoolsView />}
            {view === 'rules' && <RulesView />}
            {view === 'traffic' && <TrafficPolicyView />}
            {view === 'connection' && <ConnectionView />}
            {view === 'core' && <CoreView />}
          </div>
        </main>
      </div>
    </div>
  )
}

function viewFromHash(hash: string): ViewName {
  const value = hash.replace(/^#/, '')
  return value === 'subscriptions' || value === 'nodes' || value === 'pools' || value === 'rules' || value === 'traffic' || value === 'connection' || value === 'core'
    ? value
    : 'overview'
}

export default App
