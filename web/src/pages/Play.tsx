// The player: the whole window for the picture, controls that get out of the way.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import Hls from 'hls.js'
import { api } from '../lib/api'
import { useApp } from '../lib/app-context'
import { detectCaps } from '../lib/caps'
import type { PlayInfo, StreamInfo } from '../lib/types'
import { V1, clock, deviceName, epCode, img } from '../lib/watch'
import { Icon } from '../components/icons'
import { Spinner, errMsg } from '../components/ui'

const QUALITIES = [{ v: 'auto', l: 'Best' }, { v: '20', l: '20 Mbps' }, { v: '10', l: '10 Mbps' }, { v: '8', l: '8 Mbps (1080p)' }, { v: '4', l: '4 Mbps (720p)' }, { v: '2', l: '2 Mbps' }, { v: '1', l: '1 Mbps' }]

export default function Play() {
  const { id = '' } = useParams()
  const loc = useLocation()
  const navigate = useNavigate()
  const { settings } = useApp()
  const video = useRef<HTMLVideoElement>(null)
  const hls = useRef<Hls | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const [info, setInfo] = useState<PlayInfo | null>(null)
  const [err, setErr] = useState('')
  const [playing, setPlaying] = useState(false)
  const [waiting, setWaiting] = useState(true)
  const [time, setTime] = useState(0)
  const [buffered, setBuffered] = useState(0)
  const [duration, setDuration] = useState(0)
  const [volume, setVolume] = useState(() => Number(localStorage.getItem('pwt-volume') ?? '1'))
  const [muted, setMuted] = useState(false)
  const [ui, setUi] = useState(true)
  const [menu, setMenu] = useState<'' | 'tracks' | 'quality' | 'chapters'>('')
  const [sub, setSub] = useState(-1)
  const [quality, setQuality] = useState<string>(() => localStorage.getItem('pwt-quality') || '')
  const [hover, setHover] = useState<{ x: number; t: number } | null>(null)
  const [fs, setFs] = useState(false)
  const [nextIn, setNextIn] = useState<number | null>(null)
  const [nextDismissed, setNextDismissed] = useState(false)
  const session = useRef('')
  const lastReport = useRef(0)
  const started = useRef(false)
  const triedTranscode = useRef(false)
  const skipped = useRef<Set<number>>(new Set())
  const t0 = Number(new URLSearchParams(loc.search).get('t') ?? '-1')

  // ---- loading ----
  const open = useCallback(async (opts: { start: number; audio?: number; subtitle?: number; quality?: string; transcode?: boolean }) => {
    setErr('')
    setWaiting(true)
    try {
      const p = await api.post<PlayInfo>(`${V1}/play/${id}`, {
        start: opts.start, audio: opts.audio, subtitle: opts.subtitle, quality: opts.quality || quality || undefined, caps: detectCaps(),
        device: deviceName(), replace: session.current || undefined, transcode: opts.transcode,
      })
      session.current = p.session
      started.current = false
      setInfo(p)
      setSub(p.burned ? -1 : p.subtitle_index)
      attach(p, opts.start >= 0 ? opts.start : p.start)
    } catch (e) { setErr(errMsg(e)); setWaiting(false) }
  }, [id, quality]) // eslint-disable-line react-hooks/exhaustive-deps

  const attach = (p: PlayInfo, start: number) => {
    const v = video.current
    if (!v) return
    hls.current?.destroy()
    hls.current = null
    v.removeAttribute('src')
    const caps = detectCaps()
    if (p.hls && !caps.native_hls && Hls.isSupported()) {
      const h = new Hls({ startPosition: start, maxBufferLength: 40, maxMaxBufferLength: 120, backBufferLength: 60, xhrSetup: x => { x.withCredentials = true } })
      h.on(Hls.Events.ERROR, (_e, data) => {
        if (!data.fatal) return
        if (data.type === Hls.ErrorTypes.MEDIA_ERROR) { h.recoverMediaError(); return }
        setErr(`Playback failed: ${data.details}`)
      })
      h.loadSource(p.url)
      h.attachMedia(v)
      hls.current = h
    } else {
      v.src = p.url
      const seek = () => { if (start > 0) v.currentTime = start; v.removeEventListener('loadedmetadata', seek) }
      v.addEventListener('loadedmetadata', seek)
    }
    v.volume = volume
    v.play().catch(() => setWaiting(false))
  }

  useEffect(() => {
    triedTranscode.current = false
    skipped.current = new Set()
    setNextIn(null); setNextDismissed(false); setInfo(null); session.current = ''
    open({ start: t0 })
    return () => { report('stop'); hls.current?.destroy(); hls.current = null }
  }, [id]) // eslint-disable-line react-hooks/exhaustive-deps

  // ---- reporting to the server (and Jellyfin) ----
  const report = (event: string) => {
    const v = video.current
    if (!session.current || !v) return
    const body = JSON.stringify({ session: session.current, event, position: v.currentTime || 0, paused: v.paused })
    if (event === 'stop') {
      fetch(`${V1}/play/${id}/report`, { method: 'POST', body, keepalive: true, credentials: 'same-origin', headers: { 'Content-Type': 'application/json' } }).catch(() => {})
      return
    }
    api.post(`${V1}/play/${id}/report`, JSON.parse(body)).catch(() => {})
  }
  useEffect(() => {
    const h = () => report('stop')
    window.addEventListener('pagehide', h)
    return () => window.removeEventListener('pagehide', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  // ---- video events ----
  useEffect(() => {
    const v = video.current
    if (!v) return
    const on = (ev: string, fn: () => void) => { v.addEventListener(ev, fn); return () => v.removeEventListener(ev, fn) }
    const offs = [
      on('playing', () => { setPlaying(true); setWaiting(false); if (!started.current) { started.current = true; report('start') } else report('unpause') }),
      on('pause', () => { setPlaying(false); report('pause') }),
      on('waiting', () => setWaiting(true)),
      on('canplay', () => setWaiting(false)),
      on('durationchange', () => setDuration(isFinite(v.duration) ? v.duration : 0)),
      on('volumechange', () => { setMuted(v.muted); setVolume(v.volume); localStorage.setItem('pwt-volume', String(v.volume)) }),
      on('timeupdate', () => {
        setTime(v.currentTime)
        if (v.buffered.length) setBuffered(v.buffered.end(v.buffered.length - 1))
        if (Date.now() - lastReport.current > 10000 && !v.paused) { lastReport.current = Date.now(); report('progress') }
      }),
      on('ended', () => { report('stop'); session.current = '' }),
      on('error', () => {
        if (info?.method === 'DirectPlay' && !triedTranscode.current) {
          triedTranscode.current = true
          open({ start: v.currentTime || info.start, transcode: true })
        } else setErr('This browser can’t play the stream.')
      }),
    ]
    return () => offs.forEach(f => f())
  }) // eslint-disable-line react-hooks/exhaustive-deps

  // subtitles: show the chosen text track
  useEffect(() => {
    const v = video.current
    if (!v || !info) return
    const apply = () => { for (const tr of Array.from(v.textTracks)) tr.mode = tr.id === `s${sub}` ? 'showing' : 'disabled' }
    apply()
    const t = setTimeout(apply, 300)
    v.textTracks.addEventListener?.('addtrack', apply)
    return () => { clearTimeout(t); v.textTracks.removeEventListener?.('addtrack', apply) }
  }, [sub, info])

  // ---- the controls hide while watching ----
  const idle = useRef<number>(0)
  const poke = useCallback(() => {
    setUi(true)
    window.clearTimeout(idle.current)
    idle.current = window.setTimeout(() => { if (!video.current?.paused) { setUi(false); setMenu('') } }, 3000)
  }, [])
  useEffect(() => { poke(); return () => window.clearTimeout(idle.current) }, [poke])
  useEffect(() => { if (!playing) setUi(true); else poke() }, [playing, poke])

  // ---- actions ----
  const toggle = () => { const v = video.current; if (!v) return; if (v.paused) v.play().catch(() => {}); else v.pause() }
  const seek = (to: number) => { const v = video.current; if (!v) return; v.currentTime = Math.max(0, Math.min(to, (duration || info?.runtime || 0) - 1)); poke() }
  const back = () => { if (document.fullscreenElement) { document.exitFullscreen().catch(() => {}); return } report('stop'); session.current = ''; if (window.history.length > 1) navigate(-1); else navigate(`/title/${id}`) }
  const goNext = () => { if (!info?.next) return; report('stop'); session.current = ''; navigate(`/play/${info.next.id}`, { replace: true }) }
  const fullscreen = () => { if (document.fullscreenElement) document.exitFullscreen().catch(() => {}); else (box.current?.requestFullscreen?.() ?? (video.current as unknown as { webkitEnterFullscreen?: () => void })?.webkitEnterFullscreen?.()) }
  useEffect(() => { const h = () => setFs(!!document.fullscreenElement); document.addEventListener('fullscreenchange', h); return () => document.removeEventListener('fullscreenchange', h) }, [])
  const pickSub = (s: StreamInfo | null) => {
    setMenu('')
    if (!info) return
    if (s && !s.text) { open({ start: video.current?.currentTime || 0, subtitle: s.index, audio: info.audio_index }); return }
    if (info.burned) { open({ start: video.current?.currentTime || 0, subtitle: s ? s.index : -1, audio: info.audio_index }); return }
    setSub(s ? s.index : -1)
  }
  const pickAudio = (a: StreamInfo) => { setMenu(''); if (info && a.index !== info.audio_index) open({ start: video.current?.currentTime || 0, audio: a.index, subtitle: info.burned ? info.subtitle_index : undefined }) }
  const pickQuality = (q: string) => { setMenu(''); setQuality(q); localStorage.setItem('pwt-quality', q); open({ start: video.current?.currentTime || 0, quality: q, audio: info?.audio_index }) }

  // ---- segments: skip intro, next episode ----
  const seg = info?.segments.find(s => time >= s.start && time < s.end - 1 && (s.type === 'intro' || s.type === 'recap' || s.type === 'preview' || s.type === 'commercial'))
  const skipMode = settings?.skip_intro || 'button'
  useEffect(() => {
    if (seg && skipMode === 'auto' && !skipped.current.has(seg.start)) { skipped.current.add(seg.start); seek(seg.end) }
  }, [seg?.start, skipMode]) // eslint-disable-line react-hooks/exhaustive-deps
  const rt = duration || info?.runtime || 0
  const outro = info?.segments.find(s => s.type === 'outro' && s.end >= rt - 30)
  const nearEnd = !!info?.next && rt > 0 && (outro ? time >= outro.start : time >= rt - 25) && !nextDismissed
  useEffect(() => {
    if (!nearEnd) { setNextIn(null); return }
    if (!settings?.autoplay_next) { setNextIn(-1); return }
    setNextIn(n => (n === null || n < 0 ? 10 : n))
    const t = window.setInterval(() => setNextIn(n => (n !== null && n > 0 ? n - 1 : n)), 1000)
    return () => window.clearInterval(t)
  }, [nearEnd, settings?.autoplay_next])
  useEffect(() => { if (nextIn === 0 && playing) goNext() }, [nextIn]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { const v = video.current; if (!v) return; const h = () => { if (info?.next && settings?.autoplay_next && !nextDismissed) goNext() }; v.addEventListener('ended', h); return () => v.removeEventListener('ended', h) })

  // ---- keys and the lock screen ----
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest('input, textarea, select')) return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      const v = video.current
      if (!v) return
      switch (e.key) {
        case ' ': case 'k': e.preventDefault(); toggle(); break
        case 'ArrowLeft': case 'j': e.preventDefault(); seek(v.currentTime - 10); break
        case 'ArrowRight': case 'l': e.preventDefault(); seek(v.currentTime + (e.key === 'l' ? 10 : 10)); break
        case 'ArrowUp': e.preventDefault(); v.volume = Math.min(1, v.volume + 0.1); poke(); break
        case 'ArrowDown': e.preventDefault(); v.volume = Math.max(0, v.volume - 0.1); poke(); break
        case 'm': v.muted = !v.muted; break
        case 'f': fullscreen(); break
        case 'c': if (info) { const first = info.subtitles.find(s => s.text); setSub(sub >= 0 ? -1 : (info.subtitle_index >= 0 ? info.subtitle_index : first?.index ?? -1)) } break
        case 'N': goNext(); break
        case 's': if (seg) seek(seg.end); break
        case 'Escape': if (menu) setMenu(''); else back(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  })
  useEffect(() => {
    if (!info || !('mediaSession' in navigator)) return
    const t = info.title
    navigator.mediaSession.metadata = new MediaMetadata({
      title: t.type === 'episode' ? `${epCode(t.season, t.episode)} · ${t.name}` : t.name, artist: t.type === 'episode' ? t.series_name || '' : String(t.year || ''),
      artwork: t.poster ? [{ src: img(t.poster, 512), sizes: '512x768', type: 'image/webp' }] : [],
    })
    const ms = navigator.mediaSession
    ms.setActionHandler('play', () => video.current?.play())
    ms.setActionHandler('pause', () => video.current?.pause())
    ms.setActionHandler('seekbackward', () => seek((video.current?.currentTime || 0) - 10))
    ms.setActionHandler('seekforward', () => seek((video.current?.currentTime || 0) + 10))
    ms.setActionHandler('nexttrack', info.next ? goNext : null)
    return () => { for (const a of ['play', 'pause', 'seekbackward', 'seekforward', 'nexttrack'] as MediaSessionAction[]) { try { ms.setActionHandler(a, null) } catch { /* unsupported */ } } }
  }, [info]) // eslint-disable-line react-hooks/exhaustive-deps

  const t = info?.title
  const head = t ? (t.type === 'episode' ? t.series_name : t.name) : ''
  const sizeCls = `subs-${settings?.subtitle_size || 'm'}`
  const tp = info?.trickplay
  const preview = (() => {
    if (!tp || !hover) return null
    const n = Math.min(tp.count - 1, Math.floor((hover.t * 1000) / tp.interval))
    const per = tp.tile_w * tp.tile_h
    const sheet = Math.floor(n / per), k = n % per
    const scale = 180 / tp.width
    return { url: `${tp.url}${sheet}.jpg?${tp.query}`, x: -(k % tp.tile_w) * tp.width * scale, y: -Math.floor(k / tp.tile_w) * tp.height * scale, w: tp.width * scale, h: tp.height * scale, sw: tp.tile_w * tp.width * scale, sh: tp.tile_h * tp.height * scale }
  })()
  const pct = (x: number) => (rt > 0 ? `${(x / rt) * 100}%` : '0%')
  const onBar = (e: React.PointerEvent<HTMLDivElement>) => { const r = e.currentTarget.getBoundingClientRect(); const x = Math.max(0, Math.min(e.clientX - r.left, r.width)); return { x, t: (x / r.width) * rt } }

  return (
    <div ref={box} className={`player ${ui ? 'show-ui' : 'hide-ui'} ${sizeCls}`} onPointerMove={poke} onPointerDown={poke}>
      <video ref={video} className="player-video" playsInline crossOrigin="use-credentials" onClick={() => { if (menu) setMenu(''); else toggle() }} onDoubleClick={fullscreen}
        poster={t?.backdrop ? img(t.backdrop, 1600) : undefined}>
        {info && !info.burned && info.subtitles.filter(s => s.text && s.url).map(s => <track key={s.index} id={`s${s.index}`} kind="subtitles" src={s.url} srcLang={s.lang || 'und'} label={s.label} />)}
      </video>
      {(waiting || !info) && !err && <div className="player-spin"><Spinner size={44} /></div>}
      {err && (
        <div className="player-error">
          <Icon name="alert" size={22} /><p>{err}</p>
          <div className="row"><button type="button" className="btn" onClick={() => open({ start: video.current?.currentTime || -1, transcode: true })}>Try again</button><button type="button" className="btn ghost" onClick={back}>Back</button></div>
        </div>
      )}

      <div className="player-top">
        <button type="button" className="pbtn" onClick={back} aria-label="Back"><Icon name="arrow-left" size={22} /></button>
        <div className="player-title">
          <b className="ellipsis">{head}</b>
          {t?.type === 'episode' && <span className="ellipsis">{epCode(t.season, t.episode)} · {t.name}</span>}
        </div>
        {info?.subtitles_tool && <a className="pbtn text" href={info.subtitles_tool} target="_blank" rel="noreferrer" title="Subtitles out of sync? Nudge them in Pocket Subtitles"><Icon name="cc" size={18} /><span className="hide-mobile">Out of sync?</span></a>}
      </div>

      {seg && skipMode !== 'off' && <button type="button" className="skip-btn" onClick={() => seek(seg.end)}>{seg.type === 'recap' ? 'Skip recap' : seg.type === 'preview' ? 'Skip preview' : 'Skip intro'}<Icon name="skip-intro" size={16} /></button>}
      {nearEnd && info?.next && (
        <div className="next-card">
          <span className="next-art"><img src={img(info.next.thumb, 320)} alt="" /></span>
          <div className="grow">
            <span className="muted xsmall">{nextIn !== null && nextIn > 0 ? `Next episode in ${nextIn}` : 'Next episode'}</span>
            <b className="ellipsis">{epCode(info.next.season, info.next.episode)} · {info.next.name}</b>
            <div className="row" style={{ gap: 6, marginTop: 6 }}>
              <button type="button" className="btn primary sm" onClick={goNext}><Icon name="play" size={13} />Play now</button>
              <button type="button" className="btn glass sm" onClick={() => { setNextDismissed(true); setNextIn(null) }}>Not now</button>
            </div>
          </div>
        </div>
      )}

      <div className="player-bottom" onPointerDown={e => e.stopPropagation()}>
        <div className="seek" onPointerMove={e => setHover(onBar(e))} onPointerLeave={() => setHover(null)}
          onPointerDown={e => { e.currentTarget.setPointerCapture(e.pointerId); seek(onBar(e).t) }}
          onPointerUp={e => e.currentTarget.releasePointerCapture(e.pointerId)}
          role="slider" aria-label="Position" aria-valuemin={0} aria-valuemax={Math.round(rt)} aria-valuenow={Math.round(time)} tabIndex={0}>
          <div className="seek-track">
            <span className="seek-buf" style={{ width: pct(buffered) }} />
            <span className="seek-fill" style={{ width: pct(time) }} />
            {info?.segments.filter(s => s.type === 'intro' || s.type === 'outro').map(s => <span key={s.start} className="seek-seg" style={{ left: pct(s.start), width: `calc(${pct(s.end)} - ${pct(s.start)})` }} />)}
            {info?.chapters.slice(1).map(c => <span key={c.start} className="seek-chap" style={{ left: pct(c.start) }} />)}
            <span className="seek-knob" style={{ left: pct(time) }} />
          </div>
          {hover && (
            <div className="seek-preview" style={{ left: hover.x }}>
              {preview && <span className="tp" style={{ width: preview.w, height: preview.h, backgroundImage: `url(${preview.url})`, backgroundPosition: `${preview.x}px ${preview.y}px`, backgroundSize: `${preview.sw}px ${preview.sh}px` }} />}
              <span className="tp-time">{clock(hover.t)}</span>
            </div>
          )}
        </div>
        <div className="player-bar">
          <button type="button" className="pbtn" onClick={toggle} aria-label={playing ? 'Pause' : 'Play'}><Icon name={playing ? 'pause' : 'play'} size={22} /></button>
          <button type="button" className="pbtn" onClick={() => seek(time - 10)} aria-label="Back 10 seconds"><Icon name="rewind-10" size={20} /></button>
          <button type="button" className="pbtn" onClick={() => seek(time + 10)} aria-label="Forward 10 seconds"><Icon name="forward-10" size={20} /></button>
          {info?.next && <button type="button" className="pbtn" onClick={goNext} aria-label="Next episode" title={`Next: ${epCode(info.next.season, info.next.episode)} ${info.next.name}`}><Icon name="skip-forward" size={18} /></button>}
          <div className="vol hide-mobile">
            <button type="button" className="pbtn" onClick={() => { const v = video.current; if (v) v.muted = !v.muted }} aria-label={muted ? 'Unmute' : 'Mute'}><Icon name={muted || volume === 0 ? 'volume-x' : volume < 0.5 ? 'volume-1' : 'volume'} size={20} /></button>
            <input type="range" min={0} max={1} step={0.02} value={muted ? 0 : volume} aria-label="Volume" onChange={e => { const v = video.current; if (v) { v.volume = Number(e.target.value); v.muted = false } }} />
          </div>
          <span className="ptime">{clock(time)} <span className="muted">/ {clock(rt)}</span></span>
          <span className="spacer" />
          {info && info.chapters.length > 1 && <button type="button" className={`pbtn ${menu === 'chapters' ? 'on' : ''}`} onClick={() => setMenu(menu === 'chapters' ? '' : 'chapters')} aria-label="Chapters"><Icon name="list" size={19} /></button>}
          <button type="button" className={`pbtn ${menu === 'tracks' ? 'on' : ''} ${sub >= 0 || info?.burned ? 'lit' : ''}`} onClick={() => setMenu(menu === 'tracks' ? '' : 'tracks')} aria-label="Audio and subtitles"><Icon name="cc" size={20} /></button>
          <button type="button" className={`pbtn ${menu === 'quality' ? 'on' : ''}`} onClick={() => setMenu(menu === 'quality' ? '' : 'quality')} aria-label="Quality"><Icon name="sliders" size={19} /></button>
          {'pictureInPictureEnabled' in document && <button type="button" className="pbtn hide-mobile" onClick={() => { const v = video.current; if (!v) return; if (document.pictureInPictureElement) document.exitPictureInPicture(); else v.requestPictureInPicture().catch(() => {}) }} aria-label="Picture in picture"><Icon name="pip" size={19} /></button>}
          <button type="button" className="pbtn" onClick={fullscreen} aria-label="Full screen"><Icon name={fs ? 'fullscreen-exit' : 'fullscreen'} size={19} /></button>
        </div>
        {menu === 'tracks' && info && (
          <div className="pmenu tracks">
            <div>
              <h4>Subtitles</h4>
              <button type="button" className={`pitem ${sub < 0 && !info.burned ? 'on' : ''}`} onClick={() => pickSub(null)}>Off</button>
              {info.subtitles.map(s => <button key={s.index} type="button" className={`pitem ${(info.burned ? info.subtitle_index : sub) === s.index ? 'on' : ''}`} onClick={() => pickSub(s)}>{s.label}{s.forced ? ' · forced' : ''}{!s.text && <span className="muted"> · image</span>}</button>)}
            </div>
            <div>
              <h4>Audio</h4>
              {info.audio.map(a => <button key={a.index} type="button" className={`pitem ${info.audio_index === a.index ? 'on' : ''}`} onClick={() => pickAudio(a)}>{a.label}</button>)}
            </div>
          </div>
        )}
        {menu === 'quality' && info && (
          <div className="pmenu quality">
            <h4>Quality</h4>
            {QUALITIES.map(q => <button key={q.v} type="button" className={`pitem ${(quality || settings?.quality || 'auto') === q.v ? 'on' : ''}`} onClick={() => pickQuality(q.v)}>{q.l}</button>)}
            <p className="muted xsmall">{info.method === 'DirectPlay' ? 'Playing the file as it is.' : 'Jellyfin is converting it for this browser.'}</p>
          </div>
        )}
        {menu === 'chapters' && info && (
          <div className="pmenu chapters">
            <h4>Chapters</h4>
            {info.chapters.map((c, i) => <button key={i} type="button" className="pitem" onClick={() => { seek(c.start); setMenu('') }}><span className="grow">{c.name || `Chapter ${i + 1}`}</span><span className="muted">{clock(c.start)}</span></button>)}
          </div>
        )}
      </div>
    </div>
  )
}
