import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Watch } from '../lib/types'
import { V1, epCode, runtime } from '../lib/watch'
import { fmtDate, fmtISOTime } from '../lib/format'
import { Icon } from '../components/icons'
import { Img } from '../components/cards'
import { Empty, Loading, PageHeader, Segmented, errMsg, useAsync, useDebounce, useToast } from '../components/ui'

const SOURCE: Record<string, string> = { pocket: 'Pocket Watch', jellyfin: 'another Jellyfin app', backfill: 'from Jellyfin', import: 'imported' }

export default function History() {
  const { toast } = useToast()
  const [kind, setKind] = useState<'' | 'movie' | 'episode'>('')
  const [q, setQ] = useState('')
  const dq = useDebounce(q, 250)
  const [limit, setLimit] = useState(100)
  const { data, setData, error } = useAsync(() => api.get<{ items: Watch[]; total: number }>(`${V1}/history?${new URLSearchParams({ kind, q: dq, limit: String(limit) })}`), [kind, dq, limit])
  const days = useMemo(() => {
    const m = new Map<string, Watch[]>()
    for (const w of data?.items || []) m.set(w.day, [...(m.get(w.day) || []), w])
    return [...m.entries()]
  }, [data])
  const remove = async (w: Watch) => {
    try { await api.del(`${V1}/history/${w.id}`); if (data) setData({ ...data, items: data.items.filter(x => x.id !== w.id), total: data.total - 1 }) } catch (e) { toast(errMsg(e), 'err') }
  }
  return (
    <div>
      <PageHeader title="History" sub={data ? `${data.total.toLocaleString()} titles watched, here and in every other Jellyfin app` : ' '}
        actions={<Link to="/stats" className="btn sm"><Icon name="bar" size={14} />Stats</Link>} />
      <div className="toolbar">
        <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Find in your history" aria-label="Filter history" /></div>
        <Segmented size="sm" value={kind} onChange={setKind} options={[{ value: '', label: 'Everything' }, { value: 'movie', label: 'Films' }, { value: 'episode', label: 'Episodes' }]} />
      </div>
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!data ? <Loading /> : data.items.length === 0 ? <Empty title="Nothing watched yet" icon="history">What you watch here and in other Jellyfin apps shows up within a few minutes.</Empty> : (
        <div className="history">
          {days.map(([day, ws]) => (
            <section key={day} className="h-day">
              <h3 className="h-date">{fmtDate(day)} <span className="muted small">{runtime(ws.reduce((a, w) => a + w.seconds, 0))}</span></h3>
              {ws.map(w => (
                <div key={w.id} className="h-row">
                  <Link to={`/title/${w.item_id}`} className="h-art"><Img src={w.kind === 'episode' ? w.thumb : w.poster} w={w.kind === 'episode' ? 200 : 90} alt={w.series || w.title} /></Link>
                  <div className="grow h-main">
                    <Link to={`/title/${w.kind === 'episode' && w.series_id ? w.series_id : w.item_id}`} className="h-title ellipsis">{w.kind === 'episode' ? w.series : w.title}</Link>
                    <span className="muted small ellipsis">{w.kind === 'episode' ? <>{epCode(w.season, w.episode)} · {w.title}</> : w.year || ''}</span>
                  </div>
                  <span className="h-meta small muted">
                    {w.completed ? <span className="ok-text"><Icon name="check" size={12} strokeWidth={2.6} /> watched</span> : <>{w.runtime ? Math.round((w.position / w.runtime) * 100) : 0}% in</>}
                    <span title={w.device ? `${w.device} · ${SOURCE[w.source] || w.source}` : SOURCE[w.source] || w.source}>{w.source === 'backfill' ? 'earlier' : fmtISOTime(w.watched_at)}</span>
                  </span>
                  <button type="button" className="btn ghost icon-only xs h-del" aria-label="Remove from history" title="Remove from history (Jellyfin keeps its own)" onClick={() => remove(w)}><Icon name="x" size={14} /></button>
                </div>
              ))}
            </section>
          ))}
          {data.total > data.items.length && <div className="row mt"><button type="button" className="btn" onClick={() => setLimit(l => l + 200)}>Show more</button></div>}
        </div>
      )}
    </div>
  )
}
