// One film, show, season, episode or person: the backdrop, what to press, seasons and episodes, cast, more like it.
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, ApiError } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Title } from '../lib/types'
import { V1, epCode, img, remaining, runtime } from '../lib/watch'
import { fmtDate } from '../lib/format'
import { Icon } from '../components/icons'
import { Img, NotConnected, PosterCard, Progress, Shelf } from '../components/cards'
import { useCrumb } from '../components/Layout'
import { Loading, MenuButton, errMsg, useToast } from '../components/ui'

interface Detail { title: Title; seasons?: Title[]; episodes?: Title[]; next?: Title; similar?: Title[]; titles?: Title[]; watchlist_id?: number }

export default function TitlePage({ person }: { person?: boolean }) {
  const { id = '' } = useParams()
  const [d, setD] = useState<Detail | null>(null)
  const [err, setErr] = useState<{ msg: string; nc: boolean } | null>(null)
  const load = () => api.get<Detail>(`${V1}/titles/${id}`).then(x => { setD(x); setErr(null) }).catch(e => setErr({ msg: errMsg(e), nc: e instanceof ApiError && e.data.code === 'not_connected' }))
  useEffect(() => { setD(null); load() }, [id]) // eslint-disable-line react-hooks/exhaustive-deps
  useCrumb(d?.title.type === 'episode' ? `${d.title.series_name} · ${epCode(d.title.season, d.title.episode)}` : d?.title.name)
  if (err?.nc) return <NotConnected message={err.msg} />
  if (err) return <div className="callout danger"><Icon name="alert" size={15} />{err.msg}</div>
  if (!d) return <Loading />
  if (person || d.title.type === 'person') return <PersonPage d={d} />
  return <TitleView d={d} reload={load} setD={setD} />
}

function TitleView({ d, reload, setD }: { d: Detail; reload: () => void; setD: (d: Detail) => void }) {
  const t = d.title
  const navigate = useNavigate()
  const { settings } = useApp()
  const { toast } = useToast()
  const isShow = t.type === 'series' || t.type === 'season'
  const mark = async (id: string, played: boolean) => {
    try { await api.post(`${V1}/titles/${id}/played`, { played }); toast(played ? 'Marked as watched' : 'Marked as not watched'); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const fav = async () => {
    try { await api.post(`${V1}/titles/${t.id}/favorite`, { favorite: !t.favorite }); setD({ ...d, title: { ...t, favorite: !t.favorite } }) } catch (e) { toast(errMsg(e), 'err') }
  }
  const watchlist = async () => {
    try {
      if (d.watchlist_id) { await api.del(`${V1}/watchlist/${d.watchlist_id}`); setD({ ...d, watchlist_id: undefined }); toast('Taken off your watchlist') }
      else { const w = await api.post<{ id: number }>(`${V1}/watchlist`, { item_id: t.id }); setD({ ...d, watchlist_id: w.id }); toast('On your watchlist') }
    } catch (e) { toast(errMsg(e), 'err') }
  }
  const jf = settings?.jellyfin_public_url
  const playTarget = t.playable ? t : d.next
  const playLabel = t.playable
    ? (t.position && !t.played ? `Resume · ${remaining(t)}` : t.played ? 'Watch again' : 'Play')
    : d.next ? `${d.next.position ? 'Continue' : t.played ? 'Watch again' : t.unplayed === t.child_count ? 'Start' : 'Next'}: ${epCode(d.next.season, d.next.episode)}` : ''
  const meta = [
    t.type === 'episode' ? epCode(t.season, t.episode, t.episode_end) : '',
    t.year ? (t.type === 'series' && t.end_year && t.end_year !== t.year ? `${t.year}–${t.end_year}` : t.type === 'series' && t.status === 'Continuing' ? `${t.year}–` : String(t.year)) : '',
    t.rating || '', runtime(t.runtime), t.type === 'series' && t.child_count ? `${t.child_count} episodes` : '',
  ].filter(Boolean)
  return (
    <div className="title-page">
      <div className="backdrop" style={t.backdrop ? { backgroundImage: `url(${img(t.backdrop, 1920)})` } : undefined}><div className="backdrop-shade" /></div>
      <div className="title-hero">
        <span className="title-poster"><Img src={t.poster} w={320} alt={t.type === 'episode' ? t.series_name || t.name : t.name} /></span>
        <div className="title-text">
          {t.type === 'episode' && t.series_id && <Link to={`/title/${t.series_id}`} className="eyebrow link">{t.series_name}<Icon name="chevron-right" size={12} /></Link>}
          {t.type === 'season' && t.series_id && <Link to={`/title/${t.series_id}`} className="eyebrow link">{t.series_name}<Icon name="chevron-right" size={12} /></Link>}
          {t.logo && t.type !== 'episode' && t.type !== 'season' ? <img className="title-logo" src={img(t.logo, 560)} alt={t.name} /> : <h1 className="title-name">{t.name}</h1>}
          {t.original && <p className="muted small" style={{ margin: 0 }}>{t.original}</p>}
          <p className="title-meta">
            {meta.map((m, i) => <span key={i}>{m}</span>)}
            {t.score ? <span className="score"><Icon name="star" size={13} />{t.score}</span> : null}
            {t.critics ? <span className="critics" title="Critics">🍅 {t.critics}%</span> : null}
          </p>
          {t.genres && t.genres.length > 0 && <p className="title-genres">{t.genres.map(g => <Link key={g} to={`${t.type === 'movie' ? '/films' : '/shows'}?genre=${encodeURIComponent(g)}`} className="chip sm">{g}</Link>)}</p>}
          {t.tagline && <p className="tagline">{t.tagline}</p>}
          {t.leaving && <p className="callout warn small"><Icon name="hourglass" size={14} />Leaving the library on {fmtDate(t.leaving)}.</p>}
          <div className="title-actions">
            {playTarget && <button type="button" className="btn primary lg" onClick={() => navigate(`/play/${playTarget.id}`)}><Icon name="play" size={16} />{playLabel}</button>}
            {(t.playable || isShow) && <button type="button" className={`btn lg round-ish ${t.played ? 'on' : ''}`} title={t.played ? 'Watched — mark as not watched' : 'Mark as watched'} aria-label="Watched" onClick={() => mark(t.id, !t.played)}><Icon name={t.played ? 'check-circle' : 'check'} size={17} /><span className="hide-mobile">{t.played ? 'Watched' : 'Mark watched'}</span></button>}
            {(t.type === 'movie' || t.type === 'series') && <button type="button" className={`btn lg icon-only ${d.watchlist_id ? 'on' : ''}`} title={d.watchlist_id ? 'On your watchlist' : 'Add to your watchlist'} aria-label="Watchlist" onClick={watchlist}><Icon name={d.watchlist_id ? 'bookmark-filled' : 'bookmark-plus'} size={17} /></button>}
            <button type="button" className={`btn lg icon-only ${t.favorite ? 'on fav' : ''}`} title={t.favorite ? 'A favourite' : 'Make it a favourite'} aria-label="Favourite" onClick={fav}><Icon name={t.favorite ? 'heart-filled' : 'heart'} size={17} /></button>
            <MenuButton label="More" className="btn lg icon-only" items={() => [
              ...(t.trailer ? [{ label: 'Watch the trailer', icon: 'video', onSelect: () => window.open(t.trailer, '_blank') }] : []),
              ...(jf ? [{ label: 'Open in Jellyfin', icon: 'external', onSelect: () => window.open(`${jf}/web/#/details?id=${t.id}`, '_blank') }] : []),
              ...(t.imdb ? [{ label: 'IMDb', icon: 'external', onSelect: () => window.open(`https://www.imdb.com/title/${t.imdb}/`, '_blank') }] : []),
              ...(t.tmdb ? [{ label: 'TMDB', icon: 'external', onSelect: () => window.open(`https://www.themoviedb.org/${t.type === 'movie' ? 'movie' : 'tv'}/${t.tmdb}`, '_blank') }] : []),
            ]}><Icon name="more" size={17} /></MenuButton>
          </div>
          {t.playable && !t.played && t.progress ? <div className="title-progress"><Progress value={t.progress} /><span className="muted small">{remaining(t)}</span></div> : null}
          {t.overview && <p className="overview">{t.overview}</p>}
          <dl className="facts">
            {t.studio && <><dt>{t.type === 'series' ? 'Network' : 'Studio'}</dt><dd>{t.studio}</dd></>}
            {t.premiere && <><dt>{t.type === 'episode' ? 'Aired' : 'Released'}</dt><dd>{fmtDate(t.premiere)}</dd></>}
            {directors(t).length > 0 && <><dt>Directed by</dt><dd>{directors(t).join(', ')}</dd></>}
            {t.video && <><dt>Video</dt><dd>{t.video}</dd></>}
            {t.audio && <><dt>Audio</dt><dd>{t.audio}</dd></>}
          </dl>
        </div>
      </div>

      {t.type === 'series' && d.seasons && <Seasons show={t} seasons={d.seasons} mark={mark} />}
      {(t.type === 'season' || t.type === 'episode') && d.episodes && (
        <section className="section">
          <h2 className="section-h">{t.type === 'season' ? t.name : `Season ${t.season}`}</h2>
          <Episodes list={d.episodes} current={t.type === 'episode' ? t.id : undefined} mark={mark} />
        </section>
      )}
      {t.people && t.people.filter(p => p.kind === 'Actor').length > 0 && (
        <Shelf title="Cast">
          {t.people.filter(p => p.kind === 'Actor').map(p => (
            <Link key={p.id + p.role} to={`/person/${p.id}`} className="tile person">
              <span className="tile-art round"><Img src={p.photo} w={160} alt={p.name} round /></span>
              <span className="tile-title ellipsis">{p.name}</span><span className="tile-sub ellipsis">{p.role}</span>
            </Link>
          ))}
        </Shelf>
      )}
      {d.similar && d.similar.length > 0 && <Shelf title="More like this">{d.similar.map(s => <PosterCard key={s.id} t={s} />)}</Shelf>}
    </div>
  )
}

function directors(t: Title) { return (t.people || []).filter(p => p.kind === 'Director').map(p => p.name) }

function Seasons({ show, seasons, mark }: { show: Title; seasons: Title[]; mark: (id: string, played: boolean) => void }) {
  const [sp, setSp] = useSearchParams()
  const pick = sp.get('season')
  const firstUnwatched = seasons.find(s => !s.played && (s.season ?? 0) > 0) || seasons[0]
  const cur = seasons.find(s => s.id === pick) || firstUnwatched
  const [eps, setEps] = useState<Title[] | null>(null)
  const load = () => { if (cur) api.get<Title[]>(`${V1}/titles/${show.id}/episodes?season=${cur.id}`).then(setEps).catch(() => setEps([])) }
  useEffect(() => { setEps(null); load() }, [cur?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!cur) return null
  return (
    <section className="section">
      <div className="season-tabs" role="tablist">
        {seasons.map(s => (
          <button key={s.id} type="button" role="tab" aria-selected={s.id === cur.id} className={`season-tab ${s.id === cur.id ? 'active' : ''}`}
            onClick={() => { const n = new URLSearchParams(sp); n.set('season', s.id); setSp(n, { replace: true }) }}>
            {s.name}{s.played && <Icon name="check" size={12} strokeWidth={2.6} />}
          </button>
        ))}
        <span className="spacer" />
        <button type="button" className="btn ghost sm" onClick={() => { mark(cur.id, !cur.played); setTimeout(load, 400) }}><Icon name="check" size={14} />{cur.played ? 'Season not watched' : 'Season watched'}</button>
      </div>
      {cur.overview && <p className="muted season-overview">{cur.overview}</p>}
      {eps ? <Episodes list={eps} mark={(id, p) => { mark(id, p); setEps(eps.map(e => e.id === id ? { ...e, played: p, progress: 0 } : e)) }} /> : <Loading />}
    </section>
  )
}

function Episodes({ list, current, mark }: { list: Title[]; current?: string; mark: (id: string, played: boolean) => void }) {
  const navigate = useNavigate()
  return (
    <ol className="episodes">
      {list.map(e => (
        <li key={e.id} className={`episode ${e.missing ? 'missing' : ''} ${e.id === current ? 'current' : ''} ${e.played ? 'played' : ''}`}>
          <button type="button" className="ep-art" disabled={!e.playable} onClick={() => navigate(`/play/${e.id}`)} aria-label={`Play ${e.name}`}>
            <Img src={e.thumb} w={300} alt={e.name} icon="tv" />
            {e.playable && <span className="ep-play"><Icon name="play" size={18} /></span>}
            <Progress value={e.played ? 0 : e.progress || 0} className="on-art" />
          </button>
          <div className="ep-body">
            <div className="ep-head">
              <Link to={`/title/${e.id}`} className="ep-title"><span className="ep-n">{e.episode ?? ''}{e.episode_end && e.episode_end !== e.episode ? `–${e.episode_end}` : ''}.</span> {e.name}</Link>
              <span className="muted small nowrap">{[runtime(e.runtime), e.premiere ? fmtDate(e.premiere, false) : ''].filter(Boolean).join(' · ')}</span>
            </div>
            {e.overview && <p className="ep-overview">{e.overview}</p>}
            {e.missing && <p className="muted small">Not in the library{e.premiere && e.premiere > new Date().toISOString().slice(0, 10) ? ` — airs ${fmtDate(e.premiere)}` : ''}</p>}
          </div>
          {!e.missing && <button type="button" className={`btn ghost icon-only ep-check ${e.played ? 'on' : ''}`} title={e.played ? 'Watched — mark as not watched' : 'Mark as watched'} aria-label="Watched" onClick={() => mark(e.id, !e.played)}><Icon name={e.played ? 'check-circle' : 'circle'} size={18} /></button>}
        </li>
      ))}
    </ol>
  )
}

function PersonPage({ d }: { d: Detail }) {
  const t = d.title
  return (
    <div>
      <div className="person-hero">
        <span className="person-photo"><Img src={t.poster} w={240} alt={t.name} round icon="user" /></span>
        <div>
          <p className="eyebrow">Cast & crew</p>
          <h1 className="title-name">{t.name}</h1>
          {t.overview && <p className="overview clamp">{t.overview}</p>}
        </div>
      </div>
      <h2 className="section-h">In your library</h2>
      <div className="grid-posters">{(d.titles || []).map(x => <PosterCard key={x.id} t={x} />)}</div>
    </div>
  )
}
