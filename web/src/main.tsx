import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/app.css'
import './styles/shared.css'
import './styles/watch.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { Loading, ToastProvider } from './components/ui'
import Layout from './components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'
import Login from './pages/Login'
import Home from './pages/Home'
import Browse, { Leaving } from './pages/Browse'
import TitlePage from './pages/TitlePage'
import Watchlist from './pages/Watchlist'
import History from './pages/History'
import Stats from './pages/Stats'
import Upcoming from './pages/Upcoming'
import Discover, { DiscoverTitle, Requests } from './pages/Discover'
import Settings from './pages/Settings'

// the player carries hls.js: load it when something plays
const Play = React.lazy(() => import('./pages/Play'))

function Gate({ children }: { children: React.ReactNode }) {
  const { status, user } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  return <>{children}</>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login mode="login" />} />
      <Route path="/setup" element={<Login mode="setup" />} />
      <Route path="/settings/:tab?" element={<Gate><ErrorBoundary where="settings"><Settings /></ErrorBoundary></Gate>} />
      <Route path="/play/:id" element={<Gate><ErrorBoundary where="player"><React.Suspense fallback={<div className="player"><div className="player-spin"><Loading label="" /></div></div>}><Play /></React.Suspense></ErrorBoundary></Gate>} />
      <Route element={<Gate><ErrorBoundary where="watch"><Layout /></ErrorBoundary></Gate>}>
        <Route path="/" element={<Home />} />
        <Route path="/tonight" element={<Home />} />
        <Route path="/films" element={<Browse kind="movie" key="movie" />} />
        <Route path="/shows" element={<Browse kind="series" key="series" />} />
        <Route path="/title/:id" element={<TitlePage />} />
        <Route path="/person/:id" element={<TitlePage person />} />
        <Route path="/leaving" element={<Leaving />} />
        <Route path="/watchlist" element={<Watchlist />} />
        <Route path="/history" element={<History />} />
        <Route path="/stats" element={<Stats />} />
        <Route path="/upcoming" element={<Upcoming />} />
        <Route path="/discover" element={<Discover />} />
        <Route path="/discover/:type/:tmdb" element={<DiscoverTitle />} />
        <Route path="/requests" element={<Requests />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

// an older build may have registered a service worker: take it away
navigator.serviceWorker?.getRegistrations?.().then(rs => rs.forEach(r => r.unregister())).catch(() => {})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><App /></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
