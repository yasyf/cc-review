import type { LineLevel } from '../lib/types';
import type { Rect } from './ui/floating';
import { Popover } from './ui/Popover';

export function FocusPopover({ note, level, anchor }: { note: string; level: LineLevel; anchor: Rect }) {
  return (
    <Popover anchor={anchor} interactive={false} className={`focus-popover focus-popover-${level}`}>
      <span className="focus-popover-chip">{level}</span>
      <span className="focus-popover-note">{note}</span>
    </Popover>
  );
}
