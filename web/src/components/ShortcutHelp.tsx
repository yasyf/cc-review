import { Dialog } from './ui/Dialog';

const SHORTCUTS: { keys: string[]; label: string }[] = [
  { keys: ['j', 'k'], label: 'Next / previous file' },
  { keys: ['v'], label: 'Toggle Viewed, then advance' },
  { keys: ['c'], label: 'Collapse / expand file' },
  { keys: ['n', 'p'], label: 'Next / previous comment' },
  { keys: ['['], label: 'Show / hide the stack rail' },
  { keys: [']'], label: 'Show / hide the file tree' },
  { keys: ['⌘', 'K'], label: 'Open the command deck' },
  { keys: ['?'], label: 'Toggle this help' },
  { keys: ['Esc'], label: 'Close help' },
];

export function ShortcutHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onClose={onClose} title="Keyboard shortcuts" className="shortcut-help">
      <dl className="shortcut-help-list">
        {SHORTCUTS.map((s) => (
          <div key={s.label} className="shortcut-help-row">
            <dt>
              {s.keys.map((key) => (
                <kbd key={key}>{key}</kbd>
              ))}
            </dt>
            <dd>{s.label}</dd>
          </div>
        ))}
      </dl>
    </Dialog>
  );
}
