const SHORTCUTS: { keys: string; label: string }[] = [
  { keys: 'j / k', label: 'Next / previous file' },
  { keys: 'v', label: 'Toggle Viewed, then advance' },
  { keys: 'c', label: 'Collapse / expand file' },
  { keys: 'n / p', label: 'Next / previous comment' },
  { keys: '?', label: 'Toggle this help' },
  { keys: 'Esc', label: 'Close help' },
];

export function ShortcutHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  if (!open) return null;
  return (
    <div className="shortcut-help" onClick={onClose}>
      <div className="shortcut-help-panel" onClick={(e) => e.stopPropagation()}>
        <div className="shortcut-help-title">Keyboard shortcuts</div>
        <dl className="shortcut-help-list">
          {SHORTCUTS.map((s) => (
            <div key={s.keys} className="shortcut-help-row">
              <dt>
                <kbd>{s.keys}</kbd>
              </dt>
              <dd>{s.label}</dd>
            </div>
          ))}
        </dl>
      </div>
    </div>
  );
}
