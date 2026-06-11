import type { TurnIndexEntry } from '../lib/attribution';

export function TurnPopover({ entry, x, y }: { entry: TurnIndexEntry; x: number; y: number }) {
  return (
    <div className="turn-popover" style={{ left: x, top: y }}>
      <span
        className="turn-chip"
        style={{ color: `var(--turn-${entry.colorVar})` }}
      >
        T{entry.seq}
      </span>
      <span className="turn-popover-prompt">{entry.turn.prompt}</span>
      <span className="turn-popover-time">
        {new Date(entry.turn.startedAt).toLocaleTimeString()}
        {entry.turn.interrupted ? ' · interrupted' : ''}
      </span>
    </div>
  );
}
