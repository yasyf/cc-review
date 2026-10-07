import { useCallback, useEffect, useMemo, useRef } from 'react';
import type { RefObject } from 'react';
import { annotationsByItem, decorateAnnotations } from '../annotations';
import { buildTurnIndex, decorateContainer } from '../attribution';
import type { TurnIndex } from '../attribution';
import { buildImportanceIndex, decorateImportance } from '../importance';
import type { ImportanceIndex } from '../importance';
import type { Annotation, AttributionRange, SessionResponse } from '../types';
import { useViewPrefs } from '../view-prefs';
import { fileItemId } from './items';
import type { CodeViewRef } from './items';

export interface LineLookups {
  attributions: Readonly<Record<string, AttributionRange[]>>;
  turnIndex: TurnIndex;
  importance: ImportanceIndex;
  annotations: Readonly<Record<string, Annotation[]>>;
  focusMode: boolean;
  activeTurnId: string | null;
}

export interface Decorations {
  lookups: RefObject<LineLookups>;
  decorate(node: HTMLElement, itemId: string): void;
}

function paint(node: HTMLElement, itemId: string, lookups: LineLookups): void {
  decorateContainer(node, lookups.attributions[itemId] ?? [], lookups.turnIndex, lookups.activeTurnId);
  decorateImportance(node, lookups.importance.get(itemId) ?? null, lookups.focusMode, lookups.activeTurnId);
  decorateAnnotations(node, lookups.annotations[itemId] ?? []);
}

export function useAttributionIndex(session: SessionResponse): Record<string, AttributionRange[]> {
  return useMemo(() => {
    const out: Record<string, AttributionRange[]> = {};
    const pending = session.sections.find((s) => s.pending);
    if (pending?.attributions) {
      for (const [path, ranges] of Object.entries(pending.attributions)) {
        out[fileItemId(pending.sectionKey, path)] = ranges;
      }
    }
    return out;
  }, [session.sections]);
}

export function useDecorations(
  codeView: CodeViewRef,
  session: SessionResponse,
  attributions: Record<string, AttributionRange[]>,
): Decorations {
  const { focusMode, activeTurnId } = useViewPrefs();
  const turnIndex = useMemo(() => buildTurnIndex(session.turns), [session.turns]);
  const importance = useMemo(() => buildImportanceIndex(session.sections), [session.sections]);
  const annotations = useMemo(() => annotationsByItem(session.annotations), [session.annotations]);

  const current = useMemo<LineLookups>(
    () => ({ attributions, turnIndex, importance, annotations, focusMode, activeTurnId }),
    [attributions, turnIndex, importance, annotations, focusMode, activeTurnId],
  );

  // Decorations ride a ref so CodeView's options keep their identity: an options
  // swap would re-render every diff.
  const lookups = useRef(current);
  useEffect(() => {
    lookups.current = current;
    const instance = codeView.current?.getInstance();
    if (!instance) return;
    for (const rendered of instance.getRenderedItems()) paint(rendered.element, rendered.id, current);
  }, [codeView, current]);

  const decorate = useCallback((node: HTMLElement, itemId: string) => paint(node, itemId, lookups.current), []);

  return { lookups, decorate };
}
