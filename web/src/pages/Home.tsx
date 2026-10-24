import { Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Home as HomeT, Title } from '../lib/types'
import { V1, img, remaining, runtime, subline, epCode } from '../lib/watch'
import { Icon } from '../components/icons'
import { Img, NotConnected, PosterCard, Shelf, StatusBadge, WideCard } from '../components/cards'
import { openCreate } from '../components/Layout'
import { Loading, useAsync } from '../components/ui'

function greeting() {
  const h = new Date().getHours()
  return h < 5 ? 'Good night' : h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
}

export default function Home() {
  const { user } = useApp()
  const { data: h, error } = useAsync(() => api.get<HomeT>(`${V1}/home`), [])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} />{error}</div>
  if (!h) return <Loading />
  if (h.not_connected) return <NotConnected message={h.not_connected} />
  const first = (user?.name || '').split(' ')[0]
  const feature = h.continue?.[0] || h.picks?.[0]
  const resuming = !!h.continue?.[0]
  return (
    <div className="home">
      {feature && <Feature t={feature} resuming={resuming} />}
      <header className="home-hero">
        <div className="grow">
          <h1>{greeting()}{first ? `, ${first}` : ''}</h1>
          <p className="muted">{h.today && h.today.seconds > 60 ? <>{runtime(h.today.seconds)} watched today</> : h.next_up && h.next_up.length > 0 ? <>{h.next_up.length} {h.next_up.length === 1 ? 'show has' : 'shows have'} a next episode waiting</> : <>Pick something from your shelves</>}</p>
        </div>
        <button type="button" className="btn primary" onClick={() => openCreate('tonight')}><Icon name="popcorn" size={16} />What should we watch?</button>
      </header>

      {h.continue && h.continue.length > (resuming ? 1 : 0) && <Shelf title="Continue watching" wide>{h.continue.slice(resuming ? 1 : 0).map(t => <WideCard key={t.id} t={t} />)}</Shelf>}
      {h.next_up && h.next_up.length > 0 && <Shelf title="Next up" sub="The next episode of what you're watching" wide>{h.next_up.map(t => <WideCard key={t.id} t={t} />)}</Shelf>}
      {h.arrived && h.arrived.length > 0 && (
        <Shelf title="Just arrived" sub="What you asked for" to="/requests">
          {h.arrived.map(r => (
            <Link key={r.id} to={r.jellyfin_id ? `/title/${r.jellyfin_id}` : `/discover/${r.media_type}/${r.tmdb_id}`} className="tile">
              <span className="tile-art poster"><Img src={r.poster} w={240} alt={r.title} /><span className="mark status"><StatusBadge status="available" short /></span></span>
              <span className="tile-title ellipsis">{r.title}</span><span className="tile-sub">{r.year || ''}</span>
            </Link>
          ))}
        </Shelf>
      )}
      {h.latest_shows && h.latest_shows.length > 0 && <Shelf title="New episodes" to="/shows?sort=added">{h.latest_shows.map(t => <PosterCard key={t.id} t={t} sub={t.type === 'episode' ? epCode(t.season, t.episode) + ' · ' + t.name : t.unplayed ? `${t.unplayed} new` : subline(t)} />)}</Shelf>}
      {h.latest_movies && h.latest_movies.length > 0 && <Shelf title="Recently added films" to="/films?sort=added">{h.latest_movies.map(t => <PosterCard key={t.id} t={t} />)}</Shelf>}
      {h.watchlist && h.watchlist.length > 0 && (
        <Shelf title="On your watchlist" sub="And already in the library" to="/watchlist">
          {h.watchlist.map(w => (
            <Link key={w.id} to={`/title/${w.item_id}`} className="tile">
              <span className="tile-art poster"><Img src={w.poster} w={240} alt={w.title} /></span>
              <span className="tile-title ellipsis">{w.title}</span><span className="tile-sub">{[w.year, w.media_type === 'tv' ? 'Show' : 'Film'].filter(Boolean).join(' · ')}</span>
            </Link>
          ))}
        </Shelf>
      )}
      {h.because && h.because.items.length > 0 && <Shelf title={<>Because you watched <Link to={`/title/${h.because.id}`} className="accent-link">{h.because.title}</Link></>}>{h.because.items.map(t => <PosterCard key={t.id} t={t} />)}</Shelf>}
      {h.leaving && h.leaving.length > 0 && <Shelf title="Leaving soon" sub="Watch them before they're cleaned up" to="/leaving">{h.leaving.map(t => <PosterCard key={t.id} t={t} sub={`Leaves ${t.leaving}`} />)}</Shelf>}
      {h.picks && h.picks.length > 0 && <Shelf title="From your shelves" sub="Films you haven't seen, picked for today" to="/films?show=unwatched&sort=random">{h.picks.map(t => <PosterCard key={t.id} t={t} />)}</Shelf>}
      {h.sync && !h.sync.backfilled && <p className="muted small">Reading what you watched before from Jellyfin…</p>}
    </div>
  )
}

/** The big banner: what you were in the middle of, or today's pick. */
function Feature({ t, resuming }: { t: Title; resuming: boolean }) {
  const navigate = useNavigate()
  const name = t.type === 'episode' ? t.series_name || t.name : t.name
  const back = t.backdrop || t.thumb
  return (
    <section className="feature" style={back ? { backgroundImage: `url(${img(back, 1600)})` } : undefined}>
      <div className="feature-shade" />
      <div className="feature-body">
        <p className="eyebrow">{resuming ? 'Pick up where you left off' : 'Tonight, maybe'}</p>
        {t.logo ? <img className="feature-logo" src={img(t.logo, 500)} alt={name} /> : <h2 className="feature-title">{name}</h2>}
        <p className="feature-meta">{t.type === 'episode' ? <>{epCode(t.season, t.episode)} · {t.name}</> : subline(t)}{t.genres && t.genres.length > 0 && t.type !== 'episode' ? ` · ${t.genres.slice(0, 3).join(', ')}` : ''}</p>
        {t.overview && !resuming && <p className="feature-overview">{t.overview}</p>}
        <div className="row wrap">
          <button type="button" className="btn primary lg" onClick={() => navigate(`/play/${t.id}`)}><Icon name="play" size={16} />{resuming ? `Resume${remaining(t) ? ` · ${remaining(t)}` : ''}` : 'Play'}</button>
          <button type="button" className="btn glass lg" onClick={() => navigate(`/title/${t.type === 'episode' && t.series_id ? t.series_id : t.id}`)}><Icon name="info" size={16} />Details</button>
        </div>
        {resuming && t.progress ? <span className="feature-progress"><span style={{ width: `${t.progress}%` }} /></span> : null}
      </div>
    </section>
  )
}
