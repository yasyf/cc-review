import { useCallback, useEffect, useRef, useState } from 'react';
import type { SelectedLineRange } from '@pierre/diffs';
import type { Section } from '../types';
import { clearDraft, composerDraftKey } from '../drafts';
import { parseItemId } from './items';
import type { CodeViewRef, ComposerDraft } from './items';

export interface ComposerState {
  draft: ComposerDraft | null;
  openDraft(itemId: string, range: SelectedLineRange): void;
  closeDraft(): void;
}

export function useComposerDraft(
  codeView: CodeViewRef,
  sectionByKey: ReadonlyMap<string, Section>,
  readOnly: boolean,
): ComposerState {
  const [draft, setDraft] = useState<ComposerDraft | null>(null);
  const seqRef = useRef(0);
  const sectionByKeyRef = useRef(sectionByKey);
  useEffect(() => {
    sectionByKeyRef.current = sectionByKey;
  });

  const openDraft = useCallback((itemId: string, range: SelectedLineRange) => {
    const parsed = parseItemId(itemId);
    const section = sectionByKeyRef.current.get(parsed.sectionKey);
    if (!section) return;
    setDraft({
      sectionId: section.sectionId,
      sectionKey: parsed.sectionKey,
      filePath: parsed.path,
      range,
      seq: ++seqRef.current,
    });
  }, []);

  const closeDraft = useCallback(() => {
    clearDraft(composerDraftKey);
    setDraft(null);
    codeView.current?.clearSelectedLines();
  }, [codeView]);

  useEffect(() => {
    if (readOnly) closeDraft();
  }, [readOnly, closeDraft]);

  return { draft, openDraft, closeDraft };
}
