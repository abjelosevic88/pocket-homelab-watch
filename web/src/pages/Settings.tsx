import { AskButton } from '../components/AskButton'
import React, { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { APIToken, Connection, Settings as SettingsT, User } from '../lib/types'
import { DATE_FORMATS, TIME_FORMATS, fmtBytes, fmtDateTime, type DateFormat, type TimeFormat } from '../lib/format'
import { Avatar, Confirm, CoreHint, Field, Loading, Modal, Segmented, SettingRow, Spinner, Switch, Tabs, errMsg, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { ShortcutsSheet } from '../components/ShortcutsSheet'
import { PocketModelSelect } from '../components/PocketModelSelect'
import { AIStatusLine, useAIStatus } from '../components/Tonight'

type TabId = 'jellyfin' | 'playback' | 'history' | 'ai' | 'notifications' | 'services' | 'profile' | 'people' | 'tokens' | 'data' | 'about'

const TABS: { id: TabId; label: string; icon: string; sub: string; admin?: boolean }[] = [
  { id: 'jellyfin', label: 'Jellyfin', icon: 'server', sub: 'Your Jellyfin account: Pocket Watch plays with it, so resume points and watched marks stay yours everywhere.' },
  { id: 'playback', label: 'Playback', icon: 'play-circle', sub: 'Quality, audio and subtitle languages, skipping intros, the next episode.' },
  { id: 'history', label: 'History & upcoming', icon: 'history', sub: 'Where your history comes from, and which shows count as yours.' },
  { id: 'ai', label: 'AI', icon: 'sparkles', sub: 'Who answers “What should we watch?”.' },
  { id: 'notifications', label: 'Notifications', icon: 'bell', sub: 'A push when a request arrives, and a weekly recap.' },
  { id: 'services', label: 'Services', icon: 'layers', sub: 'Jellyseerr, Sonarr, Radarr, Maintainerr and Pocket Subtitles for the whole box.', admin: true },
  { id: 'profile', label: 'Profile', icon: 'user', sub: 'Your name, email and password.' },
  { id: 'people', label: 'People', icon: 'users', sub: 'Everyone who has an account on this box.' },
  { id: 'tokens', label: 'API & shortcuts', icon: 'key', sub: 'Personal tokens for Homepage widgets, Siri Shortcuts and Raycast.' },
  { id: 'data', label: 'Backup & data', icon: 'database', sub: 'Download the database, export your history or import it.' },
  { id: 'about', label: 'About', icon: 'info', sub: 'Version, numbers and the server.' },
]

export default function Settings() {
  const { tab: raw } = useParams()
  const navigate = useNavigate()
  const mobile = useIsMobile()
  const { user } = useApp()
  const tabs = TABS.filter(t => !t.admin || user?.role === 'admin')
  const tab: TabId = tabs.find(t => t.id === raw)?.id || 'jellyfin'
  const current = tabs.find(t => t.id === tab)!
  const go = (id: TabId) => navigate(`/settings/${id}`, { replace: true })
  useEffect(() => { document.querySelector('.set-content')?.scrollTo({ top: 0 }) }, [tab])
  useEffect(() => {
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape' && !document.querySelector('.modal-backdrop, .menu, .sel-pop')) navigate('/') }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [navigate])
  return (
    <div className="set-page">
      <header className="set-header">
        <button type="button" className="btn ghost icon-only" aria-label="Back to Pocket Watch" title="Back (esc)" onClick={() => navigate('/')}><Icon name="chevron-left" size={18} /></button>
        <img src="/favicon.svg" alt="" width={24} height={24} className="set-logo" />
        <h1>Settings</h1>
        <span style={{ flex: 1 }} />
        <AskButton />
      </header>
      {mobile && <div className="set-tabs-mobile"><Tabs tabs={tabs.map(t => ({ id: t.id, label: t.label, icon: t.icon }))} value={tab} onChange={go} /></div>}
      <div className="set-body">
        {!mobile && (
          <nav className="set-nav" aria-label="Settings sections">
            {tabs.map(t => (
              <button key={t.id} type="button" className={`set-nav-item ${t.id === tab ? 'active' : ''}`} aria-current={t.id === tab ? 'page' : undefined} onClick={() => go(t.id)}>
                <Icon name={t.icon} size={16} />{t.label}
              </button>
            ))}
          </nav>
        )}
        <main className="set-content">
          <div className="set-inner" key={tab}>
            <div className="set-title"><h2>{current.label}</h2><p>{current.sub}</p></div>
            {tab === 'jellyfin' && <JellyfinAccount />}
            {tab === 'playback' && <Playback />}
            {tab === 'history' && <HistorySettings />}
            {tab === 'ai' && <AISettings />}
            {tab === 'notifications' && <Notifications />}
            {tab === 'services' && <Services />}
            {tab === 'profile' && <Profile />}
            {tab === 'people' && <Users />}
            {tab === 'tokens' && <Tokens />}
            {tab === 'data' && <Data />}
            {tab === 'about' && <About />}
          </div>
        </main>
      </div>
    </div>
  )
}

function Section({ title, sub, icon, actions, children }: { title: React.ReactNode; sub?: React.ReactNode; icon?: string; actions?: React.ReactNode; children?: React.ReactNode }) {
  return (
    <section className="set-section">
      <div className="set-section-head">
        {icon && <span className="set-app-ico"><Icon name={icon} size={17} /></span>}
        <div className="grow"><h3>{title}</h3>{sub && <p>{sub}</p>}</div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {children && <div className="set-section-body">{children}</div>}
    </section>
  )
}

function useSettingsForm() {
  const { settings, setSettings, user } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<SettingsT | null>(settings)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (!f && settings) setF(settings) }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps
  const save = async (patch?: Partial<SettingsT>) => {
    if (!f) return
    setBusy(true)
    try { const s = await api.put<SettingsT>(`${V1}/settings`, { ...f, ...patch }); setSettings(s); setF(s); toast('Saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return { f, setF, save, busy, isAdmin: user?.role === 'admin' }
}

function useTester() {
  const [test, setTest] = useState<Record<string, string>>({})
  const run = async (which: string, url: string, key: string, extra: Record<string, string> = {}) => {
    setTest(t => ({ ...t, [which]: 'testing…' }))
    try { const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/integrations/test`, { which, url, key, ...extra }); setTest(t => ({ ...t, [which]: r.ok ? `✓ ${r.message}` : `✕ ${r.error}` })) } catch (e) { setTest(t => ({ ...t, [which]: `✕ ${errMsg(e)}` })) }
  }
  const status = (which: string) => <span className={`small ${test[which]?.startsWith('✓') ? 'ok-text' : test[which]?.startsWith('✕') ? 'danger-text' : 'muted'}`}>{test[which]}</span>
  return { run, status }
}

// ---------- Jellyfin ----------

function JellyfinAccount() {
  const { toast } = useToast()
  const { settings } = useApp()
  const [c, setC] = useState<Connection | null>(null)
  const [mode, setMode] = useState<'core' | 'direct' | 'off'>('direct')
  const [how, setHow] = useState<'password' | 'quick'>('password')
  const [form, setForm] = useState({ url: '', username: '', password: '' })
  const [busy, setBusy] = useState(false)
  const [code, setCode] = useState('')
  const poll = useRef<number>(0)
  const load = () => api.get<Connection>(`${V1}/connection`).then(x => { setC(x); setMode(x.mode); setForm(f => ({ ...f, url: x.custom_url || '', username: x.username || x.account || '' })) }).catch(e => toast(errMsg(e), 'err'))
  useEffect(() => { load(); return () => window.clearInterval(poll.current) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  if (!c) return <Loading />
  const save = async (m = mode) => {
    setBusy(true)
    try { const x = await api.put<Connection>(`${V1}/connection`, { mode: m, ...form }); setC(x); setMode(x.mode); setForm(f => ({ ...f, password: '' })); toast(x.connected ? 'Signed in — reading what you watched' : x.message || 'Saved', x.connected ? 'ok' : 'err') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const quick = async () => {
    setBusy(true)
    try {
      const r = await api.post<{ code: string }>(`${V1}/connection/quick-connect`, { url: form.url })
      setCode(r.code)
      window.clearInterval(poll.current)
      poll.current = window.setInterval(async () => {
        try {
          const s = await api.get<{ done: boolean; account?: string }>(`${V1}/connection/quick-connect`)
          if (s.done) { window.clearInterval(poll.current); setCode(''); toast(`Signed in as ${s.account}`); load() }
        } catch (e) { window.clearInterval(poll.current); setCode(''); toast(errMsg(e), 'err') }
      }, 2500)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const s = c.sync
  return (
    <div className="stack loose">
      <Section title="Your Jellyfin account" icon="server" actions={<span className={`badge ${c.connected ? 'ok' : 'warn'}`}>{c.connected ? `connected${c.via === 'core' ? ' · Pocket Core' : ''}` : 'not connected'}</span>}
        sub="Pocket Watch watches with your own account, so resume points, watched marks and favourites stay yours, and the TV, phone and Infuse agree with it.">
        <div className="conn-block">
          <div className="conn-mode">
            <Segmented size="sm" value={mode} onChange={m => { setMode(m); if (m !== 'direct') save(m) }} options={[
              { value: 'core', label: 'Pocket Core', title: c.core ? 'The Jellyfin login Pocket Core holds for you' : 'Needs Pocket Core', disabled: !c.core_configured },
              { value: 'direct', label: 'My account' }, { value: 'off', label: 'Off' }]} />
          </div>
          {!c.core_configured && <CoreHint />}
          <div className={`conn-status ${mode === 'off' ? '' : c.connected ? 'ok' : 'bad'}`}><span className="dot" />
            {c.connected ? <>Signed in as <b>{c.account}</b>{c.admin ? ' (administrator)' : ''} on {c.server_name || 'Jellyfin'} {c.server_version}{c.via === 'core' ? ' — the login Pocket Core keeps for you (Pocket Core → Settings → My logins)' : c.via === 'key' ? ' — with the API key this box was set up with' : ''}.</> : c.message}
          </div>
          {c.connected && c.counts && <div className="set-stats">
            <div className="set-stat"><Icon name="film" size={16} /><span className="set-stat-n">{c.counts.MovieCount.toLocaleString()}</span><span className="set-stat-l">Films</span></div>
            <div className="set-stat"><Icon name="tv" size={16} /><span className="set-stat-n">{c.counts.SeriesCount.toLocaleString()}</span><span className="set-stat-l">Shows</span></div>
            <div className="set-stat"><Icon name="layers" size={16} /><span className="set-stat-n">{c.counts.EpisodeCount.toLocaleString()}</span><span className="set-stat-l">Episodes</span></div>
          </div>}
          {mode === 'direct' && (
            <div className="stack">
              {!c.server_url && <Field label="Jellyfin address" help="e.g. http://jellyfin:8096"><input value={form.url} onChange={e => setForm({ ...form, url: e.target.value })} placeholder="https://jellyfin.example.com" /></Field>}
              {c.server_url && <p className="muted small">Jellyfin is set on this box ({settings?.jellyfin_public_url || 'JELLYFIN_URL'}). <button type="button" className="link-btn" onClick={() => setForm({ ...form, url: form.url || 'https://' })}>Use another server</button></p>}
              {c.server_url && form.url && <Field label="Other Jellyfin address"><input value={form.url} onChange={e => setForm({ ...form, url: e.target.value })} /></Field>}
              <Segmented size="sm" value={how} onChange={setHow} options={[{ value: 'password', label: 'Password' }, { value: 'quick', label: 'Quick Connect', disabled: c.quick_connect === false, title: c.quick_connect === false ? 'Switched off in Jellyfin (Dashboard → General)' : undefined }]} />
              {how === 'password' ? (
                <form className="stack" onSubmit={e => { e.preventDefault(); save('direct') }}>
                  <div className="two-col">
                    <Field label="Username"><input value={form.username} onChange={e => setForm({ ...form, username: e.target.value })} autoComplete="username" /></Field>
                    <Field label="Password" help="Used once to sign in; only Jellyfin's token is kept."><input type="password" value={form.password} onChange={e => setForm({ ...form, password: e.target.value })} autoComplete="current-password" /></Field>
                  </div>
                  <div className="row"><button className="btn primary" disabled={busy}>{busy ? <Spinner /> : null}{c.connected ? 'Sign in again' : 'Sign in'}</button></div>
                </form>
              ) : (
                <div className="stack">
                  <p className="muted small">For accounts that sign in with a passkey or single sign-on: approve a code in a Jellyfin app you're already signed in to (its menu → Quick Connect).</p>
                  {code ? <div className="qc-code"><span className="muted small">Enter this code in Jellyfin</span><b>{code}</b><span className="row small muted"><Spinner />Waiting for approval…</span></div>
                    : <div className="row"><button type="button" className="btn primary" onClick={quick} disabled={busy}>{busy && <Spinner />}Get a code</button></div>}
                </div>
              )}
            </div>
          )}
          {c.connected && c.public_url && <div className="row"><a className="btn sm" href={c.public_url} target="_blank" rel="noreferrer"><Icon name="external" size={13} />Open Jellyfin</a></div>}
        </div>
      </Section>
      {c.connected && (
        <Section title="Your history from Jellyfin" icon="history" sub="What you watch in other Jellyfin apps is read every few minutes; the first time, everything you had already finished.">
          <p className="muted small">{s.synced_at ? `Last read ${fmtDateTime(s.synced_at)}.` : 'Not read yet.'}{s.error ? <span className="danger-text"> {s.error}</span> : null}{c.syncing ? ' Reading now…' : ''}</p>
          <div className="row wrap mt-s">
            <button type="button" className="btn sm" onClick={() => api.post(`${V1}/sync`).then(() => toast('Reading your history'))}><Icon name="refresh" size={14} />Read now</button>
            <button type="button" className="btn sm ghost" onClick={() => api.post(`${V1}/sync?full=1`).then(() => toast('Reading everything again'))}>Read everything again</button>
          </div>
        </Section>
      )}
    </div>
  )
}

// ---------- playback ----------

const LANGS = [['', 'The file’s default'], ['eng', 'English'], ['srp', 'Serbian'], ['hrv', 'Croatian'], ['bos', 'Bosnian'], ['slv', 'Slovenian'], ['mkd', 'Macedonian'], ['deu', 'German'], ['fra', 'French'], ['spa', 'Spanish'], ['ita', 'Italian'], ['por', 'Portuguese'], ['nld', 'Dutch'], ['pol', 'Polish'], ['rus', 'Russian'], ['jpn', 'Japanese'], ['kor', 'Korean'], ['zho', 'Chinese']]

function Playback() {
  const { f, setF, save, busy } = useSettingsForm()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Picture" icon="film">
        <SettingRow title="Quality" help="“Best” plays the file as it is whenever this browser can; a cap makes Jellyfin convert to fit (slow connections, phones on mobile data). The player can change it per film too.">
          <Select ariaLabel="Quality" value={f.quality} onChange={v => setF({ ...f, quality: v })} options={[{ value: 'auto', label: 'Best' }, { value: '40', label: '40 Mbps' }, { value: '20', label: '20 Mbps' }, { value: '10', label: '10 Mbps' }, { value: '8', label: '8 Mbps (1080p)' }, { value: '4', label: '4 Mbps (720p)' }, { value: '2', label: '2 Mbps' }, { value: '1', label: '1 Mbps' }]} />
        </SettingRow>
      </Section>
      <Section title="Audio & subtitles" icon="cc">
        <SettingRow title="Audio language"><Select ariaLabel="Audio language" value={f.audio_lang} onChange={v => setF({ ...f, audio_lang: v })} options={LANGS.map(([v, l]) => ({ value: v, label: l }))} /></SettingRow>
        <SettingRow title="Subtitle languages" help="In order of preference, as three-letter codes: srp, hrv, eng" stack><input value={f.subtitle_lang} onChange={e => setF({ ...f, subtitle_lang: e.target.value })} placeholder="srp, eng" /></SettingRow>
        <SettingRow title="When to show subtitles">
          <Segmented size="sm" value={f.subtitle_mode} onChange={v => setF({ ...f, subtitle_mode: v })} options={[{ value: 'auto', label: 'Smart', title: 'When the audio is not in one of your languages, plus forced ones' }, { value: 'always', label: 'Always' }, { value: 'forced', label: 'Forced only' }, { value: 'off', label: 'Off' }]} />
        </SettingRow>
        <SettingRow title="Subtitle size"><Segmented size="sm" value={f.subtitle_size} onChange={v => setF({ ...f, subtitle_size: v })} options={[{ value: 's', label: 'S' }, { value: 'm', label: 'M' }, { value: 'l', label: 'L' }, { value: 'xl', label: 'XL' }]} /></SettingRow>
      </Section>
      <Section title="Shows" icon="tv">
        <SettingRow title="Skip intros and recaps" help="Needs intro marks in Jellyfin (the Intro Skipper plugin, or chapters it reads).">
          <Segmented size="sm" value={f.skip_intro} onChange={v => setF({ ...f, skip_intro: v })} options={[{ value: 'button', label: 'Show a button' }, { value: 'auto', label: 'Skip by itself' }, { value: 'off', label: 'Off' }]} />
        </SettingRow>
        <SettingRow title="Play the next episode" help="Counts down from ten during the credits."><Switch checked={f.autoplay_next} onChange={v => setF({ ...f, autoplay_next: v })} label="Autoplay the next episode" /></SettingRow>
        <SettingRow title="Counts as watched from" help="Past this share (or into the end credits) a title is marked watched.">
          <Segmented size="sm" value={f.completion} onChange={v => setF({ ...f, completion: v })} options={[{ value: 80, label: '80%' }, { value: 90, label: '90%' }, { value: 95, label: '95%' }]} />
        </SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function HistorySettings() {
  const { f, setF, save, busy } = useSettingsForm()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="History" icon="history" sub="Pocket Watch keeps one entry per title per day: from its own player as you watch, and from Jellyfin for everything else.">
        <SettingRow title="Include other Jellyfin apps" help="The TV, Infuse, Swiftfin, the Jellyfin web client. Off: only what plays here (and what you mark watched)."><Switch checked={f.history_all} onChange={v => setF({ ...f, history_all: v })} label="Read plays from other apps" /></SettingRow>
      </Section>
      <Section title="Upcoming" icon="calendar" sub="From Sonarr and Radarr, when an admin connected them.">
        <SettingRow title="Show by default">
          <Segmented size="sm" value={f.upcoming_scope} onChange={v => setF({ ...f, upcoming_scope: v })} options={[{ value: 'mine', label: 'Mine', title: 'Shows you watched this year or starred; films you requested or put on your watchlist' }, { value: 'all', label: 'Everything' }]} />
        </SettingRow>
      </Section>
      <div className="row"><button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button></div>
    </div>
  )
}

// ---------- AI ----------

function AISettings() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  const ai = useAIStatus()
  if (!f) return <Loading />
  const coreOn = f.core_configured && f.core_registered !== false
  const shown = !coreOn && f.ai_provider === 'pocket' ? 'openai' : (f.ai_provider || 'auto')
  return (
    <div className="stack loose">
      <Section title="What should we watch?" icon="popcorn" sub="The AI sees titles, years, genres, lengths and ratings of what you haven't watched — never your history — and answers with picks from that list. Without AI the picks come from the words.">
        <AIStatusLine st={ai} />
        <div className="grid" style={{ gap: 12 }}>
          <Field group label="Who picks">
            <Select ariaLabel="AI provider" value={shown} onChange={v => setF({ ...f, ai_provider: v as SettingsT['ai_provider'] })} options={[{ value: 'auto', label: coreOn ? 'Automatic — prefer Pocket AI' : 'Automatic' }, { value: 'pocket', label: 'Pocket AI', disabled: !coreOn, hint: coreOn ? undefined : 'needs Pocket Core' }, { value: 'openai', label: 'OpenAI / compatible API' }]} />
          </Field>
          {!coreOn && <CoreHint />}
          {coreOn && shown !== 'openai' && <PocketModelSelect value={f.ai_pocket_model || ''} onChange={v => setF({ ...f, ai_pocket_model: v })} />}
          {(shown === 'openai' || (!coreOn && shown === 'auto')) && <>
            <Field label="OpenAI API key" help="Stored on the server; shown masked."><input value={f.openai_api_key} onChange={e => setF({ ...f, openai_api_key: e.target.value })} autoComplete="off" placeholder="sk-…" /></Field>
            <div className="two-col">
              <Field label="Model" help="Default gpt-4o-mini."><input value={f.openai_model} onChange={e => setF({ ...f, openai_model: e.target.value })} placeholder="gpt-4o-mini" /></Field>
              <Field label="Base URL (optional)" help="Ollama, LM Studio, OpenRouter…"><input value={f.openai_base_url} onChange={e => setF({ ...f, openai_base_url: e.target.value })} placeholder="https://api.openai.com/v1" /></Field>
            </div>
          </>}
        </div>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        {(shown === 'openai' || (!coreOn && shown === 'auto')) && <><button type="button" className="btn" onClick={() => run('openai', f.openai_base_url, f.openai_api_key)} disabled={!f.openai_api_key}>Test the key</button>{status('openai')}</>}
      </div>
    </div>
  )
}

// ---------- notifications ----------

function Notifications() {
  const { f, setF, save, busy } = useSettingsForm()
  const { run, status } = useTester()
  if (!f) return <Loading />
  return (
    <div className="stack loose">
      <Section title="Push" icon="bell" actions={<span className={`badge ${f.notify_configured ? 'ok' : ''}`}>{f.notify_configured ? 'on' : 'not set'}</span>}
        sub={<>Apprise gateway (<code>…/notify/&lt;key&gt;</code>), an ntfy topic URL, or any webhook that takes JSON.</>}>
        <SettingRow title="URL" stack><input value={f.notify_url} onChange={e => setF({ ...f, notify_url: e.target.value })} placeholder="http://apprise:8000/notify/watch" /></SettingRow>
        <SettingRow title="Format"><Segmented value={f.notify_format} onChange={v => setF({ ...f, notify_format: v })} options={[{ value: 'apprise', label: 'Apprise' }, { value: 'ntfy', label: 'ntfy' }, { value: 'json', label: 'JSON' }]} size="sm" /></SettingRow>
        <SettingRow title="A request arrives" help="When something you asked for in Jellyseerr is ready to watch."><Switch checked={f.notify_available} onChange={v => setF({ ...f, notify_available: v })} label="Push arrivals" /></SettingRow>
        <SettingRow title="Weekly recap" help="Monday morning: how much you watched, films and episodes, your top show."><Switch checked={f.notify_weekly} onChange={v => setF({ ...f, notify_weekly: v })} label="Weekly recap" /></SettingRow>
      </Section>
      <div className="row wrap">
        <button type="button" className="btn primary" onClick={() => save()} disabled={busy}>Save changes</button>
        <button type="button" className="btn" onClick={() => run('notify', f.notify_url, '', { format: f.notify_format })} disabled={!f.notify_url}><Icon name="mail" size={15} />Send a test</button>
        {status('notify')}
      </div>
      <Section title="Date & time">
        <SettingRow title="Date format"><Select value={f.date_format} onChange={v => setF({ ...f, date_format: v as DateFormat })} options={DATE_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
        <SettingRow title="Time format"><Select value={f.time_format} onChange={v => setF({ ...f, time_format: v as TimeFormat })} options={TIME_FORMATS.map(d => ({ value: d.id, label: d.label }))} /></SettingRow>
      </Section>
    </div>
  )
}

// ---------- the box's services (admins) ----------

type SvcStatus = { mode: string; via?: string; url?: string; error?: string }

/** The address and key: shown for Direct; for Automatic only while Core isn't the way in (else folded behind a link). */
function Fallback({ mode, st, children }: { mode: string; st?: SvcStatus; children: React.ReactNode }) {
  const [open, setOpen] = useState(false)
  if (mode === 'core' || mode === 'off') return null
  if (mode === 'direct') return <>{children}</>
  if (st && st.via !== 'core' && !(st.error && mode === 'off')) return <>{children}</>
  if (!st) return null
  return open
    ? <><p className="muted small" style={{ margin: 0 }}>Used only when Pocket Core can't answer. <button type="button" className="link-btn" onClick={() => setOpen(false)}>Hide</button></p>{children}</>
    : <p className="muted small" style={{ margin: 0 }}><button type="button" className="link-btn" onClick={() => setOpen(true)}>Direct fallback…</button></p>
}

/** One line on how a service is reached right now: through Pocket Core, directly, or why not. */
function SvcLine({ st }: { st?: SvcStatus }) {
  if (!st) return <p className="svc-line muted small"><span className="dot" />Checking…</p>
  if (st.error && st.mode === 'off') return <p className="svc-line muted small"><span className="dot" />Off</p>
  if (st.error) return <p className="svc-line bad small"><span className="dot" />{st.error}</p>
  return <p className="svc-line ok small"><span className="dot" />{st.via === 'core' ? 'Connected through Pocket Core' : st.via === 'auto' ? 'Through Pocket Core, or the token when Core can\'t' : 'Connected directly'}{st.url ? <> · <code>{st.url}</code></> : null}</p>
}

function Services() {
  const { f, setF, save: saveRaw, busy } = useSettingsForm()
  const { run, status } = useTester()
  const [live, setLive] = useState<Record<string, SvcStatus> | null>(null)
  const load = (fresh = false) => api.get<Record<string, SvcStatus>>(`${V1}/services/status${fresh ? '?fresh=1' : ''}`).then(setLive).catch(() => setLive({}))
  useEffect(() => { load() }, [])
  if (!f) return <Loading />
  const save = async () => { await saveRaw(); setLive(null); load(true) }
  const coreOn = f.core_configured && f.core_registered !== false
  const svc = (which: string, title: string, icon: string, sub: React.ReactNode, modeKey: keyof SettingsT, urlKey: keyof SettingsT, keyKey: keyof SettingsT | null, placeholder: string, extra?: React.ReactNode) => {
    const mode = String(f[modeKey] || '')
    const st = live?.[which]
    const badge = !st ? '' : st.error ? (mode === 'off' ? 'off' : 'not connected') : st.via === 'core' ? 'Pocket Core' : 'direct'
    return (
      <Section title={title} icon={icon} sub={sub} actions={badge && <span className={`badge ${st && !st.error ? 'ok' : st?.error && mode !== 'off' ? 'warn' : ''}`}>{badge}</span>}>
        <div className="stack">
          <Segmented size="sm" value={mode} onChange={v => setF({ ...f, [modeKey]: v })} options={[
            { value: '', label: 'Automatic', title: 'Pocket Core first, the address below when Core can\'t' },
            { value: 'core', label: 'Pocket Core', disabled: !coreOn, title: coreOn ? 'The login Pocket Core keeps for you (My logins), switched on in Pocket Core → Links' : 'Needs Pocket Core' },
            { value: 'direct', label: 'Direct' }, { value: 'off', label: 'Off' }]} />
          {!coreOn && <CoreHint />}
          <SvcLine st={st} />
          <Fallback mode={mode} st={st}>
            <div className={keyKey ? 'two-col' : ''}>
              <Field label="Address"><input value={String(f[urlKey] || '')} onChange={e => setF({ ...f, [urlKey]: e.target.value })} placeholder={placeholder} /></Field>
              {keyKey && <Field label="API key"><input value={String(f[keyKey] || '')} onChange={e => setF({ ...f, [keyKey]: e.target.value })} autoComplete="off" /></Field>}
            </div>
          </Fallback>
          {extra}
          <div className="row"><button type="button" className="btn sm" onClick={() => run(which, '', '')} disabled={mode === 'off'}>Test</button>{status(which)}</div>
        </div>
      </Section>
    )
  }
  return (
    <div className="stack loose">
      <p className="muted small">These belong to the whole box. Through Pocket Core, Watch uses the logins Core keeps for you (Pocket Core → Settings → My logins); switch each one on in Pocket Core → Links. Pocket Watch still acts as each person: requests go in as their own Jellyseerr user, and nobody sees anyone else's requests or history.</p>
      {svc('seerr', 'Jellyseerr', 'ticket', 'Discover, requests, and a push when they arrive. People without a Jellyseerr user get one imported from Jellyfin on their first request.', 'seerr_mode', 'seerr_url', 'seerr_key', 'http://jellyseerr:5055',
        <Field label="Address people open (optional)" help="For links to Jellyseerr; the address above may only work inside the box."><input value={f.seerr_public_url || ''} onChange={e => setF({ ...f, seerr_public_url: e.target.value })} placeholder="https://requests.example.com" /></Field>)}
      {svc('sonarr', 'Sonarr', 'tv', 'Upcoming episodes (the calendar only).', 'sonarr_mode', 'sonarr_url', 'sonarr_key', 'http://sonarr:8989')}
      {svc('radarr', 'Radarr', 'film', 'Upcoming films: cinema, digital and disc releases.', 'radarr_mode', 'radarr_url', 'radarr_key', 'http://radarr:7878')}
      {svc('maintainerr', 'Maintainerr', 'hourglass', '“Leaving soon”: titles a cleanup rule will remove, with the day.', 'maintainerr_mode', 'maintainerr_url', null, 'http://maintainerr:6246')}
      <SubtitlesService f={f} setF={setF} run={run} status={status} live={live?.subtitles} />
      <div className="row"><button type="button" className="btn primary" onClick={save} disabled={busy}>Save changes</button></div>
    </div>
  )
}

function SubtitlesService({ f, setF, run, status, live }: { f: SettingsT; setF: (s: SettingsT) => void; run: (which: string, url: string, key: string) => void; status: (which: string) => React.ReactNode; live?: SvcStatus }) {
  const coreOn = f.core_configured && f.core_registered !== false
  const mode = f.subtitles_mode || ''
  const eff = f.subtitles_mode_effective || 'off'
  return (
    <Section title="Pocket Subtitles" icon="cc" actions={<span className={`badge ${eff !== 'off' && !live?.error ? 'ok' : live?.error ? 'warn' : ''}`}>{eff === 'core' ? 'Pocket Core' : eff === 'direct' ? 'token' : eff === 'auto' ? 'automatic' : 'off'}</span>}
      sub="While watching, anyone can move a subtitle track earlier or later for themselves. With Pocket Subtitles connected they can also save that timing into the .srt file for everyone: it shifts every line, pins the file so the nightly sync keeps it, and keeps a backup for undo.">
      <div className="stack">
        <Field group label="How Watch reaches it">
          <Segmented size="sm" value={mode} onChange={v => setF({ ...f, subtitles_mode: v })} options={[
            { value: '', label: 'Automatic' },
            { value: 'core', label: 'Pocket Core', disabled: !coreOn, title: coreOn ? 'As each person, through the app link (switch it on in Pocket Core → Links)' : 'Needs Pocket Core' },
            { value: 'direct', label: 'Token' }, { value: 'off', label: 'Off' }]} />
        </Field>
        {!coreOn && <CoreHint />}
        <SvcLine st={live} />
        <Fallback mode={mode} st={live}>
        <div className="two-col">
          <Field label="API address" help="As this server reaches it, e.g. http://pocket-subtitles:8080"><input value={f.subtitles_api_url || ''} onChange={e => setF({ ...f, subtitles_api_url: e.target.value })} placeholder="http://pocket-subtitles:8080" /></Field>
          <Field label="API token" help="Pocket Subtitles → Settings → API tokens"><input value={f.subtitles_token || ''} onChange={e => setF({ ...f, subtitles_token: e.target.value })} autoComplete="off" /></Field>
        </div>
        </Fallback>
        <Field label="Address people open" help="Admins get a link to the file there from the player."><input value={f.subtitles_url || ''} onChange={e => setF({ ...f, subtitles_url: e.target.value })} placeholder="https://subtitles.example.com" /></Field>
        <div className="row"><button type="button" className="btn sm" onClick={() => run('subtitles', mode === 'direct' ? f.subtitles_api_url || '' : '', mode === 'direct' ? f.subtitles_token || '' : '')}>Test</button>{status('subtitles')}</div>
      </div>
    </Section>
  )
}

// ---------- people, profile, tokens, data, about ----------

function Users() {
  const { user, users, reloadMeta, status } = useApp()
  const { toast } = useToast()
  const [edit, setEdit] = useState<Partial<User> | null>(null)
  const [del, setDel] = useState<User | null>(null)
  const isAdmin = user?.role === 'admin'
  return (
    <Section title="People on this box" actions={isAdmin && <button type="button" className="btn primary sm" onClick={() => setEdit({ role: 'member' })}><Icon name="plus" size={14} />Add person</button>}
      sub="Everyone signs in with their own account and watches with their own Jellyfin account: their own history, watchlist, requests and resume points. Admins manage accounts; they don't see anyone else's watching.">
      <div className="set-people">
        {users.map(u => (
          <div key={u.id} className="set-person">
            <Avatar name={u.name} email={u.email} size={34} />
            <div className="grow">
              <div className="set-person-name">{u.name || <span className="muted">—</span>}{u.id === user?.id && <span className="badge">you</span>}{u.role === 'admin' && <span className="badge accent">admin</span>}</div>
              <div className="muted small ellipsis">{u.email}</div>
            </div>
            {isAdmin && <span className="row" style={{ justifyContent: 'flex-end' }}><button type="button" className="btn ghost sm" onClick={() => setEdit(u)}>Edit</button>{u.id !== user?.id && <button type="button" className="btn ghost sm danger-text" onClick={() => setDel(u)}>Remove</button>}</span>}
          </div>
        ))}
      </div>
      {status?.auth_mode !== 'local' && <p className="muted small mt">Auth mode is <code>{status?.auth_mode}</code>: users are created automatically on first visit.</p>}
      {edit && <UserModal user={edit} local={status?.auth_mode === 'local'} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reloadMeta() }} />}
      {del && <Confirm confirmLabel="Remove" title={`Remove ${del.name || del.email}?`} message="Their account is deleted here with their history, watchlist and followed requests. Their Jellyfin account stays." onCancel={() => setDel(null)}
        onConfirm={async () => { const d = del; setDel(null); try { await api.del(`${V1}/users/${d.id}`); await reloadMeta(); toast('Removed') } catch (e) { toast(errMsg(e), 'err') } }} />}
    </Section>
  )
}

function UserModal({ user, local, onClose, onSaved }: { user: Partial<User>; local: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [f, setF] = useState({ name: user.name || '', email: user.email || '', password: '', role: user.role || 'member' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { if (user.id) await api.put(`${V1}/users/${user.id}`, f); else await api.post(`${V1}/users`, f); toast(user.id ? 'Saved' : 'Added'); onSaved() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={user.id ? 'Edit person' : 'Add a person'} onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} autoFocus required maxLength={60} /></Field>
        <Field label="Email" help="Used to sign in."><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} required /></Field>
        {local && <Field label={user.id ? 'New password (leave empty to keep)' : 'Password'} help="At least 8 characters."><input type="password" value={f.password} onChange={e => setF({ ...f, password: e.target.value })} minLength={user.id ? 0 : 8} required={!user.id} autoComplete="new-password" /></Field>}
        <Field group label="Role"><Segmented value={f.role} onChange={v => setF({ ...f, role: v })} options={[{ value: 'member', label: 'Member' }, { value: 'admin', label: 'Admin' }]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary" disabled={busy}>{user.id ? 'Save' : 'Add person'}</button></div>
      </form>
    </Modal>
  )
}

function Profile() {
  const { user, setUser, status } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState({ name: user?.name || '', email: user?.email || '', current_password: '', new_password: '' })
  const [busy, setBusy] = useState(false)
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true)
    try { setUser(await api.put(`${V1}/auth/me`, f)); setF(x => ({ ...x, current_password: '', new_password: '' })); toast('Profile saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <form onSubmit={save} className="stack loose">
      <div className="set-profile">
        <Avatar name={user?.name} email={user?.email} size={56} />
        <div className="grow"><h3>{user?.name || user?.email}</h3><p className="muted small">{user?.email} · {user?.role}</p></div>
      </div>
      <Section title="You"><div className="stack">
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} /></Field>
        <Field label="Email"><input type="email" value={f.email} onChange={e => setF({ ...f, email: e.target.value })} /></Field>
      </div></Section>
      {status?.auth_mode === 'local' && <Section title="Password" sub="Leave empty to keep the current one."><div className="stack">
        <Field label="Current password"><input type="password" value={f.current_password} onChange={e => setF({ ...f, current_password: e.target.value })} autoComplete="current-password" /></Field>
        <Field label="New password" help="At least 8 characters."><input type="password" value={f.new_password} onChange={e => setF({ ...f, new_password: e.target.value })} autoComplete="new-password" /></Field>
      </div></Section>}
      <div className="row"><button className="btn primary" disabled={busy}>Save changes</button></div>
    </form>
  )
}

function Tokens() {
  const { toast } = useToast()
  const { settings } = useApp()
  const { data, reload } = useAsync(() => api.get<APIToken[]>(`${V1}/tokens`), [])
  const [name, setName] = useState('')
  const [fresh, setFresh] = useState('')
  const [del, setDel] = useState<APIToken | null>(null)
  const create = async (e: React.FormEvent) => {
    e.preventDefault()
    try { const r = await api.post<{ token: string }>(`${V1}/tokens`, { name: name || 'token' }); setFresh(r.token); setName(''); reload() } catch (e) { toast(errMsg(e), 'err') }
  }
  const base = settings?.base_url || window.location.origin
  return (
    <div className="stack loose">
      <Section title="Personal tokens" icon="key" sub={<>Use as <code>Authorization: Bearer &lt;token&gt;</code>. A token acts as you. See <code>docs/api.md</code>.</>}>
        <form onSubmit={create} className="row mb"><input value={name} onChange={e => setName(e.target.value)} placeholder="Token name (e.g. Homepage widget)" /><button className="btn primary"><Icon name="plus" size={15} />Create</button></form>
        {fresh && <div className="set-fresh mb"><div className="row"><Icon name="check-circle" size={16} /><b>Copy it now — it is shown once.</b></div><pre className="mono">{fresh}</pre></div>}
        {data && data.length > 0 ? (
          <table className="table compact">
            <thead><tr><th>Name</th><th>Prefix</th><th>Last used</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map(t => <tr key={t.id}><td>{t.name}</td><td className="mono">{t.prefix}…</td><td className="muted">{t.last_used_at ? fmtDateTime(t.last_used_at) : 'never'}</td><td className="muted">{fmtDateTime(t.created_at)}</td><td className="right"><button type="button" className="btn ghost sm danger-text" onClick={() => setDel(t)}>Revoke</button></td></tr>)}</tbody>
          </table>
        ) : <p className="muted">No tokens yet.</p>}
        {del && <Confirm confirmLabel="Revoke" title={`Revoke “${del.name}”?`} message="Anything using this token stops working immediately." onCancel={() => setDel(null)} onConfirm={async () => { await api.del(`${V1}/tokens/${del.id}`); setDel(null); reload() }} />}
      </Section>
      <Section title="Handy endpoints" icon="smartphone" sub={<>At <code>{base}/api/v1</code>.</>}>
        <dl className="set-dl">
          <div><dt><code>GET /dashboard</code></dt><dd className="muted">Now playing, time watched today and this week, open requests — for a Homepage widget.</dd></div>
          <div><dt><code>GET /day/2026-10-24</code></dt><dd className="muted">One day's watching in a sentence and a list (Pocket Day uses it).</dd></div>
          <div><dt><code>GET /upcoming</code> · <code>GET /history</code> · <code>GET /stats?range=30d</code></dt><dd className="muted">For calendars, dashboards and scripts.</dd></div>
          <div><dt><code>{base}/tonight</code></dt><dd className="muted">Opens “What should we watch?” (a Siri Shortcut or a home-screen button).</dd></div>
        </dl>
      </Section>
    </div>
  )
}

function Data() {
  const { toast } = useToast()
  const { user } = useApp()
  const [busy, setBusy] = useState(false)
  const importJSON = async (file: File) => {
    setBusy(true)
    try { const r = await api.post<Record<string, number>>(`${V1}/import`, JSON.parse(await file.text())); toast('Imported: ' + (Object.entries(r).map(([k, v]) => `${v} ${k.replace('_', ' ')}`).join(', ') || 'nothing new')) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <div className="stack loose">
      {user?.role === 'admin' && <Section title="Database backup" icon="database" sub="The whole box, every account — admins only.">
        <p className="muted">A consistent copy of the SQLite database (<code>VACUUM INTO</code>): history, watchlists, followed requests and settings. The films and resume points live in Jellyfin. Restore by stopping the app and replacing <code>pocket-watch.db</code>.</p>
        <a className="btn primary" href={`${V1}/backup`}><Icon name="download" size={15} />Download database</a>
      </Section>}
      <Section title="Your history" icon="archive" sub="Everything you watched and your watchlist as JSON, to keep or to move to another box.">
        <div className="row wrap">
          <a className="btn" href={`${V1}/export.json`}><Icon name="download" size={15} />Export JSON</a>
          <label className="btn"><Icon name="upload" size={15} />{busy ? 'Importing…' : 'Import JSON…'}<input type="file" accept="application/json,.json" hidden onChange={e => e.target.files?.[0] && importJSON(e.target.files[0])} /></label>
        </div>
      </Section>
    </div>
  )
}

function About() {
  const { data, error } = useAsync(() => api.get<Record<string, unknown>>(`${V1}/settings/system`), [])
  const [keys, setKeys] = useState(false)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const counts = (data.counts || {}) as Record<string, number>
  const tiles: [string, number | undefined, string][] = [['Titles watched', counts.watches, 'history'], ['On the watchlist', counts.watchlist, 'bookmark'], ['Requests followed', counts.requests, 'ticket']]
  const core = (data.core || {}) as { configured: boolean; registered: boolean; url: string; error: string }
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{String(data.version)}</code>],
    ['Uptime', String(data.uptime)],
    ['Runtime', `${String(data.go)} · ${String(data.os)}/${String(data.arch)}`],
    ['Time zone', String(data.timezone)],
    ['Sign-in', String(data.auth_mode)],
    ['Jellyfin', data.jellyfin_url ? <code key="n">{String(data.jellyfin_url)}</code> : 'each person enters their own'],
    ['Pocket Core', core.configured ? (core.registered ? <span key="c">registered at <code>{core.url}</code></span> : <span key="c" className="danger-text">not registered: {core.error}</span>) : 'not configured'],
    ['Database', <span key="db"><code>{String(data.db_path)}</code> · {fmtBytes(Number(data.db_bytes))}</span>],
    ['People', `${counts.users ?? 0} accounts on this box`],
  ]
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Watch</h3>
          <p className="muted small">Films and shows for the whole household. Jellyfin keeps the files, transcodes and serves every other app; Pocket Watch adds a fast player, a watch history it owns with stats, a watchlist, Jellyseerr requests that tell you when they arrive, upcoming episodes, and “what should we watch?”. One binary, one SQLite file.</p>
        </div>
      </div>
      <div className="set-stats">
        {tiles.map(([label, n, icon]) => <div key={label} className="set-stat"><Icon name={icon} size={16} /><span className="set-stat-n tnum">{(n ?? 0).toLocaleString()}</span><span className="set-stat-l">{label}</span></div>)}
      </div>
      <Section title="This server"><dl className="set-dl">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl></Section>
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setKeys(true)}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-watch" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
      <p className="muted small">Open source (MIT). Part of the Pocket apps: Pocket Core connects them — Day, Calendar, Music, Photos and more.</p>
      {keys && <ShortcutsSheet onClose={() => setKeys(false)} />}
    </div>
  )
}
