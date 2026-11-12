import { useMemo } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Upcoming as U } from '../lib/types'
import { V1, epCode } from '../lib/watch'
import { fmtDate, fmtISOTime, todayISO, addDays } from '../lib/format'
import { Icon } from '../components/icons'
import { Img } from '../components/cards'
import { Empty, Loading, PageHeader, Segmented, useAsync } from '../components/ui'
import { useState } from 'react'

const RELEASE: Record<string, string> = { cinema: 'In cinemas', digital: 'Digital release', physical: 'On disc' }

// what isn't set up yet (sonarr / radarr), where an admin finishes it (Pocket Core's Downloads settings), and whom to ask
interface Setup { missing: string[]; setup_url?: string; admin?: string }

function SetupLink({ setup, className }: { setup: Setup; className?: string }) {
  return setup.setup_url
    ? <a href={setup.setup_url} className={className}>Finish Downloads setup →</a>
    : <Link to="/settings/services" className={className}>Connect them in Settings →</Link>
}

// one of the two is missing: a calm note above what the other one has
function PartlySetUp({ setup, admin }: { setup: Setup; admin: boolean }) {
  const films = setup.missing.includes('radarr')
  const what = films ? 'Films' : 'New episodes'
  const who = setup.admin || 'an admin'
  const body: ReactNode = admin
    ? <>{what} aren’t shown yet: the box’s {films ? 'film' : 'show'} downloads ({films ? 'Radarr' : 'Sonarr'}) aren’t set up. <SetupLink setup={setup} /></>
    : <>{what} will show here once {who} finishes setting up the box’s {films ? 'film' : 'show'} downloads.</>
  return <div className="callout info small"><Icon name="info" size={14} /><span>{body}</span></div>
}

export default function Upcoming() {
  const { settings, user } = useApp()
  const isAdmin = user?.role === 'admin'
  const [scope, setScope] = useState<'mine' | 'all'>(settings?.upcoming_scope || 'mine')
  const { data, error } = useAsync(() => api.get<{ items: U[]; configured: boolean; problems?: string[]; setup?: Setup }>(`${V1}/upcoming?days=28&scope=${scope}`), [scope])
  const days = useMemo(() => {
    const m = new Map<string, U[]>()
    for (const u of data?.items || []) m.set(u.date, [...(m.get(u.date) || []), u])
    return [...m.entries()]
  }, [data])
  const setup: Setup = data?.setup || { missing: [] }
  const today = todayISO()
  const label = (d: string) => d === today ? 'Today' : d === addDays(today, 1) ? 'Tomorrow' : d === addDays(today, -1) ? 'Yesterday' : fmtDate(d)
  return (
    <div>
      <PageHeader title="Upcoming" sub="New episodes and films on their way to the library, from Sonarr and Radarr."
        actions={<Segmented size="sm" value={scope} onChange={setScope} options={[{ value: 'mine', label: 'Mine' }, { value: 'all', label: 'Everything' }]} />} />
      {error && <div className="callout danger"><Icon name="alert" size={15} />{error}</div>}
      {data?.problems?.map(p => <div key={p} className="callout warn small"><Icon name="alert" size={14} />{p}</div>)}
      {data?.configured && setup.missing.length > 0 && <PartlySetUp setup={setup} admin={isAdmin} />}
      {!data ? <Loading /> : !data.configured ? (setup.missing.length > 0 ? (
        <Empty title="Not set up yet" icon="calendar">
          {isAdmin ? <>
            Upcoming needs the box’s film and show downloads (Radarr and Sonarr). Once they’re set up, new episodes and films on their way show here.
            <div style={{ marginTop: 14 }}><SetupLink setup={setup} className="btn primary sm" /></div>
          </> : <>Upcoming needs the box’s film and show downloads; ask {setup.admin || 'an admin'} to finish setting them up.</>}
        </Empty>
      ) : <Empty title="Not set up" icon="calendar">An admin connects Sonarr and Radarr under Settings → Services.</Empty>) : days.length === 0 ? (
        <Empty title="Nothing coming up" icon="calendar">{scope === 'mine' ? 'Nothing new for the shows you watch or the films you asked for in the next four weeks.' : 'Nothing in the next four weeks.'}</Empty>
      ) : (
        <div className="agenda">
          {days.map(([d, list]) => (
            <section key={d} className={`ag-day ${d < today ? 'past' : ''}`}>
              <h3 className="ag-date">{label(d)}<span className="muted small">{d === today || d === addDays(today, 1) ? fmtDate(d) : ''}</span></h3>
              {list.map(u => {
                const to = u.library_id ? `/title/${u.library_id}` : u.tmdb ? `/discover/${u.kind === 'movie' ? 'movie' : 'tv'}/${u.tmdb}` : ''
                const body = <>
                  <span className="ag-art"><Img src={u.poster} w={80} alt={u.series || u.title} /></span>
                  <span className="grow ag-main">
                    <b className="ellipsis">{u.series || u.title}</b>
                    <span className="muted small ellipsis">{u.kind === 'episode' ? <>{epCode(u.season, u.episode)} · {u.title}</> : RELEASE[u.release || ''] || 'Release'}</span>
                  </span>
                  <span className="ag-meta small">
                    {u.has_file ? <span className="badge st-ok">In the library</span> : u.kind === 'episode' ? <span className="muted">{fmtISOTime(u.at)}</span> : null}
                    {u.mine && scope === 'all' && <span className="badge accent-soft">Yours</span>}
                  </span>
                </>
                return to ? <Link key={u.kind + u.at + u.title + (u.series || '')} to={to} className="ag-row">{body}</Link> : <div key={u.kind + u.at + u.title} className="ag-row">{body}</div>
              })}
            </section>
          ))}
        </div>
      )}
    </div>
  )
}
