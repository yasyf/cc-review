import type { TurnIndexEntry } from '../lib/attribution';
import type { Rect } from './ui/floating';
import { Popover } from './ui/Popover';

export function TurnPopover({ entry, anchor }: { entry: TurnIndexEntry; anchor: Rect }) {
  return (
    <Popover anchor={anchor} interactive={false} className="turn-popover">
      <span className="turn-chip" style={{ color: `var(--turn-${entry.colorVar})` }}>
        T{entry.seq}
      </span>
      <span className="turn-popover-prompt">{entry.turn.prompt}</span>
      <span className="turn-popover-time">
        {new Date(entry.turn.startedAt).toLocaleTimeString()}
        {entry.turn.interrupted ? ' · interrupted' : ''}
      </span>
    </Popover>
  );
}
