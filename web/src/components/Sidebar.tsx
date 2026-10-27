// Left column: create button, library, yours, discover — the Pocket Calendar sidebar.
import { type ReactNode, useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { type Anchor, Menu, rectOf } from './ui'

type NavItem = { to: string; label: string; icon: string; end?: boolean; hide?: boolean }

export function Sidebar({ onCreate, onNavigate }: { onCreate: (k: CreateKind) => void; onNavigate: () => void }) {
  const { status, settings } = useApp()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Watch'
  const LIBRARY: NavItem[] = [
    { to: '/films', label: 'Films', icon: 'film' },
    { to: '/shows', label: 'Shows', icon: 'tv' },
  ]
  const YOURS: NavItem[] = [
    { to: '/watchlist', label: 'Watchlist', icon: 'bookmark' },
    { to: '/history', label: 'History', icon: 'history' },
    { to: '/stats', label: 'Stats', icon: 'bar' },
  ]
  const DISCOVER: NavItem[] = [
    { to: '/upcoming', label: 'Upcoming', icon: 'calendar', hide: settings ? !settings.upcoming : false },
    { to: '/discover', label: 'Discover', icon: 'compass', hide: settings ? !settings.requests : false },
    { to: '/requests', label: 'Requests', icon: 'ticket', hide: settings ? !settings.requests : false },
  ]
  return (
    <aside className="sidebar" aria-label="Watch">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Watch' ? title : <>Pocket <b>Watch</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={17} strokeWidth={2.2} />New<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate, !!settings?.requests)} onClose={() => setMenu(null)} width={260} />}

      <nav className="sb-nav" aria-label="Sections">
        <NavLink to="/" end onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}><Icon name="home" size={16} />Home</NavLink>
      </nav>
      <Group title="Library"><Nav items={LIBRARY} onNavigate={onNavigate} /></Group>
      <Group title="Yours"><Nav items={YOURS} onNavigate={onNavigate} /></Group>
      {DISCOVER.some(n => !n.hide) && <Group title="Discover"><Nav items={DISCOVER.filter(n => !n.hide)} onNavigate={onNavigate} /></Group>}

      <div className="sb-tonight">
        <button type="button" className="tonight-btn" onClick={() => onCreate('tonight')}>
          <span className="tonight-ico"><Icon name="popcorn" size={18} /></span>
          <span className="grow"><b>What should we watch?</b><span>Tell it the mood, get picks</span></span>
        </button>
      </div>
      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pwt:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

function Nav({ items, onNavigate }: { items: NavItem[]; onNavigate: () => void }) {
  return (
    <nav className="sb-nav tight">
      {items.map(n => (
        <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
          <Icon name={n.icon} size={16} />{n.label}
        </NavLink>
      ))}
    </nav>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  const key = `pwt-sb-${title}`
  const [open, setOpen] = useState(() => localStorage.getItem(key) !== '0')
  const toggle = () => setOpen(o => { localStorage.setItem(key, o ? '0' : '1'); return !o })
  return (
    <section className="sb-sec">
      <h4 className="sb-h">
        <button type="button" className="sb-h-btn" aria-expanded={open} onClick={toggle}><Icon name={open ? 'chevron-down' : 'chevron-right'} size={13} />{title}</button>
      </h4>
      {open && children}
    </section>
  )
}
