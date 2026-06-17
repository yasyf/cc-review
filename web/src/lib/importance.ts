import type { LineNote, Organization } from './types';

// Injected into each diff's shadow root via the CodeView `unsafeCSS` option.
// decorateImportance stamps data-cc-importance; the focus tier draws a gutter
// dot. opacity is one property and cannot blend, so this and TURN_UNSAFE_CSS's
// [data-cc-dim] never coexist on a row (decorateImportance is a no-op whenever a
// turn is selected — turn-dim owns opacity then).
export const IMPORTANCE_UNSAFE_CSS = `
[data-cc-importance="default"] { opacity: 0.72; }
[data-cc-importance="mechanical"] { opacity: 0.4; }
[data-cc-importance="focus"] { opacity: 1; position: relative; }
[data-cc-importance="focus"]::before {
  content: "";
  position: absolute;
  left: 3px;
  top: 50%;
  transform: translateY(-50%);
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--cc-focus-dot, #e0a000);
}
`;

export type ImportanceIndex = ReadonlyMap<string, readonly LineNote[]>;

export function buildImportanceIndex(org: Organization | null): ImportanceIndex {
  const map = new Map<string, LineNote[]>();
  if (!org) return map;
  for (const chapter of org.chapters) {
    for (const file of chapter.files) {
      if (!file.lines || file.lines.length === 0) continue;
      if (map.has(file.path)) continue;
      map.set(file.path, [...file.lines].sort((a, b) => a.start - b.start));
    }
  }
  return map;
}

export function noteAt(notes: readonly LineNote[], line: number): LineNote | undefined {
  let lo = 0;
  let hi = notes.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const n = notes[mid];
    if (line < n.start) hi = mid - 1;
    else if (line > n.end) lo = mid + 1;
    else return n;
  }
  return undefined;
}

// Idempotent: stamps data-cc-importance on the new-side addition rows of one
// rendered diff and strips it whenever the gradient must not own opacity (a turn
// is selected, focus mode is off, or the file has no notes), so it can re-run on
// every post-render and on focusMode/activeTurnId changes.
export function decorateImportance(
  rootEl: HTMLElement,
  fileNotes: readonly LineNote[] | null,
  focusMode: boolean,
  activeTurnId: string | null,
): void {
  const rows = rootEl.shadowRoot?.querySelectorAll<HTMLElement>(
    '[data-line][data-line-type="change-addition"]',
  );
  const active = focusMode && activeTurnId === null && fileNotes !== null;
  for (const row of rows ?? []) {
    if (!active) {
      delete row.dataset.ccImportance;
      continue;
    }
    const note = noteAt(fileNotes!, Number(row.dataset.line));
    row.dataset.ccImportance = note ? note.level : 'default';
  }
}
