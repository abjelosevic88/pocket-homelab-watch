// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Title } from '../lib/types'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { TonightModal } from './Tonight'
import { Avatar, type MenuItem, MenuButton, errMsg, modalOpen, useIsMobile, useMediaQuery, useToast } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pwt-sidebar'

/** The page's own name for the top bar breadcrumb (a film's or a show's name). */
const CrumbCtx = createContext<(t: string) => void>(() => {})
export function useCrumb(title: string | undefined) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || ''); return () => set('') }, [title, set])
}

export type CreateKind = 'tonight' | 'request' | 'random-film' | 'random-episode'
/** Open a create dialog from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pwt:create', { detail: k }))

export function createItems(onCreate: (k: CreateKind) => void, requests = true): MenuItem[] {
  return [
    { label: 'What should we watch?', icon: 'popcorn', hint: 'W', onSelect: () => onCreate('tonight') },
    { label: 'Surprise me: a film', icon: 'shuffle', onSelect: () => onCreate('random-film') },
    ...(requests ? ['sep' as const, { label: 'Request a film or show', icon: 'ticket', onSelect: () => onCreate('request') }] : []),
  ]
}

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
  const [sp, setSp] = useSearchParams()
  const { toast } = useToast()
  const { settings } = useApp()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState('')
  const setCrumb = useCallback((t: string) => setCrumbRaw(t), [])
  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [tonight, setTonight] = useState(false)

  const create = (k: CreateKind) => {
    closeDrawer()
    if (k === 'tonight') { setTonight(true); return }
    if (k === 'request') { navigate('/discover?find=1'); return }
    if (k === 'random-film') {
      api.get<{ items: Title[] }>(`${V1}/browse?type=movie&sort=random&show=unwatched&limit=1`)
        .then(r => r.items[0] ? navigate(`/title/${r.items[0].id}`) : toast('Nothing unwatched left — impressive', 'err'))
        .catch(e => toast(errMsg(e), 'err'))
    }
  }
  const createRef = useRef(create)
  createRef.current = create

  // /tonight (the PWA shortcut) opens "What should we watch?"
  useEffect(() => { if (loc.pathname === '/tonight') { setTonight(true); navigate('/', { replace: true }) } }, [loc.pathname, navigate])
  useEffect(() => { if (sp.get('do') === 'random') { sp.delete('do'); setSp(sp, { replace: true }); createRef.current('random-film') } }, [sp, setSp])

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => createRef.current((e as CustomEvent<CreateKind>).detail)
    const pal = () => setPalette(true)
    window.addEventListener('pwt:shortcuts', open)
    window.addEventListener('pwt:create', make)
    window.addEventListener('pwt:palette', pal)
    return () => { window.removeEventListener('pwt:shortcuts', open); window.removeEventListener('pwt:create', make); window.removeEventListener('pwt:palette', pal) }
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'w': e.preventDefault(); createRef.current('tonight'); break
        case 'h': go('/'); break
        case 'f': go('/films'); break
        case 's': go('/shows'); break
        case 'l': go('/watchlist'); break
        case 'u': if (settings?.upcoming) go('/upcoming'); break
        case 'd': if (settings?.requests) go('/discover'); break
        case 'r': if (settings?.requests) go('/requests'); break
        case 'y': go('/history'); break
        case 't': go('/stats'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onCreate={create} onNavigate={closeDrawer} />
        </div>
        <div className="main">
          <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
          <main className="content"><div className="content-inner"><Outlet /></div></main>
        </div>
        {palette && <CommandPalette onClose={() => setPalette(false)} onCreate={create} onShortcuts={() => setShortcuts(true)} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        {tonight && <TonightModal onClose={() => setTonight(false)} />}
      </div>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = {
  '': ['Home', '/'], films: ['Films', '/films'], shows: ['Shows', '/shows'], title: ['', ''], person: ['People', ''],
  watchlist: ['Watchlist', '/watchlist'], history: ['History', '/history'], stats: ['Stats', '/stats'], upcoming: ['Upcoming', '/upcoming'],
  discover: ['Discover', '/discover'], requests: ['Requests', '/requests'], leaving: ['Leaving soon', '/leaving'], genre: ['Genres', ''],
}

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pwt:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <>{section && sectionTo ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /></> : section ? <><span className="crumb static">{section}</span><Icon name="chevron-right" size={16} className="crumb-sep" /></> : null}<span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      {!mobile && <button type="button" className="top-search" onClick={onPalette}><Icon name="search" size={15} /><span>Search films and shows</span><kbd>/</kbd></button>}
      {mobile && <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>}
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
