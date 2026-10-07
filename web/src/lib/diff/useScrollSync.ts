import { useCallback, useEffect, useRef, useState } from 'react';
import { useSetFileStates } from '../api';
import { firstOccurrence } from '../attribution';
import { useReview } from '../review-context';
import type { AttributionRange, Comment, Section } from '../types';
import { useViewPrefs } from '../view-prefs';
import { isInlineThread } from '../threads';
import { commentItemId, fileItemId, parseItemId } from './items';
import type { CodeViewInstance, CodeViewRef, FileRef, ReviewItem } from './items';

// The current file switches when the next file's top crosses the viewport top
// (under the sticky header); +1px keeps the boundary inclusive.
export const CURRENT_ITEM_OFFSET_PX = 1;

// A pending scroll whose target never re-enters `items` (e.g. its reveal mutation
// failed) must not fire a surprise scrollTo minutes later.
const MAX_PENDING_SCROLL_MISSES = 5;

type PendingScroll = { kind: 'file'; id: string } | { kind: 'comment'; comment: Comment };

export interface ScrollSync {
  currentItemRef: { readonly current: string | null };
  syncCurrentFromScroll(viewer: CodeViewInstance): void;
  goToItem(id: string): void;
  scrollToFile(ref: FileRef): void;
  scrollToComment(comment: Comment): void;
}

export function useScrollSync({
  codeView,
  items,
  sectionByKey,
  autoCollapse,
  attributions,
}: {
  codeView: CodeViewRef;
  items: readonly ReviewItem[];
  sectionByKey: ReadonlyMap<string, Section>;
  autoCollapse: ReadonlySet<string>;
  attributions: Readonly<Record<string, AttributionRange[]>>;
}): ScrollSync {
  const { slug, version } = useReview();
  const { mutate: mutateStates } = useSetFileStates(slug, version);
  const { expandOverrides, toggleExpandOverride, activeTurnId } = useViewPrefs();
  const [pendingScroll, setPendingScroll] = useState<PendingScroll | null>(null);
  const pendingScrollMisses = useRef(0);
  // Current-item tracking is DOM-only (a `.file-current` class); routing it through
  // items would re-collapse on every scroll.
  const currentItemRef = useRef<string | null>(null);
  const itemsRef = useRef(items);
  useEffect(() => {
    itemsRef.current = items;
  });

  const applyCurrentItem = useCallback(
    (id: string | null) => {
      if (currentItemRef.current === id) return;
      currentItemRef.current = id;
      const viewer = codeView.current?.getInstance();
      if (!viewer) return;
      for (const rendered of viewer.getRenderedItems()) {
        rendered.element.classList.toggle('file-current', rendered.id === id);
      }
    },
    [codeView],
  );

  const syncCurrentFromScroll = useCallback(
    (viewer: CodeViewInstance) => {
      const scrollTop = viewer.getScrollTop();
      const list = itemsRef.current;
      let currentId: string | null = list.length > 0 ? list[0].id : null;
      for (const item of list) {
        const top = viewer.getTopForItem(item.id);
        if (top === undefined) continue;
        if (top <= scrollTop + CURRENT_ITEM_OFFSET_PX) currentId = item.id;
        else break;
      }
      applyCurrentItem(currentId);
    },
    [applyCurrentItem],
  );

  useEffect(() => {
    const viewer = codeView.current?.getInstance();
    if (viewer) syncCurrentFromScroll(viewer);
  }, [codeView, items, syncCurrentFromScroll]);

  const goToItem = useCallback(
    (id: string) => {
      codeView.current?.scrollTo({ type: 'item', id, align: 'start', behavior: 'smooth' });
      applyCurrentItem(id);
    },
    [codeView, applyCurrentItem],
  );

  const reveal = useCallback(
    (itemId: string) => {
      const parsed = parseItemId(itemId);
      const state = sectionByKey.get(parsed.sectionKey)?.fileStates[parsed.path];
      if (state?.hidden) {
        mutateStates([{ sectionKey: parsed.sectionKey, path: parsed.path, hidden: false }], {
          onError: () => setPendingScroll(null),
        });
      }
      if ((state?.reviewed || autoCollapse.has(itemId)) && !expandOverrides.has(itemId)) {
        toggleExpandOverride(itemId);
      }
    },
    [sectionByKey, expandOverrides, toggleExpandOverride, mutateStates, autoCollapse],
  );

  const scrollToFile = useCallback(
    (ref: FileRef) => {
      const id = fileItemId(ref.sectionKey, ref.path);
      reveal(id);
      pendingScrollMisses.current = 0;
      setPendingScroll({ kind: 'file', id });
    },
    [reveal],
  );

  const scrollToComment = useCallback(
    (comment: Comment) => {
      reveal(commentItemId(comment));
      pendingScrollMisses.current = 0;
      setPendingScroll({ kind: 'comment', comment });
    },
    [reveal],
  );

  useEffect(() => {
    if (!pendingScroll) return;
    const id = pendingScroll.kind === 'file' ? pendingScroll.id : commentItemId(pendingScroll.comment);
    if (!items.some((item) => item.id === id)) {
      if (++pendingScrollMisses.current >= MAX_PENDING_SCROLL_MISSES) setPendingScroll(null);
      return;
    }
    if (pendingScroll.kind === 'file' || !isInlineThread(pendingScroll.comment)) {
      codeView.current?.scrollTo({ type: 'item', id, align: 'start', behavior: 'smooth' });
    } else {
      const { range } = pendingScroll.comment;
      codeView.current?.scrollTo({
        type: 'range',
        id,
        range: {
          start: range.start,
          end: range.end,
          ...(range.startSide ? { side: range.startSide } : {}),
          ...(range.endSide ? { endSide: range.endSide } : {}),
        },
        align: 'center',
        behavior: 'smooth',
      });
    }
    setPendingScroll(null);
  }, [codeView, pendingScroll, items]);

  const prevActiveTurnId = useRef<string | null>(null);
  useEffect(() => {
    if (activeTurnId !== null && activeTurnId !== prevActiveTurnId.current) {
      const target = firstOccurrence(attributions, activeTurnId);
      if (target) {
        codeView.current?.scrollTo({
          type: 'range',
          id: target.file,
          range: { start: target.line, end: target.line, side: 'additions', endSide: 'additions' },
          align: 'center',
          behavior: 'smooth',
        });
      }
    }
    prevActiveTurnId.current = activeTurnId;
  }, [codeView, activeTurnId, attributions]);

  return { currentItemRef, syncCurrentFromScroll, goToItem, scrollToFile, scrollToComment };
}
