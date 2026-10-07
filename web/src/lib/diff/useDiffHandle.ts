import { useCallback, useEffect, useImperativeHandle, useRef } from 'react';
import type { Ref } from 'react';
import { useSetFileStates } from '../api';
import { useReview } from '../review-context';
import type { Comment, Section } from '../types';
import { useViewPrefs } from '../view-prefs';
import { commentItemId, parseItemId } from './items';
import type { CodeViewInstance, CodeViewRef, FileRef, ReviewItem } from './items';
import { CURRENT_ITEM_OFFSET_PX } from './useScrollSync';
import type { ScrollSync } from './useScrollSync';

/** The imperative surface the app uses to navigate the diff without touching @pierre/diffs. */
export interface DiffViewHandle {
  scrollToFile(ref: FileRef): void;
  scrollToComment(comment: Comment): void;
  focusNextFile(): void;
  focusPrevFile(): void;
  toggleViewedCurrent(): void;
  toggleCollapseCurrent(): void;
  focusNextComment(): void;
  focusPrevComment(): void;
}

function commentIndexNearScroll(viewer: CodeViewInstance, comments: readonly Comment[]): number {
  const scrollTop = viewer.getScrollTop();
  let index = 0;
  for (let i = 0; i < comments.length; i++) {
    const top = viewer.getTopForItem(commentItemId(comments[i]));
    if (top !== undefined && top <= scrollTop + CURRENT_ITEM_OFFSET_PX) index = i + 1;
  }
  return index;
}

function stepTarget(list: readonly ReviewItem[], currentId: string | null, dir: 1 | -1): string | null {
  if (list.length === 0) return null;
  const idx = list.findIndex((item) => item.id === currentId);
  if (idx < 0) return list[0].id;
  return list[Math.max(0, Math.min(idx + dir, list.length - 1))].id;
}

export function useDiffHandle(
  ref: Ref<DiffViewHandle> | undefined,
  {
    codeView,
    items,
    orderedComments,
    sectionByKey,
    scroll,
  }: {
    codeView: CodeViewRef;
    items: readonly ReviewItem[];
    orderedComments: readonly Comment[];
    sectionByKey: ReadonlyMap<string, Section>;
    scroll: ScrollSync;
  },
): void {
  const { slug, version } = useReview();
  const { mutate: mutateStates } = useSetFileStates(slug, version);
  const { toggleExpandOverride, clearExpandOverride } = useViewPrefs();
  const latest = useRef({ items, orderedComments, sectionByKey });
  useEffect(() => {
    latest.current = { items, orderedComments, sectionByKey };
  });

  const { currentItemRef, goToItem, scrollToFile, scrollToComment } = scroll;

  const stepFile = useCallback(
    (dir: 1 | -1) => {
      const target = stepTarget(latest.current.items, currentItemRef.current, dir);
      if (target) goToItem(target);
    },
    [currentItemRef, goToItem],
  );

  const stepComment = useCallback(
    (dir: 1 | -1) => {
      const comments = latest.current.orderedComments;
      const viewer = codeView.current?.getInstance();
      if (comments.length === 0 || !viewer) return;
      const i = commentIndexNearScroll(viewer, comments);
      scrollToComment(comments[dir === 1 ? Math.min(i, comments.length - 1) : Math.max(i - 1, 0)]);
    },
    [codeView, scrollToComment],
  );

  useImperativeHandle(
    ref,
    () => ({
      scrollToFile,
      scrollToComment,
      focusNextFile: () => stepFile(1),
      focusPrevFile: () => stepFile(-1),
      toggleViewedCurrent() {
        const id = currentItemRef.current;
        if (!id) return;
        const parsed = parseItemId(id);
        const reviewed = latest.current.sectionByKey.get(parsed.sectionKey)?.fileStates[parsed.path]?.reviewed ?? false;
        if (reviewed) {
          mutateStates([{ sectionKey: parsed.sectionKey, path: parsed.path, reviewed: false }]);
          return;
        }
        // Capture the next file before mutating: with hideReviewed on, the viewed
        // file leaves `items` and the indices shift.
        const list = latest.current.items;
        const nextId = list[list.findIndex((item) => item.id === id) + 1]?.id ?? null;
        clearExpandOverride(id);
        mutateStates([{ sectionKey: parsed.sectionKey, path: parsed.path, reviewed: true }]);
        if (nextId) goToItem(nextId);
      },
      toggleCollapseCurrent() {
        const id = currentItemRef.current;
        if (id) toggleExpandOverride(id);
      },
      focusNextComment: () => stepComment(1),
      focusPrevComment: () => stepComment(-1),
    }),
    [
      scrollToFile,
      scrollToComment,
      stepFile,
      stepComment,
      currentItemRef,
      goToItem,
      mutateStates,
      toggleExpandOverride,
      clearExpandOverride,
    ],
  );
}
