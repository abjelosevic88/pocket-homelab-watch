import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { WatchlistItem } from '../lib/types'
import { V1, subline } from '../lib/watch'
import { Icon } from '../components/icons'
import { Img, Progress, StatusBadge } from '../components/cards'
import { Empty, Loading, PageHeader, Segmented, errMsg, useAsync, useToast } from '../components/ui'
import { useState } from 'react'

export default function Watchlist() {
  const { settings } = useApp()
  const { toast } = useToast()
  const { data, setData, error } = useAsync(() => api.get<WatchlistItem[]>(`${V1}/watchlist`), [])
  const [show, setShow] = useState<'all' | 'here' | 'missing'>('all')
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!data) return <Loading />
  const remove = async (w: WatchlistItem) => {
    try { await api.del(`${V1}/watchlist/${w.id}`); setData(data.filter(x => x.id !== w.id)) } catch (e) { toast(errMsg(e), 'err') }
  }
  const list = data.filter(w => show === 'all' || (show === 'here' ? !!w.item_id : !w.item_id))
  return (
    <div>
      <PageHeader title="Watchlist" sub="What you mean to watch — in the library already, or one click from a request."
        actions={<Segmented size="sm" value={show} onChange={setShow} options={[{ value: 'all', label: `All ${data.length}` }, { value: 'here', label: 'In the library' }, { value: 'missing', label: 'Not here yet' }]} />} />
      {list.length === 0 ? (
        <Empty title="Nothing on your watchlist" icon="bookmark">Use <Icon name="bookmark-plus" size={13} /> on a film or show{settings?.requests ? <>, or find something new in <Link to="/discover">Discover</Link></> : ''}.</Empty>
      ) : (
        <div className="wl-grid">
          {list.map(w => {
            const to = w.item_id ? `/title/${w.item_id}` : `/discover/${w.media_type}/${w.tmdb_id}`
            const t = w.library
            return (
              <div key={w.id} className="wl-card">
                <Link to={to} className="wl-art"><Img src={t?.poster || w.poster} w={200} alt={w.title} />{t && !t.played && <Progress value={t.progress || 0} className="on-art" />}</Link>
                <div className="wl-body">
                  <Link to={to} className="wl-title">{w.title}</Link>
                  <span className="muted small">{t ? subline(t) : [w.year, w.media_type === 'tv' ? 'Show' : 'Film'].filter(Boolean).join(' · ')}</span>
                  <span className="row wrap" style={{ gap: 6 }}>
                    {w.item_id ? <span className="badge st-ok">In the library</span> : w.request ? <StatusBadge status={w.request} /> : <span className="badge">Not in the library</span>}
                    {t?.played && <span className="badge">Watched</span>}
                  </span>
                  {w.note && <p className="small muted">{w.note}</p>}
                  <div className="row" style={{ gap: 6, marginTop: 'auto' }}>
                    {t?.playable && <Link className="btn primary sm" to={`/play/${t.id}`}><Icon name="play" size={13} />Play</Link>}
                    {!w.item_id && !w.request && settings?.requests && <Link className="btn sm" to={to}><Icon name="ticket" size={13} />Request</Link>}
                    <button type="button" className="btn ghost sm" onClick={() => remove(w)}><Icon name="x" size={13} />Remove</button>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
