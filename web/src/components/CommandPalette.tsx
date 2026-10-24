// ⌘K / "/" — search the library (films, shows, episodes, people), titles to request, or run a command.
import React, { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Found, Title } from '../lib/types'
import { V1, epCode, subline } from '../lib/watch'
import { Icon } from './icons'
import { Img } from './cards'
import type { CreateKind } from './Layout'
import { useDebounce } from './ui'

interface Results { movies: Title[]; series: Title[]; episodes: Title[]; people: Title[] }

export function CommandPalette({ onClose, onCreate, onShortcuts }: { onClose: () => void; onCreate: (k: CreateKind) => void; onShortcuts: () => void }) {
  const navigate = useNavigate()
  const { settings } = useApp()
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 140)
  const [hi, setHi] = useState(0)
  const [res, setRes] = useState<Results | null>(null)
  const [found, setFound] = useState<Found[]>([])
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!dq.trim()) { setRes(null); setFound([]); return }
    let live = true
    api.get<Results>(`${V1}/search?q=${encodeURIComponent(dq)}`).then(r => { if (live) setRes(r) }).catch(() => {})
    if (settings?.requests && dq.trim().length > 2) api.get<{ items: Found[] }>(`${V1}/discover?q=${encodeURIComponent(dq)}`).then(r => { if (live) setFound(r.items.filter(f => f.status !== 'available' && f.status !== 'partial').slice(0, 5)) }).catch(() => {})
    return () => { live = false }
  }, [dq, settings?.requests])

  type Row = { key: string; group: string; icon: React.ReactNode; label: React.ReactNode; sub?: React.ReactNode; hint?: string; run: () => void; alt?: () => void }
  const commands: Row[] = useMemo(() => [
    { key: 'tonight', group: 'Commands', icon: <Icon name="popcorn" size={16} />, label: 'What should we watch?', hint: 'W', run: () => onCreate('tonight') },
    { key: 'random', group: 'Commands', icon: <Icon name="shuffle" size={16} />, label: 'Surprise me: a film I haven’t seen', run: () => onCreate('random-film') },
    ...(settings?.requests ? [{ key: 'request', group: 'Commands', icon: <Icon name="ticket" size={16} />, label: 'Request a film or show', run: () => onCreate('request') }] : []),
    { key: 'home', group: 'Go to', icon: <Icon name="home" size={16} />, label: 'Home', hint: 'H', run: () => navigate('/') },
    { key: 'films', group: 'Go to', icon: <Icon name="film" size={16} />, label: 'Films', hint: 'F', run: () => navigate('/films') },
    { key: 'shows', group: 'Go to', icon: <Icon name="tv" size={16} />, label: 'Shows', hint: 'S', run: () => navigate('/shows') },
    { key: 'watchlist', group: 'Go to', icon: <Icon name="bookmark" size={16} />, label: 'Watchlist', hint: 'L', run: () => navigate('/watchlist') },
    { key: 'history', group: 'Go to', icon: <Icon name="history" size={16} />, label: 'History', hint: 'Y', run: () => navigate('/history') },
    { key: 'stats', group: 'Go to', icon: <Icon name="bar" size={16} />, label: 'Stats', hint: 'T', run: () => navigate('/stats') },
    ...(settings?.upcoming ? [{ key: 'upcoming', group: 'Go to', icon: <Icon name="calendar" size={16} />, label: 'Upcoming', hint: 'U', run: () => navigate('/upcoming') }] : []),
    ...(settings?.requests ? [
      { key: 'discover', group: 'Go to', icon: <Icon name="compass" size={16} />, label: 'Discover', hint: 'D', run: () => navigate('/discover') },
      { key: 'requests', group: 'Go to', icon: <Icon name="ticket" size={16} />, label: 'Requests', hint: 'R', run: () => navigate('/requests') },
    ] : []),
    { key: 'settings', group: 'Go to', icon: <Icon name="settings" size={16} />, label: 'Settings', hint: ',', run: () => navigate('/settings') },
    { key: 'jellyfin', group: 'Go to', icon: <Icon name="server" size={16} />, label: 'Settings: Jellyfin account', run: () => navigate('/settings/jellyfin') },
    { key: 'keys', group: 'Go to', icon: <Icon name="keyboard" size={16} />, label: 'Keyboard shortcuts', hint: '?', run: onShortcuts },
  ], [navigate, onCreate, onShortcuts, settings?.requests, settings?.upcoming])

  const rows = useMemo<Row[]>(() => {
    const n = q.trim().toLowerCase()
    const words = n.split(/\s+/).filter(Boolean)
    const hit = (text: string) => words.every(w => text.toLowerCase().includes(w))
    const cmds = commands.filter(c => !n || hit(`${c.label} ${c.group}`))
    if (!n) return cmds
    const out: Row[] = []
    const art = (t: Title, src?: string) => <Img src={src || t.poster} w={64} alt={t.name} className="pal-art" icon={t.type === 'person' ? 'user' : undefined} />
    if (res) {
      out.push(...res.movies.map<Row>(t => ({ key: 'm' + t.id, group: 'Films', icon: art(t), label: t.name, sub: subline(t), hint: '↵', run: () => navigate(`/title/${t.id}`), alt: () => navigate(`/play/${t.id}`) })))
      out.push(...res.series.map<Row>(t => ({ key: 's' + t.id, group: 'Shows', icon: art(t), label: t.name, sub: subline(t), run: () => navigate(`/title/${t.id}`) })))
      out.push(...res.episodes.map<Row>(t => ({ key: 'e' + t.id, group: 'Episodes', icon: art(t), label: t.name, sub: `${t.series_name} · ${epCode(t.season, t.episode)}`, run: () => navigate(`/title/${t.id}`), alt: () => navigate(`/play/${t.id}`) })))
      out.push(...res.people.map<Row>(t => ({ key: 'p' + t.id, group: 'People', icon: art(t), label: t.name, sub: 'Cast & crew', run: () => navigate(`/person/${t.id}`) })))
    }
    out.push(...found.map<Row>(f => ({ key: 'f' + f.media_type + f.tmdb_id, group: 'Not in the library', icon: <Img src={f.poster} w={64} alt={f.title} className="pal-art" />, label: f.title, sub: `${f.media_type === 'tv' ? 'Show' : 'Film'}${f.year ? ' · ' + f.year : ''}${f.status ? ' · ' + f.status : ' · request it'}`, run: () => navigate(`/discover/${f.media_type}/${f.tmdb_id}`) })))
    out.push(...cmds.slice(0, 5))
    return out
  }, [q, res, found, commands, navigate])
  useEffect(() => setHi(0), [q, res])
  useEffect(() => { list.current?.querySelector(`[data-i="${hi}"]`)?.scrollIntoView({ block: 'nearest' }) }, [hi])

  const run = (r: Row | undefined, alt = false) => { if (!r) return; onClose(); (alt && r.alt ? r.alt : r.run)() }
  let last = ''
  return createPortal(
    <div className="palette-backdrop" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
      <div className="palette-box" role="dialog" aria-label="Search and commands">
        <div className="pal-input">
          <Icon name="search" size={18} />
          <input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Films, shows, episodes, people, or a command…" aria-label="Search"
            onKeyDown={e => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setHi(h => Math.min(rows.length - 1, h + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setHi(h => Math.max(0, h - 1)) }
              else if (e.key === 'Enter') { e.preventDefault(); run(rows[hi], e.shiftKey) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
            }} />
          <kbd>esc</kbd>
        </div>
        <div className="pal-list" ref={list} role="listbox">
          {rows.length === 0 && <div className="pal-empty">{q.trim() && !res ? 'Searching…' : 'Nothing matches.'}</div>}
          {rows.map((r, i) => {
            const head = r.group !== last ? <div className="pal-group" key={'g' + r.group}>{r.group}</div> : null
            last = r.group
            return (
              <React.Fragment key={r.key}>
                {head}
                <div data-i={i} role="option" aria-selected={i === hi} className={`pal-row ${i === hi ? 'hi' : ''}`} onMouseEnter={() => setHi(i)} onClick={() => run(r)}>
                  <span className="pal-ico">{r.icon}</span>
                  <span className="grow"><span className="pal-label ellipsis">{r.label}</span>{r.sub && <span className="pal-sub ellipsis">{r.sub}</span>}</span>
                  {r.hint && <kbd>{r.hint}</kbd>}
                  {i === hi && <Icon name="chevron-right" size={14} className="muted" />}
                </div>
              </React.Fragment>
            )
          })}
        </div>
        {q.trim() && <div className="pal-foot muted xsmall"><kbd>↵</kbd> open · <kbd>⇧</kbd><kbd>↵</kbd> play a film or episode now</div>}
      </div>
    </div>, document.body)
}
