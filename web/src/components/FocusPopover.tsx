import type { LineLevel } from '../lib/types';

export function FocusPopover({
  note,
  level,
  x,
  y,
}: {
  note: string;
  level: LineLevel;
  x: number;
  y: number;
}) {
  return (
    <div className={`focus-popover focus-popover-${level}`} style={{ left: x, top: y }}>
      <span className="focus-popover-chip">{level}</span>
      <span className="focus-popover-note">{note}</span>
    </div>
  );
}
