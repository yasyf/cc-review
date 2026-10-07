import { useCallback, useState } from 'react';
import type { RefObject } from 'react';
import type { LineTypes } from '@pierre/diffs';
import { annotationAt } from '../annotations';
import { turnIdAt } from '../attribution';
import type { TurnIndexEntry } from '../attribution';
import { noteAt } from '../importance';
import type { LineLevel, Side } from '../types';
import type { LineLookups } from './useDecorations';

export type LineHover =
  | { kind: 'annotation'; label: string; anchor: DOMRect }
  | { kind: 'focus'; note: string; level: LineLevel; anchor: DOMRect }
  | { kind: 'turn'; entry: TurnIndexEntry; anchor: DOMRect };

export interface LineEnterProps {
  lineNumber: number;
  lineElement: HTMLElement;
  lineType?: LineTypes;
}

function hoverFor(props: LineEnterProps, itemId: string, lookups: LineLookups): LineHover | null {
  if (props.lineType !== 'change-addition' && props.lineType !== 'change-deletion') return null;
  const anchor = props.lineElement.getBoundingClientRect();
  const side: Side = props.lineType === 'change-deletion' ? 'deletions' : 'additions';
  const annotation = annotationAt(lookups.annotations[itemId] ?? [], side, props.lineNumber);
  if (annotation?.label) return { kind: 'annotation', label: annotation.label, anchor };
  if (side === 'deletions') return null;

  const notes = lookups.importance.get(itemId);
  const note = lookups.focusMode && lookups.activeTurnId === null && notes ? noteAt(notes, props.lineNumber) : undefined;
  if (note) return { kind: 'focus', note: note.note, level: note.level, anchor };

  const turnId = turnIdAt(lookups.attributions[itemId] ?? [], props.lineNumber);
  const entry = turnId ? lookups.turnIndex.get(turnId) : undefined;
  return entry ? { kind: 'turn', entry, anchor } : null;
}

export function useLineHover(lookups: RefObject<LineLookups>) {
  const [hover, setHover] = useState<LineHover | null>(null);

  const onLineEnter = useCallback(
    (props: LineEnterProps, context: { item: { id: string } }) =>
      setHover(hoverFor(props, context.item.id, lookups.current)),
    [lookups],
  );
  const onLineLeave = useCallback(() => setHover(null), []);

  return { hover, onLineEnter, onLineLeave };
}
