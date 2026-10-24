// Small helpers shared by the pages: image sizes, lengths, episode codes.
import type { Title } from './types'

export { V1 } from './api'

/** A Pocket Watch image URL at a width (Jellyfin resizes). */
export function img(url: string | undefined, w: number): string {
  if (!url) return ''
  if (url.startsWith('https://image.tmdb.org/')) return url
  return url + (url.includes('?') ? '&' : '?') + 'w=' + Math.round(w * Math.min(2, window.devicePixelRatio || 1))
}

/** 1h 52m · 48m */
export function runtime(sec?: number): string {
  if (!sec) return ''
  const h = Math.floor(sec / 3600), m = Math.round((sec % 3600) / 60)
  return h > 0 ? (m > 0 ? `${h}h ${m}m` : `${h}h`) : `${m}m`
}

/** 12 h 40 m of watching, for totals. */
export function longDuration(sec: number): string {
  if (sec < 60) return '0 m'
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60)
  if (h >= 10) return `${h.toLocaleString()} h`
  if (h > 0) return `${h} h ${m} m`
  return `${m} m`
}

/** 1:02:03 / 4:05 */
export function clock(sec: number): string {
  if (!isFinite(sec) || sec < 0) sec = 0
  const s = Math.floor(sec % 60), m = Math.floor((sec / 60) % 60), h = Math.floor(sec / 3600)
  const p = (n: number) => String(n).padStart(2, '0')
  return h > 0 ? `${h}:${p(m)}:${p(s)}` : `${m}:${p(s)}`
}

export function epCode(season?: number, episode?: number, end?: number): string {
  if (season == null && episode == null) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `S${p(season ?? 0)}E${p(episode ?? 0)}${end && end !== episode ? `–${p(end)}` : ''}`
}

/** Where a title lives in the app. */
export function titleLink(t: Pick<Title, 'id' | 'type'>): string {
  return t.type === 'person' ? `/person/${t.id}` : `/title/${t.id}`
}

/** "S02E03 · The Walking Dead" for an episode, the year for the rest. */
export function subline(t: Title): string {
  if (t.type === 'episode') return [epCode(t.season, t.episode, t.episode_end), t.series_name].filter(Boolean).join(' · ')
  if (t.type === 'series') return [t.year && (t.end_year && t.end_year !== t.year ? `${t.year}–${t.end_year}` : t.status === 'Continuing' ? `${t.year}–` : String(t.year)), t.child_count ? `${t.child_count} episodes` : ''].filter(Boolean).join(' · ')
  return [t.year, runtime(t.runtime)].filter(Boolean).join(' · ')
}

export function tz(): string { return Intl.DateTimeFormat().resolvedOptions().timeZone }

export function remaining(t: Title): string {
  if (!t.runtime || !t.position) return ''
  const left = Math.max(0, t.runtime - t.position)
  return `${runtime(left)} left`
}

/** A stable hue from text (placeholder art). */
export function hue(s: string): number { let h = 0; for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0; return Math.abs(h) % 360 }

export function deviceName(): string {
  const ua = navigator.userAgent
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : 'Browser'
  const br = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : ''
  return br ? `${br} on ${os}` : os
}
