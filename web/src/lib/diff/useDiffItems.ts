import { useEffect, useMemo } from 'react';
import type { Comment, Section, SessionResponse } from '../types';
import { fileOrder } from '../order';
import { useViewPrefs } from '../view-prefs';
import { buildItems, commentItemId, fileItemId, parseFiles } from './items';
import type { ReviewItem, SectionFiles } from './items';
import type { ComposerState } from './useComposerDraft';

export interface DiffItems {
  items: ReviewItem[];
  orderedComments: Comment[];
  autoCollapse: ReadonlySet<string>;
}

export function useSectionIndex(sections: readonly Section[]): ReadonlyMap<string, Section> {
  return useMemo(() => new Map(sections.map((s) => [s.sectionKey, s])), [sections]);
}

export function useDiffItems(session: SessionResponse, composer: ComposerState): DiffItems {
  const { viewMode, hideReviewed, expandOverrides } = useViewPrefs();
  const { draft, closeDraft } = composer;

  const sectionFiles = useMemo<SectionFiles[]>(
    () => session.sections.map((section) => ({ section, files: parseFiles(section.patchText) })),
    [session.sections],
  );
  const order = useMemo(() => fileOrder(session, viewMode), [session, viewMode]);
  const showBanners = session.sections.length > 1;
  const autoCollapse = useMemo(
    () =>
      new Set(
        session.sections.flatMap((s) =>
          s.files.filter((f) => f.generated || f.vendored).map((f) => fileItemId(s.sectionKey, f.path)),
        ),
      ),
    [session.sections],
  );
  const items = useMemo(
    () =>
      buildItems(
        sectionFiles,
        session.comments,
        draft,
        order,
        hideReviewed,
        expandOverrides,
        autoCollapse,
        showBanners,
      ),
    [sectionFiles, session.comments, draft, order, hideReviewed, expandOverrides, autoCollapse, showBanners],
  );

  const orderedComments = useMemo(
    () =>
      [...session.comments].sort((a, b) => {
        const ra = order.get(commentItemId(a)) ?? Infinity;
        const rb = order.get(commentItemId(b)) ?? Infinity;
        if (ra !== rb) return ra - rb;
        return a.range.end - b.range.end;
      }),
    [session.comments, order],
  );

  useEffect(() => {
    if (draft && !items.some((item) => item.id === fileItemId(draft.sectionKey, draft.filePath))) {
      closeDraft();
    }
  }, [draft, items, closeDraft]);

  return { items, orderedComments, autoCollapse };
}
