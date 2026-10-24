import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Stats as StatsT } from '../lib/types'
import { V1, longDuration } from '../lib/watch'
import { Icon } from '../components/icons'
import { BarList, Columns } from '../components/charts'
import { Img } from '../components/cards'
import { Card, Empty, Loading, PageHeader, Segmented, useAsync } from '../components/ui'

const hours = (s: number) => `${(s / 3600).toFixed(s >= 36000 ? 0 : 1)} h`
const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

export default function Stats() {
  const year = new Date().getFullYear()
  const [range, setRange] = useState('30d')
  const { data: s, error } = useAsync(() => api.get<StatsT>(`${V1}/stats?range=${range}`), [range])
  return (
    <div>
      <PageHeader title="Stats" sub="What you watched, from your history here and in every other Jellyfin app."
        actions={<Segmented size="sm" value={range} onChange={setRange} options={[{ value: '7d', label: 'Week' }, { value: '30d', label: 'Month' }, { value: '90d', label: '3 months' }, { value: String(year), label: String(year) }, { value: 'all', label: 'All time' }]} />} />
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {!s ? <Loading /> : s.totals.titles === 0 ? <Empty title="Nothing watched in this period" icon="bar" /> : <>
        <div className="stat-tiles">
          <div className="stat-tile"><span className="stat-label">Time watched</span><span className="stat-value">{longDuration(s.totals.seconds)}</span><span className="stat-delta">{s.days_active} {s.days_active === 1 ? 'day' : 'days'} with something on</span></div>
          <div className="stat-tile"><span className="stat-label">Films</span><span className="stat-value">{s.totals.movies}</span><span className="stat-delta">{longDuration(s.totals.movie_seconds)}</span></div>
          <div className="stat-tile"><span className="stat-label">Episodes</span><span className="stat-value">{s.totals.episodes}</span><span className="stat-delta">{longDuration(s.totals.episode_seconds)}</span></div>
          <div className="stat-tile"><span className="stat-label">Streak</span><span className="stat-value">{s.streak.current} {s.streak.current === 1 ? 'day' : 'days'}</span><span className="stat-delta">longest {s.streak.longest}</span></div>
        </div>
        <Card title="Hours watched" className="mt">
          <Columns ariaLabel="Hours watched over time" data={s.series.map(p => ({ key: p.key, label: p.label, value: p.seconds / 3600, detail: longDuration(p.seconds) }))} format={n => `${+n.toFixed(1)} h`} labelEvery={s.series.length > 16 ? Math.ceil(s.series.length / 12) : 1} />
        </Card>
        <div className="two-col mt">
          <Card title="Top shows">
            {s.top_shows.length === 0 ? <p className="muted small">No episodes in this period.</p> : (
              <ol className="top-list">
                {s.top_shows.map((r, i) => (
                  <li key={r.key || r.name}>
                    <span className="top-n">{i + 1}</span>
                    <Link to={`/title/${r.key}`} className="top-art"><Img src={r.image} w={80} alt={r.name} /></Link>
                    <Link to={`/title/${r.key}`} className="grow ellipsis top-name">{r.name}</Link>
                    <span className="muted small nowrap">{r.count} ep · {hours(r.seconds)}</span>
                  </li>
                ))}
              </ol>
            )}
          </Card>
          <Card title="Genres"><BarList data={s.top_genres.map(g => ({ key: g.key, label: g.name, value: g.seconds / 3600, detail: `${hours(g.seconds)} · ${g.count} titles` }))} format={n => `${+n.toFixed(1)} h`} /></Card>
        </div>
        <div className="two-col mt">
          <Card title="By weekday"><Columns ariaLabel="Hours watched by weekday" height={150} data={s.weekday.map((v, i) => ({ key: WEEKDAYS[i], label: WEEKDAYS[i], value: v / 3600, detail: longDuration(v) }))} format={n => `${+n.toFixed(1)} h`} /></Card>
          <Card title="By hour of the day"><Columns ariaLabel="Hours watched by hour of the day" height={150} labelEvery={3} data={s.hours.map((v, i) => ({ key: String(i), label: String(i), value: v / 3600, detail: `${i}:00 · ${longDuration(v)}` }))} format={n => `${+n.toFixed(1)} h`} /></Card>
        </div>
      </>}
    </div>
  )
}
