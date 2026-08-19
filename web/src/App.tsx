import { useEffect, useState } from 'react'
import { LogOut } from 'lucide-react'
import { AUTH_REQUIRED_EVENT, getAuthSession, logout } from './api/client'
import { LoginView } from './components/LoginView'
import { Navigation, type ViewName } from './components/Navigation'
import { ThemeToggle } from './components/ThemeToggle'
import { useEventStream } from './hooks/useEventStream'
import { ConnectionView } from './views/ConnectionView'
import { CoreView } from './views/CoreView'
import { DnsView } from './views/DnsView'
import { LinksView } from './views/LinksView'
import { NodesView } from './views/NodesView'
import { OverviewView } from './views/OverviewView'
import { PoolsView } from './views/PoolsView'
import { RulesView } from './views/RulesView'
import { SubscriptionsView } from './views/SubscriptionsView'
import { TrafficPolicyView } from './views/TrafficPolicyView'
import './App.css'

function App() {
  const [authState, setAuthState] = useState<'checking' | 'authenticated' | 'anonymous'>('checking')

  useEffect(() => {
    void getAuthSession().then(() => setAuthState('authenticated')).catch(() => setAuthState('anonymous'))
    const requireAuth = () => setAuthState('anonymous')
    window.addEventListener(AUTH_REQUIRED_EVENT, requireAuth)
    return () => window.removeEventListener(AUTH_REQUIRED_EVENT, requireAuth)
  }, [])

  if (authState !== 'authenticated') {
    return <LoginView checking={authState === 'checking'} onAuthenticated={() => setAuthState('authenticated')} />
  }

  return <AuthenticatedApp onLogout={() => setAuthState('anonymous')} />
}

function AuthenticatedApp({ onLogout }: { onLogout: () => void }) {
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

  const signOut = async () => {
    try { await logout() } finally { onLogout() }
  }

  return (
    <div className="app-shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            <img src="/brand-mark.svg" alt="" />
          </span>
          <span>sing-box WebUI</span>
        </div>
        <div className="topbar-actions">
          <div className="endpoint">
            <span className="status-dot status-dot--ok" aria-hidden="true" />
            127.0.0.1:11872
          </div>
          <ThemeToggle />
          <button className="icon-button" type="button" title="退出登录" aria-label="退出登录" onClick={() => void signOut()}><LogOut size={16} /></button>
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
            {view === 'dns' && <DnsView />}
            {view === 'connection' && <ConnectionView />}
            {view === 'links' && <LinksView />}
            {view === 'core' && <CoreView />}
          </div>
        </main>
      </div>
    </div>
  )
}

function viewFromHash(hash: string): ViewName {
  const value = hash.replace(/^#/, '')
  return value === 'subscriptions' || value === 'nodes' || value === 'pools' || value === 'rules' || value === 'traffic' || value === 'connection' || value === 'links' || value === 'dns' || value === 'core'
    ? value
    : 'overview'
}

export default App
