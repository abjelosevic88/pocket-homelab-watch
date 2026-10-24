// Posters, wide cards, shelves and the little badges around them.
import React, { useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import type { Found, Request, Title } from '../lib/types'
import { hue, img, remaining, subline, titleLink } from '../lib/watch'
import { Icon } from './icons'

/** An image that fades in, with a coloured placeholder (initials) until it loads or when there is none. */
export function Img({ src, w, alt, className = '', round, icon }: { src?: string; w: number; alt: string; className?: string; round?: boolean; icon?: string }) {
  const [state, setState] = useState<'loading' | 'ok' | 'err'>(src ? 'loading' : 'err')
  const url = img(src, w)
  const initials = alt.split(/\s+/).filter(Boolean).slice(0, 2).map(x => x[0]).join('').toUpperCase()
  return (
    <span className={`art ${state === 'ok' ? 'loaded' : ''} ${round ? 'round' : ''} ${className}`} style={{ '--h': hue(alt) } as React.CSSProperties}>
      {state !== 'ok' && <span className="art-fallback">{icon ? <Icon name={icon} size={22} /> : <b>{initials}</b>}</span>}
      {url && state !== 'err' && <img src={url} alt="" loading="lazy" decoding="async" onLoad={() => setState('ok')} onError={() => setState('err')} />}
    </span>
  )
}

export function Progress({ value, className = '' }: { value: number; className?: string }) {
  if (!value || value <= 0) return null
  return <span className={`progress ${className}`}><span style={{ width: `${Math.min(100, Math.max(2, value))}%` }} /></span>
}

/** Little corner marks on a poster: watched, episodes left, leaving soon. */
function Marks({ t }: { t: Title }) {
  return <>
    {t.played && <span className="mark watched" title="Watched"><Icon name="check" size={12} strokeWidth={3} /></span>}
    {!t.played && t.type === 'series' && t.unplayed != null && t.unplayed > 0 && <span className="mark count" title={`${t.unplayed} episodes to watch`}>{t.unplayed}</span>}
    {t.leaving && <span className="mark leaving" title={`Leaving ${t.leaving}`}><Icon name="hourglass" size={11} />Leaving</span>}
  </>
}

/** A 2:3 poster linking to its page; a play button on hover for films. */
export function PosterCard({ t, sub }: { t: Title; sub?: React.ReactNode }) {
  const navigate = useNavigate()
  return (
    <Link to={titleLink(t)} className="tile" title={t.name}>
      <span className="tile-art poster">
        <Img src={t.poster} w={240} alt={t.name} icon={t.type === 'person' ? 'user' : undefined} />
        <Marks t={t} />
        {t.playable && <button type="button" className="play-fab" aria-label={`Play ${t.name}`} onClick={e => { e.preventDefault(); navigate(`/play/${t.id}`) }}><Icon name="play" size={18} /></button>}
        <Progress value={t.played ? 0 : t.progress || 0} className="on-art" />
      </span>
      <span className="tile-title ellipsis">{t.type === 'episode' ? t.series_name : t.name}</span>
      <span className="tile-sub ellipsis">{sub ?? subline(t)}</span>
    </Link>
  )
}

/** A 16:9 card that plays: continue watching, next up, new episodes. */
export function WideCard({ t, sub, action = 'play' }: { t: Title; sub?: React.ReactNode; action?: 'play' | 'open' }) {
  const to = action === 'play' && t.playable ? `/play/${t.id}` : titleLink(t)
  const head = t.type === 'episode' ? t.series_name : t.name
  return (
    <Link to={to} className="tile wide" title={t.name}>
      <span className="tile-art landscape">
        <Img src={t.thumb || t.backdrop} w={420} alt={head || t.name} />
        {t.logo && t.type !== 'episode' && <img className="wide-logo" src={img(t.logo, 220)} alt="" loading="lazy" />}
        <span className="wide-play"><Icon name="play" size={20} /></span>
        <Marks t={t} />
        <Progress value={t.played ? 0 : t.progress || 0} className="on-art" />
      </span>
      <span className="tile-title ellipsis">{head}</span>
      <span className="tile-sub ellipsis">{sub ?? (t.type === 'episode' ? <>{subline(t).split(' · ')[0]} · {t.name}</> : remaining(t) || subline(t))}</span>
    </Link>
  )
}

export function Shelf({ title, sub, to, children, wide }: { title: React.ReactNode; sub?: React.ReactNode; to?: string; children: React.ReactNode; wide?: boolean }) {
  const row = useRef<HTMLDivElement>(null)
  const scroll = (dir: number) => row.current?.scrollBy({ left: dir * row.current.clientWidth * 0.85, behavior: 'smooth' })
  return (
    <section className="shelf">
      <div className="shelf-head">
        <div className="grow"><h2>{to ? <Link to={to}>{title}</Link> : title}</h2>{sub && <p className="muted small">{sub}</p>}</div>
        <div className="shelf-arrows">
          <button type="button" className="btn ghost icon-only sm" aria-label="Scroll left" onClick={() => scroll(-1)}><Icon name="chevron-left" size={16} /></button>
          <button type="button" className="btn ghost icon-only sm" aria-label="Scroll right" onClick={() => scroll(1)}><Icon name="chevron-right" size={16} /></button>
        </div>
      </div>
      <div className={`shelf-row ${wide ? 'wide' : ''}`} ref={row}>{children}</div>
    </section>
  )
}

const STATUS: Record<string, [string, string]> = {
  available: ['In the library', 'ok'], partial: ['Partly here', 'ok'], processing: ['Downloading', 'info'], approved: ['Approved', 'info'],
  pending: ['Waiting for approval', 'warn'], declined: ['Declined', 'danger'], failed: ['Failed', 'danger'],
}
export function StatusBadge({ status, short }: { status: string; short?: boolean }) {
  const [label, tone] = STATUS[status] || ['', '']
  if (!label) return null
  return <span className={`badge st-${tone}`}>{short ? label.split(' ')[0] : label}</span>
}

/** A TMDB title from Jellyseerr: opens its page here (in the library) or the request page. */
export function FoundCard({ f, request }: { f: Found; request?: Request }) {
  const to = f.jellyfin_id && (f.status === 'available' || f.status === 'partial') ? `/title/${f.jellyfin_id}` : `/discover/${f.media_type}/${f.tmdb_id}`
  return (
    <Link to={to} className="tile" title={f.title}>
      <span className="tile-art poster">
        <Img src={f.poster} w={240} alt={f.title} />
        {(request?.status || f.status) && <span className="mark status"><StatusBadge status={request?.status || f.status} short /></span>}
        <span className="mark kind">{f.media_type === 'tv' ? 'Show' : 'Film'}</span>
      </span>
      <span className="tile-title ellipsis">{f.title}</span>
      <span className="tile-sub ellipsis">{[f.year, f.score ? `★ ${f.score}` : ''].filter(Boolean).join(' · ')}</span>
    </Link>
  )
}

/** The page's empty-state when Jellyfin isn't connected yet. */
export function NotConnected({ message }: { message: string }) {
  return (
    <div className="empty big">
      <span className="empty-ico"><Icon name="server" size={22} /></span>
      <h3>Connect your Jellyfin</h3>
      <p className="muted" style={{ maxWidth: 460 }}>{message}</p>
      <p className="muted small" style={{ maxWidth: 460 }}>Pocket Watch plays your films and shows with your own Jellyfin account, so resume points and watched marks stay yours — and every other Jellyfin app agrees.</p>
      <Link to="/settings/jellyfin" className="btn primary"><Icon name="settings" size={15} />Settings → Jellyfin</Link>
    </div>
  )
}
