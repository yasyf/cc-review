import { fileItemId } from './diff';
import type { Annotation, Side } from './types';

export const ANNOTATION_UNSAFE_CSS = `
[data-cc-annotation] {
  background: var(--annotation-bg);
}
`;

// Group annotations by itemId for per-container decoration.
export function annotationsByItem(
  annotations: readonly Annotation[],
): Record<string, Annotation[]> {
  const out: Record<string, Annotation[]> = {};
  for (const annotation of annotations) {
    (out[fileItemId(annotation.sectionKey, annotation.filePath)] ??= []).push(annotation);
  }
  return out;
}

function covers(annotations: readonly Annotation[], side: Side, line: number): Annotation | undefined {
  return annotations.find((a) => a.side === side && line >= a.start && line <= a.end);
}

// Idempotent: stamps data-cc-annotation on the change rows one rendered diff
// container covers and strips it from rows that no longer match, so it can re-run
// on every post-render and on annotation updates — mirrors decorateContainer.
export function decorateAnnotations(
  rootEl: HTMLElement,
  fileAnnotations: readonly Annotation[],
): void {
  const rows = rootEl.shadowRoot?.querySelectorAll<HTMLElement>(
    '[data-line][data-line-type="change-addition"], [data-line][data-line-type="change-deletion"]',
  );
  for (const row of rows ?? []) {
    const side: Side = row.dataset.lineType === 'change-deletion' ? 'deletions' : 'additions';
    const hit = covers(fileAnnotations, side, Number(row.dataset.line));
    if (hit) {
      row.dataset.ccAnnotation = '';
      if (hit.label) row.title = hit.label;
    } else {
      delete row.dataset.ccAnnotation;
      if (row.title) row.removeAttribute('title');
    }
  }
}
