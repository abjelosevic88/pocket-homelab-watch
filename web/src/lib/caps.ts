// What this browser can decode, so Jellyfin sends the file as it is when it can.
import Hls from 'hls.js'

export interface Caps { native_hls: boolean; mse: boolean; hevc: boolean; av1: boolean; vp9: boolean; ac3: boolean; eac3: boolean; opus: boolean; flac: boolean; max_width: number; surround: boolean }

let cached: Caps | null = null

export function detectCaps(): Caps {
  if (cached) return cached
  const v = document.createElement('video')
  const can = (t: string) => { try { return v.canPlayType(t) !== '' || (window.MediaSource?.isTypeSupported?.(t) ?? false) } catch { return false } }
  const native = v.canPlayType('application/vnd.apple.mpegurl') !== ''
  const mse = Hls.isSupported()
  cached = {
    native_hls: native && !mse ? true : native && /Safari/.test(navigator.userAgent) && !/Chrome|Android/.test(navigator.userAgent),
    mse,
    hevc: can('video/mp4; codecs="hvc1.1.6.L120.90"') || can('video/mp4; codecs="hev1.1.6.L120.90"'),
    av1: can('video/mp4; codecs="av01.0.08M.08"'),
    vp9: can('video/webm; codecs="vp9"'),
    ac3: can('audio/mp4; codecs="ac-3"'),
    eac3: can('audio/mp4; codecs="ec-3"'),
    opus: can('audio/webm; codecs="opus"') || can('audio/mp4; codecs="opus"'),
    flac: can('audio/mp4; codecs="flac"') || can('audio/flac'),
    max_width: Math.max(window.screen.width, window.screen.height) * Math.min(window.devicePixelRatio || 1, 2) >= 2600 ? 3840 : 1920,
    surround: false,
  }
  return cached
}
