import type { Rect } from './ui/floating';
import { Popover } from './ui/Popover';

export function AnnotationPopover({ label, anchor }: { label: string; anchor: Rect }) {
  return (
    <Popover anchor={anchor} interactive={false} className="annotation-popover">
      <span className="focus-popover-chip">note</span>
      <span className="focus-popover-note">{label}</span>
    </Popover>
  );
}
