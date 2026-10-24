// Discover: what's trending and coming, search TMDB, and ask for what isn't here — through Jellyseerr, as you.
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { Found, FoundDetail, Request } from '../lib/types'
import { V1, img, runtime } from '../lib/watch'
import { fmtDate } from '../lib/format'
import { Icon } from '../components/icons'
import { FoundCard, Img, Shelf, StatusBadge } from '../components/cards'
import { useCrumb } from '../components/Layout'
import { Empty, Loading, Modal, PageHeader, Spinner, errMsg, useAsync, useDebounce, useToast } from '../components/ui'

const SECTIONS: [string, string][] = [['trending', 'Trending this week'], ['movies', 'Popular films'], ['tv', 'Popular shows'], ['upcoming-movies', 'Coming to cinemas'], ['upcoming-tv', 'New seasons and shows']]

export default function Discover() {
  const [sp, setSp] = useSearchParams()
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q, 350)
  useEffect(() => { const n = new URLSearchParams(sp); n.delete('find'); if (dq.trim()) n.set('q', dq.trim()); else n.delete('q'); setSp(n, { replace: true }) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const reqs = useAsync(() => api.get<{ items: Request[] }>(`${V1}/requests`), [])
  const mine = new Map((reqs.data?.items || []).map(r => [`${r.media_type}:${r.tmdb_id}`, r]))
  return (
    <div>
      <PageHeader title="Discover" sub="Find films and shows that aren't in the library yet and ask for them." actions={<Link to="/requests" className="btn sm"><Icon name="ticket" size={14} />My requests</Link>} />
      <div className="search-box big"><Icon name="search" size={17} /><input autoFocus={sp.get('find') === '1'} value={q} onChange={e => setQ(e.target.value)} placeholder="Search every film and show (TMDB)" aria-label="Search TMDB" /></div>
      {dq.trim() ? <SearchResults q={dq.trim()} mine={mine} /> : SECTIONS.map(([k, label]) => <Section key={k} section={k} title={label} mine={mine} />)}
    </div>
  )
}

function Section({ section, title, mine }: { section: string; title: string; mine: Map<string, Request> }) {
  const { data, error } = useAsync(() => api.get<{ items: Found[] }>(`${V1}/discover?section=${section}`), [section])
  if (error) return <div className="callout danger mt"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <div className="shelf"><Loading /></div>
  return <Shelf title={title}>{data.items.map(f => <FoundCard key={f.media_type + f.tmdb_id} f={f} request={mine.get(`${f.media_type}:${f.tmdb_id}`)} />)}</Shelf>
}

function SearchResults({ q, mine }: { q: string; mine: Map<string, Request> }) {
  const [page, setPage] = useState(1)
  const [items, setItems] = useState<Found[]>([])
  const [pages, setPages] = useState(1)
  const [err, setErr] = useState('')
  useEffect(() => { setItems([]); setPage(1) }, [q])
  useEffect(() => {
    let live = true
    api.get<{ items: Found[]; pages: number }>(`${V1}/discover?q=${encodeURIComponent(q)}&page=${page}`).then(r => { if (!live) return; setItems(p => page === 1 ? r.items : [...p, ...r.items]); setPages(r.pages); setErr('') }).catch(e => setErr(errMsg(e)))
    return () => { live = false }
  }, [q, page])
  if (err) return <div className="callout danger mt"><Icon name="alert" size={15} />{err}</div>
  return (
    <div className="mt">
      {items.length === 0 ? <Empty title="Searching…" icon="search" /> : <div className="grid-posters">{items.map(f => <FoundCard key={f.media_type + f.tmdb_id} f={f} request={mine.get(`${f.media_type}:${f.tmdb_id}`)} />)}</div>}
      {page < pages && <div className="row mt"><button type="button" className="btn" onClick={() => setPage(p => p + 1)}>More results</button></div>}
    </div>
  )
}

/** One film or show from TMDB: what it is, whether it's here, and the request button. */
export function DiscoverTitle() {
  const { type = 'movie', tmdb = '' } = useParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { data: d, error, reload } = useAsync(() => api.get<FoundDetail>(`${V1}/discover/${type}/${tmdb}`), [type, tmdb])
  const [asking, setAsking] = useState(false)
  const [onList, setOnList] = useState(false)
  useCrumb(d?.title.title)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!d) return <Loading />
  const f = d.title
  const here = f.jellyfin_id && (f.status === 'available' || f.status === 'partial')
  const addToList = async () => {
    try { await api.post(`${V1}/watchlist`, { media_type: f.media_type, tmdb_id: f.tmdb_id, title: f.title, year: f.year, poster: f.poster }); setOnList(true); toast('On your watchlist') } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div className="title-page">
      <div className="backdrop" style={f.backdrop ? { backgroundImage: `url(${f.backdrop})` } : undefined}><div className="backdrop-shade" /></div>
      <div className="title-hero">
        <span className="title-poster"><Img src={f.poster} w={320} alt={f.title} /></span>
        <div className="title-text">
          <p className="eyebrow">{f.media_type === 'tv' ? 'Show' : 'Film'}{d.request ? <> · <StatusBadge status={d.request.status} /></> : f.status ? <> · <StatusBadge status={f.status} /></> : null}</p>
          <h1 className="title-name">{f.title}</h1>
          <p className="title-meta">{[f.year, runtime((d.runtime || 0) * 60), f.media_type === 'tv' && d.seasons.length ? `${d.seasons.length} seasons` : ''].filter(Boolean).map((m, i) => <span key={i}>{m}</span>)}{f.score ? <span className="score"><Icon name="star" size={13} />{f.score}</span> : null}</p>
          {d.genres.length > 0 && <p className="title-genres">{d.genres.map(g => <span key={g} className="chip sm">{g}</span>)}</p>}
          {d.tagline && <p className="tagline">{d.tagline}</p>}
          <div className="title-actions">
            {here ? <button type="button" className="btn primary lg" onClick={() => navigate(`/title/${f.jellyfin_id}`)}><Icon name="play" size={16} />Watch it here</button>
              : d.request && d.request.status !== 'declined' && d.request.status !== 'failed' ? <span className="callout info small"><Icon name="ticket" size={14} />You asked for it {fmtDate(d.request.created_at.slice(0, 10))} — {d.request.status === 'pending' ? 'waiting for approval' : 'on its way'}. You'll hear when it arrives.</span>
              : <button type="button" className="btn primary lg" onClick={() => setAsking(true)}><Icon name="ticket" size={16} />Request</button>}
            {!here && <button type="button" className={`btn lg ${onList ? 'on' : ''}`} onClick={addToList} disabled={onList}><Icon name={onList ? 'bookmark-filled' : 'bookmark-plus'} size={16} />{onList ? 'On your watchlist' : 'Watchlist'}</button>}
            {d.trailer && <a className="btn lg" href={d.trailer} target="_blank" rel="noreferrer"><Icon name="video" size={16} />Trailer</a>}
            <a className="btn lg icon-only" href={d.seerr_url} target="_blank" rel="noreferrer" title="Open in Jellyseerr" aria-label="Open in Jellyseerr"><Icon name="external" size={16} /></a>
          </div>
          {f.overview && <p className="overview">{f.overview}</p>}
          {f.media_type === 'tv' && d.seasons.length > 0 && (
            <div className="seasons-mini">{d.seasons.map(s => <span key={s.number} className={`chip sm ${s.status ? 'st-' + s.status : ''}`} title={s.status || 'not requested'}>{s.name || `Season ${s.number}`} · {s.episodes} ep{s.status === 'available' ? ' ✓' : ''}</span>)}</div>
          )}
        </div>
      </div>
      {d.cast.length > 0 && (
        <Shelf title="Cast">
          {d.cast.map(p => <div key={p.name + p.role} className="tile person static"><span className="tile-art round"><Img src={p.photo} w={160} alt={p.name} round /></span><span className="tile-title ellipsis">{p.name}</span><span className="tile-sub ellipsis">{p.role}</span></div>)}
        </Shelf>
      )}
      {asking && <RequestModal d={d} onClose={() => setAsking(false)} onDone={() => { setAsking(false); reload() }} />}
    </div>
  )
}

function RequestModal({ d, onClose, onDone }: { d: FoundDetail; onClose: () => void; onDone: () => void }) {
  const { toast } = useToast()
  const f = d.title
  const open = d.seasons.filter(s => s.status !== 'available' && s.status !== 'processing' && s.status !== 'pending')
  const [pick, setPick] = useState<Set<number>>(() => new Set(open.map(s => s.number)))
  const [busy, setBusy] = useState(false)
  const send = async () => {
    setBusy(true)
    try {
      const all = f.media_type === 'tv' && pick.size === open.length && open.length === d.seasons.length
      const r = await api.post<Request>(`${V1}/requests`, { media_type: f.media_type, tmdb_id: f.tmdb_id, seasons: f.media_type === 'tv' && !all ? [...pick] : [] })
      toast(r.status === 'pending' ? 'Requested — waiting for approval' : 'Requested — it is on its way')
      onDone()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Request ${f.title}`} onClose={onClose} footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className="btn primary" onClick={send} disabled={busy || (f.media_type === 'tv' && pick.size === 0)}>{busy && <Spinner />}Request</button></>}>
      <div className="row" style={{ gap: 14, alignItems: 'flex-start' }}>
        <span style={{ width: 90, flexShrink: 0 }}><Img src={f.poster ? img(f.poster, 180) : ''} w={180} alt={f.title} /></span>
        <div className="stack">
          <p style={{ margin: 0 }}>Jellyseerr gets the request in your name. Depending on the household's rules it is approved right away or waits for an admin; you'll get a push when it is ready to watch.</p>
          {f.media_type === 'tv' && (
            <div className="stack" style={{ gap: 4 }}>
              <b className="small">Seasons</b>
              {d.seasons.map(s => {
                const done = !open.includes(s)
                return (
                  <label key={s.number} className={`check-row ${done ? 'muted' : ''}`}>
                    <input type="checkbox" disabled={done} checked={done || pick.has(s.number)} onChange={e => { const n = new Set(pick); if (e.target.checked) n.add(s.number); else n.delete(s.number); setPick(n) }} />
                    {s.name || `Season ${s.number}`} <span className="muted small">· {s.episodes} episodes{s.status ? ` · ${s.status}` : ''}</span>
                  </label>
                )
              })}
            </div>
          )}
        </div>
      </div>
    </Modal>
  )
}

export function Requests() {
  const { toast } = useToast()
  const { data, error, reload } = useAsync(() => api.get<{ items: Request[]; configured: boolean; seerr_url?: string }>(`${V1}/requests`), [])
  const cancel = async (r: Request) => {
    try { await api.del(`${V1}/requests/${r.seerr_id}`); toast('Request withdrawn'); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div>
      <PageHeader title="Requests" sub="What you asked for, here or in Jellyseerr. You get a push when something arrives."
        actions={<><Link to="/discover?find=1" className="btn primary sm"><Icon name="plus" size={14} />Request something</Link>{data?.seerr_url && <a className="btn sm" href={data.seerr_url} target="_blank" rel="noreferrer"><Icon name="external" size={14} />Jellyseerr</a>}</>} />
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <Loading /> : !data.configured ? <Empty title="Requests are not set up" icon="ticket">An admin connects Jellyseerr under Settings → Services.</Empty> : data.items.length === 0 ? <Empty title="No requests yet" icon="ticket"><Link to="/discover">Find something</Link> that isn't in the library.</Empty> : (
        <div className="req-list">
          {data.items.map(r => (
            <div key={r.id} className="req-row">
              <Link to={r.jellyfin_id && r.status === 'available' ? `/title/${r.jellyfin_id}` : `/discover/${r.media_type}/${r.tmdb_id}`} className="req-art"><Img src={r.poster} w={90} alt={r.title} /></Link>
              <div className="grow">
                <Link to={r.jellyfin_id && r.status === 'available' ? `/title/${r.jellyfin_id}` : `/discover/${r.media_type}/${r.tmdb_id}`} className="req-title">{r.title || `TMDB ${r.tmdb_id}`}</Link>
                <span className="muted small">{[r.year, r.media_type === 'tv' ? (r.seasons ? `Seasons ${r.seasons}` : 'Show') : 'Film', `asked ${fmtDate(r.created_at.slice(0, 10))}`].filter(Boolean).join(' · ')}</span>
              </div>
              <StatusBadge status={r.status} />
              {r.status === 'available' && r.jellyfin_id && <Link to={`/title/${r.jellyfin_id}`} className="btn sm primary"><Icon name="play" size={13} />Watch</Link>}
              {r.status === 'pending' && <button type="button" className="btn ghost sm" onClick={() => cancel(r)}>Withdraw</button>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
