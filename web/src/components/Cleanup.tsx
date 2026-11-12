// Settings → Clean-up (admins): rules that move titles to Leaving soon and then out of the library.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { fmtBytes, fmtDate, fmtDateTime } from '../lib/format'
import type { CleanupCondition, CleanupOverview, CleanupPreview, CleanupRule } from '../lib/types'
import { Icon } from './icons'
import { Select } from './Select'
import { Confirm, Empty, Field, Loading, Modal, Segmented, SettingRow, Spinner, Switch, errMsg, useToast } from './ui'

type FieldDef = { label: string; ops?: { value: string; label: string }[]; choices?: { value: string; label: string }[]; unit?: string; shows?: boolean; placeholder?: string }

const NUM_DAYS = [{ value: 'gt', label: 'more than' }, { value: 'lt', label: 'less than' }]
export const FIELDS: Record<string, FieldDef> = {
  watched_by: { label: 'Watched by', choices: [{ value: 'everyone', label: 'everyone here' }, { value: 'anyone', label: 'at least one person' }, { value: 'nobody', label: 'nobody, ever' }, { value: 'not_everyone', label: 'not everyone yet' }] },
  added_days: { label: 'Added to the library', ops: NUM_DAYS, unit: 'days ago', placeholder: '90' },
  last_watched_days: { label: 'Last watched by anyone', ops: NUM_DAYS, unit: 'days ago', placeholder: '365' },
  requested: { label: 'Asked for in Jellyseerr', choices: [{ value: 'yes', label: 'yes' }, { value: 'no', label: 'no' }] },
  requester_watched: { label: 'Whoever asked for it', choices: [{ value: 'yes', label: 'has watched it' }, { value: 'no', label: "hasn't watched it" }] },
  rating: { label: 'Rating', ops: [{ value: 'gt', label: 'above' }, { value: 'lt', label: 'below' }], unit: 'out of 10', placeholder: '6' },
  size_gb: { label: 'Size on disk', ops: [{ value: 'gt', label: 'more than' }, { value: 'lt', label: 'less than' }], unit: 'GB', placeholder: '20' },
  year: { label: 'Released', ops: [{ value: 'lt', label: 'before' }, { value: 'gt', label: 'after' }], placeholder: '2000' },
  genre: { label: 'Genre', ops: [{ value: 'is', label: 'is' }, { value: 'not', label: 'is not' }], placeholder: 'Horror' },
  status: { label: 'The show', shows: true, choices: [{ value: 'ended', label: 'has ended' }, { value: 'continuing', label: 'is still running' }] },
}

const TARGETS = [{ value: 'movie', label: 'Films' }, { value: 'series', label: 'Shows' }, { value: 'season', label: 'Seasons' }] as const
const PROTECT = [
  { id: 'watchlist', label: 'On someone’s watchlist' },
  { id: 'in_progress', label: 'Someone is in the middle of it', help: 'Started but not finished, played in the last six months.' },
  { id: 'favorite', label: 'Someone’s favourite' },
]

const blank = (target: CleanupRule['target'] = 'movie'): CleanupRule => ({
  id: 0, name: '', target, match: 'all', conditions: [{ field: 'watched_by', op: 'is', value: 'everyone' }, { field: 'added_days', op: 'gt', value: '90' }],
  protect: ['watchlist', 'in_progress', 'favorite'], grace_days: 14, action: 'delete', exclude_lists: false, clear_request: true, active: true,
})

const STARTERS: { label: string; sub: string; rule: Partial<CleanupRule> }[] = [
  { label: 'Films everyone has seen', sub: 'Watched by everyone, added over 90 days ago', rule: { name: 'Films everyone has seen', target: 'movie' } },
  { label: 'Films nobody watches', sub: 'Never played, added over a year ago', rule: { name: 'Films nobody watches', target: 'movie', conditions: [{ field: 'watched_by', op: 'is', value: 'nobody' }, { field: 'added_days', op: 'gt', value: '365' }] } },
  { label: 'Asked for, then watched', sub: 'Whoever requested it has watched it, 30 days ago or more', rule: { name: 'Requests that were watched', target: 'movie', conditions: [{ field: 'requester_watched', op: 'is', value: 'yes' }, { field: 'last_watched_days', op: 'gt', value: '30' }] } },
  { label: 'Finished shows', sub: 'The show has ended and everyone watched it all', rule: { name: 'Finished shows', target: 'series', action: 'unmonitor', conditions: [{ field: 'status', op: 'is', value: 'ended' }, { field: 'watched_by', op: 'is', value: 'everyone' }, { field: 'last_watched_days', op: 'gt', value: '60' }] } },
]

const kindLabel = (k: string) => k === 'movie' ? 'Films' : k === 'series' ? 'Shows' : 'Seasons'

export function Cleanup() {
  const { toast } = useToast()
  const [o, setO] = useState<CleanupOverview | null>(null)
  const [edit, setEdit] = useState<CleanupRule | null>(null)
  const [err, setErr] = useState('')
  const load = () => api.get<CleanupOverview>(`${V1}/cleanup`).then(x => { setO(x); setErr('') }).catch(e => setErr(errMsg(e)))
  useEffect(() => { load() }, [])
  useEffect(() => {
    if (!o?.running) return
    const t = setInterval(load, 2500)
    return () => clearInterval(t)
  }, [o?.running])
  if (err) return <div className="callout danger"><Icon name="alert" size={15} />{err}</div>
  if (!o) return <Loading />
  const run = async () => {
    try { await api.post(`${V1}/cleanup/run`); toast('Clean-up is running'); setO({ ...o, running: true }) } catch (e) { toast(errMsg(e), 'err') }
  }
  const toggle = async (r: CleanupRule) => {
    try { await api.put(`${V1}/cleanup/rules/${r.id}`, { ...r, active: !r.active }); load() } catch (e) { toast(errMsg(e), 'err') }
  }
  const setting = async (body: Record<string, unknown>) => {
    try { const s = await api.put<CleanupOverview['settings']>(`${V1}/cleanup/settings`, body); setO({ ...o, settings: s }); toast('Saved') } catch (e) { toast(errMsg(e), 'err') }
  }
  const unkeep = async (id: string) => {
    try { await api.del(`${V1}/cleanup/keeps/${id}`); load() } catch (e) { toast(errMsg(e), 'err') }
  }
  const noArr = !o.services.radarr && !o.services.sonarr
  const last = o.last
  return (
    <div className="stack loose">
      <section className="set-section">
        <div className="set-section-head">
          <span className="set-app-ico"><Icon name="hourglass" size={17} /></span>
          <div className="grow">
            <h3>How it works</h3>
            <p>Every {o.every_hours} hours each active rule looks at the library and at what everyone here has watched. What it picks goes on <Link to="/leaving">Leaving soon</Link> for the rule’s grace days, where anyone can keep it{o.settings.keep === 'admins' ? ' (admins only, see below)' : ''}. Then it is removed through Radarr or Sonarr{o.services.seerr ? ' and cleared in Jellyseerr' : ''}. Whoever asked for a title gets a push when it goes on Leaving soon.</p>
          </div>
          <button type="button" className="btn primary sm" onClick={run} disabled={o.running}>{o.running ? <><Spinner />Running…</> : <><Icon name="refresh" size={14} />Run now</>}</button>
        </div>
        <div className="set-section-body stack">
          <p className="row wrap small" style={{ gap: 6 }}>
            {(['radarr', 'sonarr', 'seerr'] as const).map(k => <span key={k} className={`badge ${o.services[k] ? 'ok' : ''}`}>{{ radarr: 'Radarr', sonarr: 'Sonarr', seerr: 'Jellyseerr' }[k]}{o.services[k] ? '' : ': not connected'}</span>)}
            <Link to="/settings/services" className="muted small">Services</Link>
          </p>
          {noArr && <p className="callout warn small"><Icon name="alert" size={14} /><span>Neither Radarr nor Sonarr is connected, so titles are deleted through Jellyfin. The owner’s Jellyfin account must be allowed to delete media.{o.setup_url && <> <a href={o.setup_url}>Finish Downloads setup →</a></>}</span></p>}
          {last ? (
            <p className={`small ${last.error ? 'bad' : 'muted'}`}>
              Last run {fmtDateTime(last.at)}: {last.error ? last.error : <>{last.matched} on Leaving soon ({last.added} new), {last.removed} removed{last.freed ? `, ${fmtBytes(last.freed)} freed` : ''}{last.failed ? `, ${last.failed} failed` : ''} · {last.people} {last.people === 1 ? 'person' : 'people'} counted</>}
              {last.notes && last.notes.length > 0 && <> · {last.notes.join(' · ')}</>}
            </p>
          ) : <p className="muted small">Hasn’t run yet.</p>}
        </div>
      </section>

      <section className="set-section">
        <div className="set-section-head">
          <div className="grow"><h3>Rules</h3><p>A title has to pass the rule’s conditions and not be protected. Kept titles are never picked.</p></div>
          <button type="button" className="btn sm" onClick={() => setEdit(blank())}><Icon name="plus" size={14} />New rule</button>
        </div>
        <div className="set-section-body">
          {o.rules.length === 0 ? (
            <div className="stack">
              <p className="muted small">No rules yet. Start from one of these (you can change everything before saving), or make your own:</p>
              <div className="cleanup-starters">
                {STARTERS.map(s => (
                  <button key={s.label} type="button" className="cleanup-starter" onClick={() => setEdit({ ...blank(s.rule.target), ...s.rule } as CleanupRule)}>
                    <b>{s.label}</b><span className="muted small">{s.sub}</span>
                  </button>
                ))}
              </div>
            </div>
          ) : o.rules.map(r => (
            <div key={r.id} className="set-person">
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="set-person-name">{r.name}<span className="badge">{kindLabel(r.target)}</span>{!r.active && <span className="badge">paused</span>}</div>
                <div className="muted small">{r.reason || ''}{r.reason ? ' · ' : ''}{r.grace_days} days on Leaving soon · {r.action === 'unmonitor' || r.target === 'season' ? 'files deleted, kept unmonitored' : 'deleted with its files'}</div>
                {r.last_run_at && <div className={`small ${r.last_error ? 'bad' : 'muted'}`}>{r.last_error || `${r.last_matched} on Leaving soon after the last run`}</div>}
              </div>
              <Switch size="sm" checked={r.active} onChange={() => toggle(r)} label={r.active ? 'Active' : 'Paused'} />
              <button type="button" className="btn ghost sm" onClick={() => setEdit(r)}>Edit</button>
            </div>
          ))}
        </div>
      </section>

      <section className="set-section">
        <div className="set-section-head"><div className="grow"><h3>Choices</h3></div></div>
        <div className="set-section-body">
          <SettingRow title="Who can keep a title" help="Keeping takes it off Leaving soon and out of every rule, until an admin lets go of it below.">
            <Segmented size="sm" value={o.settings.keep} onChange={v => setting({ keep: v })} options={[{ value: 'anyone', label: 'Anyone' }, { value: 'admins', label: 'Admins' }]} />
          </SettingRow>
          <SettingRow title="Removed by hand" help="An admin can remove any film, show or season from its page (More → Remove from the library). It stays on Leaving soon this long first.">
            <span className="row"><input type="number" min={1} max={365} style={{ width: 80 }} defaultValue={o.settings.manual_days} onBlur={e => { const n = Number(e.target.value); if (n && n !== o.settings.manual_days) setting({ manual_days: n }) }} /> days</span>
          </SettingRow>
        </div>
      </section>

      <section className="set-section">
        <div className="set-section-head"><div className="grow"><h3>Kept</h3><p>No rule picks these.</p></div></div>
        <div className="set-section-body">
          {o.keeps.length === 0 ? <p className="muted small">Nothing kept yet.</p> : o.keeps.map(k => (
            <div key={k.item_id} className="set-person">
              <div className="grow" style={{ minWidth: 0 }}>
                <div className="set-person-name"><Link to={`/title/${k.item_id}`}>{k.title || k.item_id}</Link></div>
                <div className="muted small">Kept by {k.by || 'someone'} · {fmtDate(k.created_at)}</div>
              </div>
              <button type="button" className="btn ghost sm" onClick={() => unkeep(k.item_id)}>Let go</button>
            </div>
          ))}
        </div>
      </section>

      <section className="set-section">
        <div className="set-section-head"><div className="grow"><h3>Removed</h3><p>The newest 100.</p></div></div>
        <div className="set-section-body">
          {o.log.length === 0 ? <p className="muted small">Nothing removed yet.</p> : (
            <div className="cleanup-log">
              {o.log.map(l => (
                <div key={l.id} className="cleanup-log-row">
                  <Icon name={l.ok ? 'check-circle' : 'x-circle'} size={14} className={l.ok ? 'ok' : 'bad'} />
                  <div className="grow" style={{ minWidth: 0 }}>
                    <div className="ellipsis"><b>{l.title}</b>{l.size ? <span className="muted"> · {fmtBytes(l.size)}</span> : null}</div>
                    <div className="muted small">{fmtDateTime(l.at)} · {l.rule || 'by hand'} · {l.via}{l.detail ? ` · ${l.detail}` : ''}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </section>

      {edit && <RuleEditor rule={edit} seerr={o.services.seerr} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); load() }} />}
    </div>
  )
}

function ConditionRow({ c, target, onChange, onRemove }: { c: CleanupCondition; target: string; onChange: (c: CleanupCondition) => void; onRemove: () => void }) {
  const def = FIELDS[c.field] || FIELDS.watched_by
  const fieldOpts = Object.entries(FIELDS).filter(([, d]) => !d.shows || target !== 'movie').map(([value, d]) => ({ value, label: d.label }))
  const pick = (field: string) => {
    const d = FIELDS[field]
    onChange({ field, op: d.ops ? d.ops[0].value : 'is', value: d.choices ? d.choices[0].value : '' })
  }
  return (
    <div className="cleanup-cond">
      <Select ariaLabel="Condition" value={c.field} onChange={pick} options={fieldOpts} />
      {def.ops && <Select ariaLabel="How" value={c.op} onChange={v => onChange({ ...c, op: v })} options={def.ops} />}
      {def.choices
        ? <Select ariaLabel="Value" value={c.value} onChange={v => onChange({ ...c, value: v })} options={def.choices} />
        : <span className="row" style={{ gap: 6 }}><input aria-label="Value" value={c.value} onChange={e => onChange({ ...c, value: e.target.value })} placeholder={def.placeholder} style={{ width: c.field === 'genre' ? 140 : 80 }} inputMode={c.field === 'genre' ? 'text' : 'decimal'} />{def.unit && <span className="muted small">{def.unit}</span>}</span>}
      <button type="button" className="btn ghost icon-only sm" aria-label="Remove condition" onClick={onRemove}><Icon name="x" size={14} /></button>
    </div>
  )
}

function RuleEditor({ rule, seerr, onClose, onSaved }: { rule: CleanupRule; seerr: boolean; onClose: () => void; onSaved: () => void }) {
  const { toast } = useToast()
  const [r, setR] = useState<CleanupRule>(rule)
  const [busy, setBusy] = useState(false)
  const [prev, setPrev] = useState<CleanupPreview | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [del, setDel] = useState(false)
  const set = (p: Partial<CleanupRule>) => { setR({ ...r, ...p }); setPrev(null) }
  const season = r.target === 'season'
  const save = async () => {
    setBusy(true)
    try {
      if (r.id) await api.put(`${V1}/cleanup/rules/${r.id}`, r)
      else await api.post(`${V1}/cleanup/rules`, r)
      toast(r.id ? 'Rule saved' : 'Rule added: it runs with the next clean-up (or press Run now)')
      onSaved()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const preview = async () => {
    setPreviewing(true)
    try { setPrev(await api.post<CleanupPreview>(`${V1}/cleanup/preview`, r)) } catch (e) { toast(errMsg(e), 'err') } finally { setPreviewing(false) }
  }
  const remove = async () => {
    try { await api.del(`${V1}/cleanup/rules/${r.id}`); toast('Rule deleted'); onSaved() } catch (e) { toast(errMsg(e), 'err') }
  }
  const footer = (
    <>
      {r.id > 0 && <button type="button" className="btn ghost danger-text" onClick={() => setDel(true)}>Delete rule</button>}
      <span style={{ flex: 1 }} />
      <button type="button" className="btn" onClick={preview} disabled={previewing}>{previewing ? <><Spinner />Looking…</> : <><Icon name="eye" size={15} />Preview</>}</button>
      <button type="button" className="btn primary" onClick={save} disabled={busy}>{r.id ? 'Save' : 'Add rule'}</button>
    </>
  )
  return (
    <Modal title={r.id ? 'Edit rule' : 'New clean-up rule'} onClose={onClose} size="lg" footer={footer}>
      <div className="stack">
        <Field label="Name"><input value={r.name} onChange={e => set({ name: e.target.value })} placeholder="Films everyone has seen" autoFocus /></Field>
        <Field group label="Looks at">
          <Segmented size="sm" value={r.target} onChange={v => set({ target: v, conditions: v === 'movie' ? r.conditions.filter(c => c.field !== 'status') : r.conditions })} options={TARGETS.map(t => ({ value: t.value, label: t.label }))} />
        </Field>
        <Field group label="Picks a title when it passes">
          <div className="stack" style={{ gap: 8 }}>
            <Segmented size="sm" value={r.match} onChange={v => set({ match: v })} options={[{ value: 'all', label: 'All of these' }, { value: 'any', label: 'Any of these' }]} />
            {r.conditions.map((c, i) => (
              <ConditionRow key={i} c={c} target={r.target} onChange={nc => set({ conditions: r.conditions.map((x, j) => j === i ? nc : x) })} onRemove={() => set({ conditions: r.conditions.filter((_, j) => j !== i) })} />
            ))}
            <div><button type="button" className="btn ghost sm" onClick={() => set({ conditions: [...r.conditions, { field: 'added_days', op: 'gt', value: '90' }] })}><Icon name="plus" size={14} />Add condition</button></div>
            <p className="muted small" style={{ margin: 0 }}>“Everyone” is everyone with an account here who watches through Jellyfin. Seasons count as watched once every episode is.</p>
          </div>
        </Field>
        <Field group label="Never picks a title that is">
          <div className="stack" style={{ gap: 6 }}>
            {PROTECT.map(p => (
              <label key={p.id} className="row" style={{ gap: 8 }} title={p.help}>
                <Switch size="sm" checked={r.protect.includes(p.id)} onChange={v => set({ protect: v ? [...r.protect, p.id] : r.protect.filter(x => x !== p.id) })} />
                <span>{p.label}{p.help && <span className="muted small"> · {p.help}</span>}</span>
              </label>
            ))}
          </div>
        </Field>
        <div className="stack">
          <Field label="Stays on Leaving soon for" help="Anyone can keep it during this time.">
            <span className="row"><input type="number" min={1} max={365} value={r.grace_days} onChange={e => set({ grace_days: Number(e.target.value) })} style={{ width: 90 }} /> days</span>
          </Field>
          <Field group label="Then">
            {season
              ? <p className="small" style={{ margin: 0 }}>The season’s files are deleted and Sonarr stops fetching it (the show stays).</p>
              : <Segmented size="sm" value={r.action} onChange={v => set({ action: v })} options={[{ value: 'delete', label: 'Delete it with its files' }, { value: 'unmonitor', label: 'Delete the files only' }]} />}
            {!season && r.action === 'unmonitor' && <p className="muted small" style={{ margin: '6px 0 0' }}>It stays in {r.target === 'movie' ? 'Radarr' : 'Sonarr'}, unmonitored, so it isn’t downloaded again.</p>}
          </Field>
        </div>
        {!season && r.action === 'delete' && <SettingRow title="Keep import lists from adding it back" help={`Adds an exclusion in ${r.target === 'movie' ? 'Radarr' : 'Sonarr'}.`}><Switch checked={r.exclude_lists} onChange={v => set({ exclude_lists: v })} /></SettingRow>}
        {!season && <SettingRow title={<>Clear it in Jellyseerr{!seerr && <span className="badge" style={{ marginLeft: 6 }}>not connected</span>}</>} help="So it shows as not there and can be asked for again."><Switch checked={r.clear_request} onChange={v => set({ clear_request: v })} /></SettingRow>}
        <SettingRow title="Active" help="A paused rule picks nothing, and what it put on Leaving soon comes off at the next run."><Switch checked={r.active} onChange={v => set({ active: v })} /></SettingRow>
        {prev && <Preview p={prev} />}
      </div>
      {del && <Confirm title={`Delete “${r.name}”?`} message="What it put on Leaving soon comes off. Nothing in the library is touched." confirmLabel="Delete" onCancel={() => setDel(false)} onConfirm={remove} />}
    </Modal>
  )
}

function Preview({ p }: { p: CleanupPreview }) {
  const [all, setAll] = useState(false)
  const shown = all ? p.matches : p.matches.slice(0, 24)
  return (
    <div className="cleanup-preview">
      <p style={{ margin: 0 }}><b>{p.matches.length === 0 ? 'Nothing right now.' : `${p.matches.length} title${p.matches.length === 1 ? '' : 's'}${p.size ? `, ${fmtBytes(p.size)}` : ''}`}</b>
        <span className="muted small"> · counted {p.people} {p.people === 1 ? 'person' : 'people'}{p.protected.length ? ` · ${p.protected.length} more protected` : ''}</span></p>
      {p.notes.length > 0 && <p className="muted small">{p.notes.join(' · ')}</p>}
      {p.matches.length > 0 && (
        <div className="cleanup-hits">
          {shown.map(h => (
            <Link key={h.id} to={`/title/${h.id}`} className="cleanup-hit" target="_blank">
              <img src={`${h.poster}?w=80`} alt="" loading="lazy" />
              <span className="grow ellipsis">{h.title}</span>
              {h.size ? <span className="muted small">{fmtBytes(h.size)}</span> : null}
            </Link>
          ))}
        </div>
      )}
      {!all && p.matches.length > shown.length && <button type="button" className="btn ghost sm" onClick={() => setAll(true)}>Show all {p.matches.length}</button>}
      {p.protected.length > 0 && (
        <details className="small"><summary className="muted">Protected ({p.protected.length})</summary>
          <ul className="muted">{p.protected.map(h => <li key={h.id}>{h.title} — {h.protected}</li>)}</ul>
        </details>
      )}
      {p.matches.length === 0 && <Empty title="Nothing matches" icon="hourglass" />}
    </div>
  )
}

export function RemoveDialog({ id, name, defaultDays, onClose, onDone }: { id: string; name: string; defaultDays: number; onClose: () => void; onDone: (removed: boolean) => void }) {
  const { toast } = useToast()
  const [when, setWhen] = useState<'later' | 'now'>('later')
  const [d, setD] = useState(defaultDays || 7)
  const [busy, setBusy] = useState(false)
  const go = async () => {
    setBusy(true)
    try {
      const res = await api.post<{ removed?: boolean; remove_on?: string }>(`${V1}/leaving/${id}`, { days: when === 'now' ? 0 : d })
      toast(res.removed ? `${name} was removed` : `${name} leaves on ${fmtDate(res.remove_on || '')}`)
      onDone(!!res.removed)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Remove ${name}?`} onClose={onClose} size="sm" footer={<><button type="button" className="btn" onClick={onClose}>Cancel</button><button type="button" className={`btn ${when === 'now' ? 'danger' : 'primary'}`} onClick={go} disabled={busy}>{busy ? <Spinner /> : null}{when === 'now' ? 'Remove now' : 'Put on Leaving soon'}</button></>}>
      <div className="stack">
        <Segmented size="sm" value={when} onChange={setWhen} options={[{ value: 'later', label: 'After a while' }, { value: 'now', label: 'Right now' }]} />
        {when === 'later'
          ? <p className="small" style={{ margin: 0 }}>It goes on Leaving soon for <input type="number" min={1} max={365} value={d} onChange={e => setD(Number(e.target.value))} style={{ width: 70 }} /> days, where anyone can still keep it.</p>
          : <p className="small" style={{ margin: 0 }}>Its files are deleted now through Radarr or Sonarr (or Jellyfin), and it is cleared in Jellyseerr. This can’t be undone.</p>}
      </div>
    </Modal>
  )
}

