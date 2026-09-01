import { useEffect, useState, type CSSProperties } from 'react'
import { LogOut } from 'lucide-react'
import { AUTH_REQUIRED_EVENT, getAuthSession, logout } from './api/client'
import { LoginView } from './components/LoginView'
import { Navigation, type ViewName } from './components/Navigation'
import { ThemeToggle } from './components/ThemeToggle'
import { useEventStream } from './hooks/useEventStream'
import { ConnectionView } from './views/ConnectionView'
import { ChainsView } from './views/ChainsView'
import { ChannelsView } from './views/ChannelsView'
import { CoreView } from './views/CoreView'
import { DnsView } from './views/DnsView'
import { LinksView } from './views/LinksView'
import { NodesView } from './views/NodesView'
import { OverviewView } from './views/OverviewView'
import { PoolsView } from './views/PoolsView'
import { RulesView } from './views/RulesView'
import { SubscriptionsView } from './views/SubscriptionsView'
import { TrafficPolicyView } from './views/TrafficPolicyView'
import { SettingsView } from './views/SettingsView'
import { fontScale, readUIPreferences, saveUIPreferences, viewScale, type UIPreferences } from './uiPreferences'
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
  const [uiPreferences, setUIPreferences] = useState<UIPreferences>(readUIPreferences)
  const eventStream = useEventStream('/api/v1/events')

  const updateUIPreferences = (preferences: UIPreferences) => {
    setUIPreferences(preferences)
    try {
      saveUIPreferences(preferences)
    } catch {
      // Preferences remain active for this session when browser storage is unavailable.
    }
  }

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
            127.0.0.1:31334
          </div>
          <ThemeToggle />
          <button className="icon-button" type="button" title="退出登录" aria-label="退出登录" onClick={() => void signOut()}><LogOut size={16} /></button>
        </div>
      </header>
      <div className="workspace" style={{ '--sidebar-width': `${uiPreferences.sidebarWidth}px` } as CSSProperties}>
        <Navigation active={view} onChange={changeView} order={uiPreferences.navigationOrder} hidden={uiPreferences.hiddenNavigation} />
        <main className="content-scroll" key={view}>
          <div
            className={`content content--${view}`}
            style={{
              '--view-scale': viewScale(uiPreferences, view) / 100,
              '--font-scale': fontScale(uiPreferences, view) / 100,
            } as CSSProperties}
          >
            {view === 'overview' && <OverviewView eventStream={eventStream} />}
            {view === 'subscriptions' && <SubscriptionsView />}
            {view === 'nodes' && <NodesView />}
            {view === 'pools' && <PoolsView />}
            {view === 'chains' && <ChainsView />}
            {view === 'channels' && <ChannelsView />}
            {view === 'rules' && <RulesView />}
            {view === 'traffic' && <TrafficPolicyView />}
            {view === 'dns' && <DnsView />}
            {view === 'connection' && <ConnectionView />}
            {view === 'links' && <LinksView />}
            {view === 'core' && <CoreView />}
            {view === 'settings' && <SettingsView preferences={uiPreferences} onChange={updateUIPreferences} />}
          </div>
        </main>
      </div>
    </div>
  )
}

function viewFromHash(hash: string): ViewName {
  const value = hash.replace(/^#/, '')
  return value === 'subscriptions' || value === 'nodes' || value === 'pools' || value === 'chains' || value === 'channels' || value === 'rules' || value === 'traffic' || value === 'connection' || value === 'links' || value === 'dns' || value === 'core' || value === 'settings'
    ? value
    : 'overview'
}

export default App
