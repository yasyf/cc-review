import { useCallback, useMemo, useRef } from 'react';
import type { Ref } from 'react';
import type { CodeViewOptions, PostRenderPhase, SelectedLineRange } from '@pierre/diffs';
import { CodeView } from '@pierre/diffs/react';
import type { CodeViewHandle } from '@pierre/diffs/react';
import { ANNOTATION_UNSAFE_CSS } from '../lib/annotations';
import { TURN_UNSAFE_CSS } from '../lib/attribution';
import { parseItemId } from '../lib/diff/items';
import type { AnnotationMeta, ReviewItem } from '../lib/diff/items';
import { useComposerDraft } from '../lib/diff/useComposerDraft';
import { useAttributionIndex, useDecorations } from '../lib/diff/useDecorations';
import { useDiffHandle } from '../lib/diff/useDiffHandle';
import type { DiffViewHandle } from '../lib/diff/useDiffHandle';
import { useDiffItems, useSectionIndex } from '../lib/diff/useDiffItems';
import { useLineHover } from '../lib/diff/useLineHover';
import { useScrollSync } from '../lib/diff/useScrollSync';
import { IMPORTANCE_UNSAFE_CSS } from '../lib/importance';
import { useThemeMode } from '../lib/theme';
import type { SessionResponse } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { themes } from '../worker';
import { AnnotationPopover } from './AnnotationPopover';
import { CommentThread } from './CommentThread';
import { FileHeaderControls } from './FileHeaderControls';
import { FocusPopover } from './FocusPopover';
import { InlineComposer } from './InlineComposer';
import { TurnPopover } from './TurnPopover';

const UNSAFE_CSS = TURN_UNSAFE_CSS + IMPORTANCE_UNSAFE_CSS + ANNOTATION_UNSAFE_CSS;

export function DiffView({ session, ref }: { session: SessionResponse; ref?: Ref<DiffViewHandle> }) {
  const { diffStyle } = useViewPrefs();
  const themeMode = useThemeMode();
  const codeView = useRef<CodeViewHandle<AnnotationMeta, undefined>>(null);
  const readOnly = session.review.status !== 'open';

  const sectionByKey = useSectionIndex(session.sections);
  const composer = useComposerDraft(codeView, sectionByKey, readOnly);
  const { draft, openDraft, closeDraft } = composer;
  const { items, orderedComments, autoCollapse } = useDiffItems(session, composer);
  const attributions = useAttributionIndex(session);
  const { lookups, decorate } = useDecorations(codeView, session, attributions);
  const { hover, onLineEnter, onLineLeave } = useLineHover(lookups);
  const scroll = useScrollSync({ codeView, items, sectionByKey, autoCollapse, attributions });
  const { currentItemRef, syncCurrentFromScroll } = scroll;
  useDiffHandle(ref, { codeView, items, orderedComments, sectionByKey, scroll });

  const options = useMemo<CodeViewOptions<AnnotationMeta, undefined>>(
    () => ({
      theme: themes,
      themeType: themeMode,
      diffStyle,
      stickyHeaders: true,
      enableLineSelection: !readOnly,
      enableGutterUtility: !readOnly,
      // The selection commit (pointer-up) is the single open/close authority: never
      // onSelectedLinesChange (fires per drag frame) nor the gutter callback (the same
      // pointer-up double-opens). A null commit is the single-line unselect gesture.
      onLineSelected: (range: SelectedLineRange | null, context: { item: { id: string } }) => {
        if (range) openDraft(context.item.id, range);
        else closeDraft();
      },
      // Must stay non-null: the library only routes "+" pointer-downs into gutter
      // selection when this callback exists.
      onGutterUtilityClick: () => {},
      unsafeCSS: UNSAFE_CSS,
      onPostRender: (node: HTMLElement, _instance: unknown, phase: PostRenderPhase, context: { item: { id: string } }) => {
        if (phase === 'unmount') return;
        decorate(node, context.item.id);
        node.classList.toggle('file-current', context.item.id === currentItemRef.current);
      },
      onLineEnter,
      onLineLeave,
    }),
    [themeMode, diffStyle, readOnly, openDraft, closeDraft, decorate, currentItemRef, onLineEnter, onLineLeave],
  );

  const renderAnnotation = useCallback(
    (annotation: { metadata: AnnotationMeta }, item: ReviewItem) => {
      if (annotation.metadata.kind === 'thread') {
        return <CommentThread commentId={annotation.metadata.commentId} />;
      }
      if (!draft || item.type !== 'diff') return null;
      return <InlineComposer draft={draft} fileDiff={item.fileDiff} onClose={closeDraft} />;
    },
    [draft, closeDraft],
  );

  const renderHeaderMetadata = useCallback((item: ReviewItem) => {
    const ref = parseItemId(item.id);
    return <FileHeaderControls sectionKey={ref.sectionKey} path={ref.path} />;
  }, []);

  return (
    <div className="diff">
      <CodeView<AnnotationMeta, undefined>
        ref={codeView}
        className="codeview"
        items={items}
        options={options}
        onScroll={(_, viewer) => syncCurrentFromScroll(viewer)}
        renderAnnotation={renderAnnotation}
        renderHeaderMetadata={renderHeaderMetadata}
      />
      {hover?.kind === 'annotation' ? <AnnotationPopover label={hover.label} anchor={hover.anchor} /> : null}
      {hover?.kind === 'focus' ? <FocusPopover note={hover.note} level={hover.level} anchor={hover.anchor} /> : null}
      {hover?.kind === 'turn' ? <TurnPopover entry={hover.entry} anchor={hover.anchor} /> : null}
    </div>
  );
}
