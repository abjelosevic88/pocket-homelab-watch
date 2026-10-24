// "What should we watch?" — a sentence becomes a few picks from what you haven't seen.
import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Title } from '../lib/types'
import { subline } from '../lib/watch'
import { Icon } from './icons'
import { Img } from './cards'
import { Modal, Segmented, Spinner, errMsg } from './ui'

export interface AIStatus { ready: boolean; provider: string; detail: string }
export function useAIStatus() {
  const [st, setSt] = useState<AIStatus | null>(null)
  useEffect(() => { api.get<AIStatus>(`${V1}/ai/status`).then(setSt).catch(() => setSt({ ready: false, provider: '', detail: '' })) }, [])
  return st
}
export function AIStatusLine({ st }: { st: AIStatus | null }) {
  if (!st) return null
  return (
    <p className={`ai-status small ${st.ready ? 'ok-text' : 'muted'}`}>
      <Icon name={st.ready ? 'check-circle' : 'info'} size={14} />
      {st.ready ? <>Picked by {st.provider === 'pocket' ? 'Pocket AI' : 'your OpenAI key'}. It sees titles, years and genres of what you haven’t watched — never your history.</> : <>No AI set up{st.detail ? ` (${st.detail})` : ''}: picks come from the words — genres, length, era. <Link to="/settings/ai">Settings → AI</Link></>}
    </p>
  )
}

interface Pick { title: Title; why: string }
const IDEAS = ['Something funny and short', 'A scary film for tonight', 'A show to start', 'A classic I should have seen', 'Light, for the whole family', 'Something tense', 'Epic and long']

export function TonightModal({ onClose, initial = '' }: { onClose: () => void; initial?: string }) {
  const navigate = useNavigate()
  const ai = useAIStatus()
  const [prompt, setPrompt] = useState(initial)
  const [kind, setKind] = useState<'any' | 'movie' | 'series'>('any')
  const [len, setLen] = useState(0)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [res, setRes] = useState<{ picks: Pick[]; intro: string; provider: string } | null>(null)
  const ask = async (p = prompt) => {
    setBusy(true); setErr('')
    try { setRes(await api.post(`${V1}/tonight`, { prompt: p, kind, max_minutes: len })) } catch (e) { setErr(errMsg(e)) } finally { setBusy(false) }
  }
  const go = (to: string) => { onClose(); navigate(to) }
  return (
    <Modal title={<span className="row"><Icon name="popcorn" size={18} />What should we watch?</span>} onClose={onClose} size="lg" className="tonight">
      <form className="stack" onSubmit={e => { e.preventDefault(); ask() }}>
        <div className="tonight-input">
          <input autoFocus value={prompt} onChange={e => setPrompt(e.target.value)} placeholder="“Something funny under two hours”, “a show we can binge”…" aria-label="What are you in the mood for?" />
          <button className="btn primary" disabled={busy}>{busy ? <Spinner /> : <Icon name="sparkles" size={15} />}Find</button>
        </div>
        <div className="row wrap" style={{ gap: 6 }}>
          {IDEAS.map(i => <button key={i} type="button" className="chip sm" onClick={() => { setPrompt(i); ask(i) }}>{i}</button>)}
        </div>
        <div className="row wrap" style={{ gap: 12 }}>
          <Segmented size="sm" value={kind} onChange={setKind} options={[{ value: 'any', label: 'Anything' }, { value: 'movie', label: 'A film' }, { value: 'series', label: 'A show' }]} />
          <Segmented size="sm" value={len} onChange={setLen} options={[{ value: 0, label: 'Any length' }, { value: 100, label: '< 1 h 40' }, { value: 120, label: '< 2 h' }]} />
        </div>
        <AIStatusLine st={ai} />
      </form>
      {err && <div className="callout danger mt"><Icon name="alert" size={15} />{err}</div>}
      {busy && !res && <div className="loading mt"><span className="spinner" /> Looking through your shelves…</div>}
      {res && (
        <div className="mt">
          {res.intro && <p className="tonight-intro">{res.intro}</p>}
          <div className="picks">
            {res.picks.map(p => (
              <div key={p.title.id} className="pick">
                <button type="button" className="pick-art" onClick={() => go(`/title/${p.title.id}`)} aria-label={p.title.name}><Img src={p.title.poster} w={160} alt={p.title.name} /></button>
                <div className="pick-body">
                  <button type="button" className="pick-title" onClick={() => go(`/title/${p.title.id}`)}>{p.title.name}</button>
                  <span className="muted small">{[p.title.type === 'series' ? 'Show' : 'Film', subline(p.title), p.title.score ? `★ ${p.title.score}` : ''].filter(Boolean).join(' · ')}</span>
                  <p className="pick-why">{p.why}</p>
                  <div className="row" style={{ gap: 6 }}>
                    {p.title.playable && <button type="button" className="btn primary sm" onClick={() => go(`/play/${p.title.id}`)}><Icon name="play" size={13} />Play</button>}
                    <button type="button" className="btn sm" onClick={() => go(`/title/${p.title.id}`)}>Details</button>
                  </div>
                </div>
              </div>
            ))}
          </div>
          {res.picks.length > 0 && <div className="row mt"><button type="button" className="btn ghost sm" onClick={() => ask()} disabled={busy}><Icon name="refresh" size={14} />Other picks</button></div>}
        </div>
      )}
    </Modal>
  )
}
