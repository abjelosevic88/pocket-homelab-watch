// Films and Shows: the whole shelf, filtered and sorted, loading more as you scroll.
import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { Title } from '../lib/types'
import { V1 } from '../lib/watch'
import { Icon } from '../components/icons'
import { NotConnected, PosterCard } from '../components/cards'
import { Select } from '../components/Select'
import { Empty, Loading, PageHeader, Segmented, errMsg, useAsync, useDebounce } from '../components/ui'
import { ApiError } from '../lib/api'

const SORTS = [
  { value: 'added', label: 'Recently added' }, { value: 'title', label: 'Title' }, { value: 'year', label: 'Release year' },
  { value: 'score', label: 'Rating' }, { value: 'critics', label: 'Critics' }, { value: 'runtime', label: 'Length' }, { value: 'played', label: 'Last watched' }, { value: 'random', label: 'Shuffle' },
]

export default function Browse({ kind }: { kind: 'movie' | 'series' }) {
  const [sp, setSp] = useSearchParams()
  const sort = sp.get('sort') || 'added'
  const show = sp.get('show') || 'all'
  const genre = sp.get('genre') || ''
  const decade = sp.get('decade') || ''
  const [q, setQ] = useState(sp.get('q') || '')
  const dq = useDebounce(q, 250)
  const set = (k: string, v: string) => { const n = new URLSearchParams(sp); if (v && v !== 'all') n.set(k, v); else n.delete(k); setSp(n, { replace: true }) }
  useEffect(() => { set('q', dq.trim()) }, [dq]) // eslint-disable-line react-hooks/exhaustive-deps
  const genres = useAsync(() => api.get<string[]>(`${V1}/genres?type=${kind}`), [kind])
  const [items, setItems] = useState<Title[]>([])
  const [total, setTotal] = useState(-1)
  const [err, setErr] = useState<{ msg: string; nc: boolean } | null>(null)
  const [busy, setBusy] = useState(false)
  const key = `${kind}|${sort}|${show}|${genre}|${decade}|${dq}`
  const live = useRef(key)
  const load = async (start: number) => {
    setBusy(true)
    const k = key
    try {
      const p = new URLSearchParams({ type: kind, sort, show, start: String(start), limit: '60' })
      if (genre) p.set('genre', genre)
      if (decade) p.set('decade', decade)
      if (dq.trim()) p.set('q', dq.trim())
      const r = await api.get<{ items: Title[]; total: number }>(`${V1}/browse?${p}`)
      if (live.current !== k) return
      setItems(prev => start === 0 ? r.items : [...prev, ...r.items])
      setTotal(r.total)
      setErr(null)
    } catch (e) {
      setErr({ msg: errMsg(e), nc: e instanceof ApiError && e.data.code === 'not_connected' })
    } finally { if (live.current === k) setBusy(false) }
  }
  useEffect(() => { live.current = key; setItems([]); setTotal(-1); load(0) }, [key]) // eslint-disable-line react-hooks/exhaustive-deps
  const more = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = more.current
    if (!el) return
    const io = new IntersectionObserver(es => { if (es[0].isIntersecting && !busy && items.length < total) load(items.length) }, { rootMargin: '600px' })
    io.observe(el)
    return () => io.disconnect()
  }) // eslint-disable-line react-hooks/exhaustive-deps
  if (err?.nc) return <NotConnected message={err.msg} />
  const decades = Array.from({ length: 11 }, (_, i) => 2020 - i * 10)
  return (
    <div>
      <PageHeader title={kind === 'movie' ? 'Films' : 'Shows'} sub={total >= 0 ? `${total.toLocaleString()} ${kind === 'movie' ? (total === 1 ? 'film' : 'films') : (total === 1 ? 'show' : 'shows')}` : ' '} />
      <div className="toolbar">
        <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder={kind === 'movie' ? 'Find a film' : 'Find a show'} aria-label="Filter by title" /></div>
        <Segmented size="sm" value={show} onChange={v => set('show', v)} options={[{ value: 'all', label: 'All' }, { value: 'unwatched', label: 'Unwatched' }, ...(kind === 'movie' ? [{ value: 'resumable', label: 'Started' }] : []), { value: 'watched', label: 'Watched' }, { value: 'favorites', label: 'Favourites' }]} />
        <span className="spacer" />
        <Select ariaLabel="Genre" value={genre} onChange={v => set('genre', v)} options={[{ value: '', label: 'Every genre' }, ...(genres.data || []).map(g => ({ value: g, label: g }))]} />
        <Select ariaLabel="Decade" value={decade} onChange={v => set('decade', v)} options={[{ value: '', label: 'Any year' }, ...decades.map(d => ({ value: String(d), label: `${d}s` }))]} />
        <Select ariaLabel="Sort" value={sort} onChange={v => set('sort', v)} options={SORTS.filter(s => kind === 'movie' || s.value !== 'runtime')} />
      </div>
      {err && <div className="callout danger"><Icon name="alert" size={15} />{err.msg}</div>}
      {total === 0 && !busy ? <Empty title="Nothing here" icon={kind === 'movie' ? 'film' : 'tv'}>No {kind === 'movie' ? 'films' : 'shows'} match these filters.</Empty> : (
        <div className="grid-posters">{items.map(t => <PosterCard key={t.id} t={t} />)}</div>
      )}
      <div ref={more} />
      {busy && <Loading />}
    </div>
  )
}

export function Leaving() {
  const { data, error } = useAsync(() => api.get<Title[]>(`${V1}/leaving`), [])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  return (
    <div>
      <PageHeader title="Leaving soon" sub="Maintainerr will clean these up on the day shown." />
      {data.length === 0 ? <Empty title="Nothing is leaving" icon="hourglass" /> : <div className="grid-posters">{data.map(t => <PosterCard key={t.id} t={t} sub={`Leaves ${t.leaving}`} />)}</div>}
    </div>
  )
}
