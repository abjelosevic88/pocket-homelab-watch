import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['h'], 'Home'], [['f'], 'Films'], [['s'], 'Shows'], [['l'], 'Watchlist'], [['u'], 'Upcoming'], [['d'], 'Discover'], [['r'], 'Requests'], [['y'], 'History'], [['t'], 'Stats'], [[','], 'Settings']] },
  { title: 'Find', keys: [[['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands'], [['w'], 'What should we watch?'], [['['], 'Show or hide the sidebar'], [['?'], 'This sheet']] },
  { title: 'Playing', keys: [[['space'], 'Play / pause'], [['k'], 'Play / pause'], [['←', '→'], 'Back / forward 10 s'], [['j', 'l'], 'Back / forward 10 s'], [['↑', '↓'], 'Volume'], [['m'], 'Mute'], [['f'], 'Full screen'], [['c'], 'Subtitles on / off'], [['⇧', 'N'], 'Next episode'], [['esc'], 'Back to the page']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
      <p className="muted small mt">Media keys and the lock screen work too. In search, <kbd>⇧</kbd><kbd>↵</kbd> plays a film or episode straight away.</p>
    </Modal>
  )
}
